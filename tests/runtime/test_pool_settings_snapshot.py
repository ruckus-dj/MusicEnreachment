from __future__ import annotations

from collections.abc import Callable, Mapping
from pathlib import Path
from types import MappingProxyType

import anyio
import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

import music_ingest.workers.runtime as runtime
from music_ingest.contracts.settings import RuntimeSettings
from music_ingest.models import Base
from music_ingest.services.settings import save_runtime_settings
from music_ingest.workers.config import ProcessingConfig


def test_pool_settings_reads_one_immutable_snapshot_and_picks_up_persisted_updates(tmp_path: Path) -> None:
    engine = create_engine(f'sqlite:///{tmp_path / "settings.db"}')
    Base.metadata.create_all(engine)

    def factory() -> Session:
        return Session(engine)

    old = runtime._read_pool_settings(factory)
    with pytest.raises(TypeError):
        old['final_publish'] = 8
    with Session(engine) as session:
        settings = RuntimeSettings()
        settings = settings.model_copy(
            update={'worker_pools': settings.worker_pools.model_copy(update={'final_publish': 7})}
        )
        save_runtime_settings(session, settings)
        session.commit()
    current = runtime._read_pool_settings(factory)
    assert old['final_publish'] == 4
    assert current['final_publish'] == 7
    assert current['lrclib_fetch'] == old['lrclib_fetch']


def test_runtime_refreshes_once_for_all_slots_and_retains_snapshot_on_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    reads: list[Mapping[str, int]] = []
    observed: set[int] = set()
    monitor = runtime.ProcessingRuntimeMonitor()

    def read(_factory: Callable[[], Session]) -> Mapping[str, int]:
        snapshot = MappingProxyType({pool: (1 if pool == 'final_publish' else 0) for pool in runtime.WORKER_POOLS})
        if reads:
            snapshot = MappingProxyType({**snapshot, 'final_publish': 3})
        reads.append(snapshot)
        if len(reads) >= 3:
            raise OSError('settings database unavailable')
        return snapshot

    def process(
        _factory: Callable[[], Session],
        _config: ProcessingConfig,
        slot: int,
        _monitor: runtime.ProcessingRuntimeMonitor | None,
        pool: str,
        kinds: frozenset[str],
    ) -> bool:
        assert pool == 'final_publish'
        assert kinds == frozenset({'final_publish'})
        observed.add(slot)
        return True  # Busy slots must not trigger settings reads per job.

    async def maintenance(*_args: object) -> None:
        await anyio.sleep_forever()

    monkeypatch.setattr(runtime, '_read_pool_settings', read)
    monkeypatch.setattr(runtime, '_run_processing_once', process)
    monkeypatch.setattr(runtime, '_run_maintenance', maintenance)

    async def exercise() -> None:
        async with anyio.create_task_group() as tasks:
            tasks.start_soon(
                runtime.run_processing_worker,
                Session,
                ProcessingConfig(Path('/incoming'), Path('/staging'), Path('/media')),
                0.03,
                monitor,
            )
            with anyio.fail_after(5):
                while len(reads) < 3 or len(observed) < 3:
                    await anyio.sleep(0.001)
            # The sole refresher failed; all slots still use the last good snapshot.
            assert len(reads) == 3
            snapshots = monitor.snapshots()
            assert len(snapshots) == len(runtime.WORKER_POOLS) * runtime.MAX_POOL_CONCURRENCY
            assert all(item.state == 'disabled' for item in snapshots if item.pool != 'final_publish')
            tasks.cancel_scope.cancel()

    anyio.run(exercise)
    assert reads[0]['final_publish'] == 1
    assert reads[1]['final_publish'] == 3
    assert len(observed) == 3
