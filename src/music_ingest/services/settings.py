from __future__ import annotations

from collections.abc import Collection
from datetime import UTC, datetime
from enum import StrEnum

from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.contracts import RuntimeSettings
from music_ingest.contracts.settings import WorkerPoolSettings
from music_ingest.models import RuntimeSettingRecord
from music_ingest.services.matching.genre_catalog import load_genre_catalog


class SettingKey(StrEnum):
    CONFIDENCE_THRESHOLD = 'matching.confidence_threshold'
    TIMEOUT_SECONDS = 'processing.timeout_seconds'
    RETRY_DELAY_SECONDS = 'processing.retry_delay_seconds'
    MAX_ATTEMPTS = 'processing.max_attempts'
    WORKER_POOLS = 'processing.worker_pools'
    MUSICBRAINZ_ENABLED = 'providers.musicbrainz.enabled'
    MUSICBRAINZ_USER_AGENT = 'providers.musicbrainz.user_agent'
    MUSICBRAINZ_HOST = 'providers.musicbrainz.host'
    MUSICBRAINZ_REQUEST_DELAY_SECONDS = 'providers.musicbrainz.request_delay_seconds'
    ACOUSTID_ENABLED = 'providers.acoustid.enabled'
    ACOUSTID_REQUEST_DELAY_SECONDS = 'providers.acoustid.request_delay_seconds'
    ACOUSTID_CLIENT_KEY = 'providers.acoustid.client_key'
    ARTWORK_ENABLED = 'artwork.enabled'
    LRCLIB_ENABLED = 'providers.lrclib.enabled'
    LRCLIB_HOST = 'providers.lrclib.host'
    LRCLIB_USER_AGENT = 'providers.lrclib.user_agent'
    LRCLIB_TIMEOUT_SECONDS = 'providers.lrclib.timeout_seconds'
    LRCLIB_MAX_ATTEMPTS = 'providers.lrclib.max_attempts'
    LRCLIB_REQUEST_DELAY_SECONDS = 'providers.lrclib.request_delay_seconds'
    LRCLIB_MAX_RESPONSE_BYTES = 'providers.lrclib.max_response_bytes'
    LRCLIB_MATCH_CONFIDENCE_THRESHOLD = 'providers.lrclib.match_confidence_threshold'


def get_setting_value(session: Session, key: SettingKey) -> str | None:
    """Return one persisted scalar setting without loading unrelated configuration."""
    record = session.get(RuntimeSettingRecord, key.value)
    return None if record is None else record.value


def get_setting_values(session: Session, keys: Collection[SettingKey]) -> dict[SettingKey, str]:
    """Return requested persisted scalar settings with one database query."""
    requested = tuple(dict.fromkeys(keys))
    if not requested:
        return {}
    records = session.scalars(
        select(RuntimeSettingRecord).where(RuntimeSettingRecord.key.in_([key.value for key in requested]))
    )
    return {SettingKey(record.key): record.value for record in records}


def build_runtime_settings(session: Session, *, include_genres: bool = False) -> RuntimeSettings:
    """Build the aggregate settings object, optionally including the separate genre catalog."""
    values = get_setting_values(session, tuple(SettingKey))
    defaults = RuntimeSettings()
    catalog = load_genre_catalog(session) if include_genres else ()
    canonical_genres = tuple(item.display_name for item in catalog) or defaults.canonical_genres
    genre_aliases = {item.source_name: (item.display_name,) for item in catalog} or defaults.genre_aliases
    return RuntimeSettings(
        confidence_threshold=_parse_float(values.get(SettingKey.CONFIDENCE_THRESHOLD), defaults.confidence_threshold),
        timeout_seconds=_parse_float(values.get(SettingKey.TIMEOUT_SECONDS), defaults.timeout_seconds),
        retry_delay_seconds=_parse_float(values.get(SettingKey.RETRY_DELAY_SECONDS), defaults.retry_delay_seconds),
        max_attempts=_parse_int(values.get(SettingKey.MAX_ATTEMPTS), defaults.max_attempts),
        worker_pools=WorkerPoolSettings.model_validate_json(values.get(SettingKey.WORKER_POOLS) or '{}'),
        musicbrainz_enabled=_parse_bool(values.get(SettingKey.MUSICBRAINZ_ENABLED), defaults.musicbrainz_enabled),
        musicbrainz_user_agent=values.get(SettingKey.MUSICBRAINZ_USER_AGENT) or defaults.musicbrainz_user_agent,
        musicbrainz_host=values.get(SettingKey.MUSICBRAINZ_HOST) or defaults.musicbrainz_host,
        musicbrainz_request_delay_seconds=_parse_float(
            values.get(SettingKey.MUSICBRAINZ_REQUEST_DELAY_SECONDS), defaults.musicbrainz_request_delay_seconds
        ),
        acoustid_enabled=_parse_bool(values.get(SettingKey.ACOUSTID_ENABLED), defaults.acoustid_enabled),
        acoustid_request_delay_seconds=_parse_float(
            values.get(SettingKey.ACOUSTID_REQUEST_DELAY_SECONDS), defaults.acoustid_request_delay_seconds
        ),
        acoustid_client_key=values.get(SettingKey.ACOUSTID_CLIENT_KEY) or defaults.acoustid_client_key,
        artwork_enabled=_parse_bool(values.get(SettingKey.ARTWORK_ENABLED), defaults.artwork_enabled),
        lrclib_enabled=_parse_bool(values.get(SettingKey.LRCLIB_ENABLED), defaults.lrclib_enabled),
        lrclib_host=values.get(SettingKey.LRCLIB_HOST) or defaults.lrclib_host,
        lrclib_user_agent=values.get(SettingKey.LRCLIB_USER_AGENT) or defaults.lrclib_user_agent,
        lrclib_timeout_seconds=_parse_float(
            values.get(SettingKey.LRCLIB_TIMEOUT_SECONDS), defaults.lrclib_timeout_seconds
        ),
        lrclib_max_attempts=_parse_int(values.get(SettingKey.LRCLIB_MAX_ATTEMPTS), defaults.lrclib_max_attempts),
        lrclib_request_delay_seconds=_parse_float(
            values.get(SettingKey.LRCLIB_REQUEST_DELAY_SECONDS), defaults.lrclib_request_delay_seconds
        ),
        lrclib_max_response_bytes=_parse_int(
            values.get(SettingKey.LRCLIB_MAX_RESPONSE_BYTES), defaults.lrclib_max_response_bytes
        ),
        lrclib_match_confidence_threshold=_parse_float(
            values.get(SettingKey.LRCLIB_MATCH_CONFIDENCE_THRESHOLD), defaults.lrclib_match_confidence_threshold
        ),
        canonical_genres=canonical_genres,
        genre_aliases=genre_aliases,
    )


