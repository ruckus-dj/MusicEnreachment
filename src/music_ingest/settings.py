from __future__ import annotations

from collections.abc import Collection
from datetime import UTC, datetime
from enum import StrEnum

from sqlalchemy import select
from sqlalchemy.orm import Session

from music_ingest.dto import RuntimeSettings
from music_ingest.matching.genre_catalog import load_genre_catalog
from music_ingest.models import RuntimeSettingRecord


class SettingKey(StrEnum):
    CONFIDENCE_THRESHOLD = 'matching.confidence_threshold'
    TIMEOUT_SECONDS = 'processing.timeout_seconds'
    RETRY_DELAY_SECONDS = 'processing.retry_delay_seconds'
    MAX_ATTEMPTS = 'processing.max_attempts'
    WORKER_CONCURRENCY = 'processing.worker_concurrency'
    MUSICBRAINZ_ENABLED = 'providers.musicbrainz.enabled'
    MUSICBRAINZ_USER_AGENT = 'providers.musicbrainz.user_agent'
    MUSICBRAINZ_HOST = 'providers.musicbrainz.host'
    MUSICBRAINZ_REQUEST_DELAY_SECONDS = 'providers.musicbrainz.request_delay_seconds'
    ACOUSTID_ENABLED = 'providers.acoustid.enabled'
    ACOUSTID_REQUEST_DELAY_SECONDS = 'providers.acoustid.request_delay_seconds'
    ACOUSTID_CLIENT_KEY = 'providers.acoustid.client_key'
    ARTWORK_ENABLED = 'artwork.enabled'


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
        worker_concurrency=_parse_int(values.get(SettingKey.WORKER_CONCURRENCY), defaults.worker_concurrency),
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
        canonical_genres=canonical_genres,
        genre_aliases=genre_aliases,
    )


def save_runtime_settings(session: Session, settings: RuntimeSettings) -> None:
    values = {
        SettingKey.CONFIDENCE_THRESHOLD: str(settings.confidence_threshold),
        SettingKey.TIMEOUT_SECONDS: str(settings.timeout_seconds),
        SettingKey.RETRY_DELAY_SECONDS: str(settings.retry_delay_seconds),
        SettingKey.MAX_ATTEMPTS: str(settings.max_attempts),
        SettingKey.WORKER_CONCURRENCY: str(settings.worker_concurrency),
        SettingKey.MUSICBRAINZ_ENABLED: str(settings.musicbrainz_enabled).lower(),
        SettingKey.MUSICBRAINZ_USER_AGENT: settings.musicbrainz_user_agent,
        SettingKey.MUSICBRAINZ_HOST: settings.musicbrainz_host,
        SettingKey.MUSICBRAINZ_REQUEST_DELAY_SECONDS: str(settings.musicbrainz_request_delay_seconds),
        SettingKey.ACOUSTID_ENABLED: str(settings.acoustid_enabled).lower(),
        SettingKey.ACOUSTID_REQUEST_DELAY_SECONDS: str(settings.acoustid_request_delay_seconds),
        SettingKey.ACOUSTID_CLIENT_KEY: settings.acoustid_client_key or '',
        SettingKey.ARTWORK_ENABLED: str(settings.artwork_enabled).lower(),
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
