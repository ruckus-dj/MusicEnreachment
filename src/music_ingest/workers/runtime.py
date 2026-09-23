from __future__ import annotations

import logging
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from datetime import UTC, datetime
from threading import Lock
from types import MappingProxyType

import anyio
from anyio.to_thread import run_sync
from sqlalchemy.orm import Session

from music_ingest.contracts.settings import WorkerPoolSettings
from music_ingest.services.settings import SettingKey, get_setting_value
from music_ingest.workers.config import ProcessingConfig
from music_ingest.workers.worker import ProcessingWorker

LOGGER = logging.getLogger(__name__)
MAX_POOL_CONCURRENCY = 8
WORKER_POOLS: dict[str, frozenset[str]] = {name: frozenset({name}) for name in WorkerPoolSettings.model_fields}
WORKER_POOLS['musicbrainz_analysis'] = frozenset({'musicbrainz_analysis', 'musicbrainz_refresh'})


@dataclass(frozen=True, slots=True)
class WorkerSlotSnapshot:
    slot: int
    state: str
    observed_at: datetime
    error: str | None
    job_id: str | None
    job_kind: str | None
    pool: str | None


class ProcessingRuntimeMonitor:
    def __init__(self) -> None:
        self._lock: Lock = Lock()
        self._slots: dict[int, WorkerSlotSnapshot] = {}

    def observe(
        self,
        slot: int,
        state: str,
        error: str | None = None,
        job_id: str | None = None,
        job_kind: str | None = None,
        pool: str | None = None,
    ) -> None:
        with self._lock:
            self._slots[slot] = WorkerSlotSnapshot(slot, state, datetime.now(UTC), error, job_id, job_kind, pool)

    def snapshots(self) -> tuple[WorkerSlotSnapshot, ...]:
        with self._lock:
            return tuple(self._slots[index] for index in sorted(self._slots))


async def run_processing_worker(
    session_factory: Callable[[], Session],
    config: ProcessingConfig,
    poll_seconds: float = 1.0,
    monitor: ProcessingRuntimeMonitor | None = None,
) -> None:
    settings = PoolSettingsSnapshot()
    # Each pool owns its threads and slots. Idle capacity is never lent to another pool.
    async with anyio.create_task_group() as task_group:
        task_group.start_soon(_refresh_pool_settings, session_factory, settings, poll_seconds)
        for pool_index, (pool, kinds) in enumerate(WORKER_POOLS.items()):
            limiter = anyio.CapacityLimiter(MAX_POOL_CONCURRENCY)
            for local_slot in range(MAX_POOL_CONCURRENCY):
                task_group.start_soon(
                    _run_processing_worker_slot,
                    session_factory,
                    config,
                    pool_index * MAX_POOL_CONCURRENCY + local_slot,
                    local_slot,
                    pool,
                    kinds,
                    limiter,
                    poll_seconds,
                    monitor,
                    settings,
                )
        task_group.start_soon(_run_maintenance, session_factory, config, poll_seconds)


async def _run_processing_worker_slot(
    session_factory: Callable[[], Session],
    config: ProcessingConfig,
    worker_slot: int,
    local_slot: int,
    pool: str,
    kinds: frozenset[str],
    limiter: anyio.CapacityLimiter,
    poll_seconds: float,
    monitor: ProcessingRuntimeMonitor | None,
    settings: PoolSettingsSnapshot,
) -> None:
    while True:
        try:
            count = settings.counts[pool]
            if local_slot >= count:
                if monitor is not None:
                    monitor.observe(worker_slot, 'disabled', pool=pool)
                processed = False
            else:
                processed = await run_sync(
                    _run_processing_once,
                    session_factory,
                    config,
                    worker_slot,
                    monitor,
                    pool,
                    kinds,
                    limiter=limiter,
                )
                if monitor is not None:
                    monitor.observe(worker_slot, 'idle', pool=pool)
        except Exception as error:  # noqa: BLE001
            LOGGER.exception('processing worker iteration failed; continuing poll loop')
            if monitor is not None:
                monitor.observe(worker_slot, 'error', str(error), pool=pool)
            processed = False
        if not processed:
            await anyio.sleep(poll_seconds)


def _run_processing_once(
    session_factory: Callable[[], Session],
    config: ProcessingConfig,
    worker_slot: int,
    monitor: ProcessingRuntimeMonitor | None,
    pool: str,
    kinds: frozenset[str],
) -> bool:
    with session_factory() as session:

        def observe_claim(job_id: str, job_kind: str) -> None:
            if monitor is not None:
                monitor.observe(worker_slot, 'processing', job_id=job_id, job_kind=job_kind, pool=pool)

        processed = ProcessingWorker(session, config).run_once(on_claimed=observe_claim, allowed_kinds=kinds)
        session.commit()
        return processed


@dataclass(slots=True)
class PoolSettingsSnapshot:
    # Replaced atomically by the event-loop refresher, never mutated by slots.
    counts: Mapping[str, int] = field(default_factory=lambda: MappingProxyType(dict.fromkeys(WORKER_POOLS, 0)))


def _read_pool_settings(session_factory: Callable[[], Session]) -> Mapping[str, int]:
    with session_factory() as session:
        value = get_setting_value(session, SettingKey.WORKER_POOLS)
        settings = WorkerPoolSettings.model_validate_json(value or '{}')
        return MappingProxyType({pool: int(getattr(settings, pool)) for pool in WORKER_POOLS})


async def _refresh_pool_settings(
    session_factory: Callable[[], Session], settings: PoolSettingsSnapshot, poll_seconds: float
) -> None:
    limiter = anyio.CapacityLimiter(1)
    while True:
        try:
            settings.counts = await run_sync(_read_pool_settings, session_factory, limiter=limiter)
        except Exception:  # noqa: BLE001
            LOGGER.exception('pool settings refresh failed; retaining last immutable snapshot')
        await anyio.sleep(poll_seconds)


async def _run_maintenance(
    session_factory: Callable[[], Session],
    config: ProcessingConfig,
    poll_seconds: float,
) -> None:
    limiter = anyio.CapacityLimiter(1)
    while True:
        try:
            await run_sync(_maintenance_once, session_factory, config, limiter=limiter)
        except Exception:  # noqa: BLE001
            LOGGER.exception('storage maintenance failed; continuing poll loop')
        await anyio.sleep(poll_seconds)


def _maintenance_once(session_factory: Callable[[], Session], config: ProcessingConfig) -> None:
    with session_factory() as session:
        ProcessingWorker(session, config).maintain_storage()
        session.commit()