def save_runtime_settings(session: Session, settings: RuntimeSettings) -> None:
    values = {
        SettingKey.CONFIDENCE_THRESHOLD: str(settings.confidence_threshold),
        SettingKey.TIMEOUT_SECONDS: str(settings.timeout_seconds),
        SettingKey.RETRY_DELAY_SECONDS: str(settings.retry_delay_seconds),
        SettingKey.MAX_ATTEMPTS: str(settings.max_attempts),
        SettingKey.WORKER_POOLS: settings.worker_pools.model_dump_json(),
        SettingKey.MUSICBRAINZ_ENABLED: str(settings.musicbrainz_enabled).lower(),
        SettingKey.MUSICBRAINZ_USER_AGENT: settings.musicbrainz_user_agent,
        SettingKey.MUSICBRAINZ_HOST: settings.musicbrainz_host,
        SettingKey.MUSICBRAINZ_REQUEST_DELAY_SECONDS: str(settings.musicbrainz_request_delay_seconds),
        SettingKey.ACOUSTID_ENABLED: str(settings.acoustid_enabled).lower(),
        SettingKey.ACOUSTID_REQUEST_DELAY_SECONDS: str(settings.acoustid_request_delay_seconds),
        SettingKey.ACOUSTID_CLIENT_KEY: settings.acoustid_client_key or '',
        SettingKey.ARTWORK_ENABLED: str(settings.artwork_enabled).lower(),
        SettingKey.LRCLIB_ENABLED: str(settings.lrclib_enabled).lower(),
        SettingKey.LRCLIB_HOST: settings.lrclib_host,
        SettingKey.LRCLIB_USER_AGENT: settings.lrclib_user_agent,
        SettingKey.LRCLIB_TIMEOUT_SECONDS: str(settings.lrclib_timeout_seconds),
        SettingKey.LRCLIB_MAX_ATTEMPTS: str(settings.lrclib_max_attempts),
        SettingKey.LRCLIB_REQUEST_DELAY_SECONDS: str(settings.lrclib_request_delay_seconds),
        SettingKey.LRCLIB_MAX_RESPONSE_BYTES: str(settings.lrclib_max_response_bytes),
        SettingKey.LRCLIB_MATCH_CONFIDENCE_THRESHOLD: str(settings.lrclib_match_confidence_threshold),
    }
    now = datetime.now(UTC)
    for key, value in values.items():
        record = session.get(RuntimeSettingRecord, key.value)
        if record is None:
            session.add(RuntimeSettingRecord(key=key.value, value=value, updated_at=now))
        else:
            record.value = value
            record.updated_at = now


def _parse_bool(value: str | None, default: bool) -> bool:
    if value is None:
        return default
    return value.casefold() == 'true'


def _parse_float(value: str | None, default: float) -> float:
    if value is None:
        return default
    try:
        return float(value)
    except ValueError:
        return default


def _parse_int(value: str | None, default: int) -> int:
    if value is None:
        return default
    try:
        return int(value)
    except ValueError:
        return default
