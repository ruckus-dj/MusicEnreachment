from __future__ import annotations

from datetime import UTC, datetime

from sqlalchemy.orm import Session

from music_ingest.dto import RuntimeSettings
from music_ingest.external.musicbrainz_genres import load_genre_catalog
from music_ingest.models import RuntimeSettingRecord


def load_runtime_settings(session: Session) -> RuntimeSettings:
    values = {
        'confidence_threshold': _setting_value(session, 'matching.confidence_threshold'),
        'timeout_seconds': _setting_value(session, 'processing.timeout_seconds'),
        'retry_delay_seconds': _setting_value(session, 'processing.retry_delay_seconds'),
        'max_attempts': _setting_value(session, 'processing.max_attempts'),
        'musicbrainz_enabled': _setting_value(session, 'providers.musicbrainz.enabled'),
        'musicbrainz_user_agent': _setting_value(session, 'providers.musicbrainz.user_agent'),
        'musicbrainz_host': _setting_value(session, 'providers.musicbrainz.host'),
        'musicbrainz_request_delay_seconds': _setting_value(session, 'providers.musicbrainz.request_delay_seconds'),
        'acoustid_enabled': _setting_value(session, 'providers.acoustid.enabled'),
        'acoustid_client_key': _setting_value(session, 'providers.acoustid.client_key'),
        'artwork_enabled': _setting_value(session, 'artwork.enabled'),
    }
    defaults = RuntimeSettings()
    catalog = load_genre_catalog(session)
    canonical_genres = tuple(item.display_name for item in catalog) or defaults.canonical_genres
    genre_aliases = {item.source_name: (item.display_name,) for item in catalog} or defaults.genre_aliases
    return RuntimeSettings(
        confidence_threshold=_parse_float(values['confidence_threshold'], defaults.confidence_threshold),
        timeout_seconds=_parse_float(values['timeout_seconds'], defaults.timeout_seconds),
        retry_delay_seconds=_parse_float(values['retry_delay_seconds'], defaults.retry_delay_seconds),
        max_attempts=_parse_int(values['max_attempts'], defaults.max_attempts),
        musicbrainz_enabled=_parse_bool(values['musicbrainz_enabled'], defaults.musicbrainz_enabled),
        musicbrainz_user_agent=values['musicbrainz_user_agent'] or defaults.musicbrainz_user_agent,
        musicbrainz_host=values['musicbrainz_host'] or defaults.musicbrainz_host,
        musicbrainz_request_delay_seconds=_parse_float(
            values['musicbrainz_request_delay_seconds'], defaults.musicbrainz_request_delay_seconds
        ),
        acoustid_enabled=_parse_bool(values['acoustid_enabled'], defaults.acoustid_enabled),
        acoustid_client_key=values['acoustid_client_key'] or defaults.acoustid_client_key,
        artwork_enabled=_parse_bool(values['artwork_enabled'], defaults.artwork_enabled),
        canonical_genres=canonical_genres,
        genre_aliases=genre_aliases,
    )


def save_runtime_settings(session: Session, settings: RuntimeSettings) -> None:
    values = {
        'matching.confidence_threshold': str(settings.confidence_threshold),
        'processing.timeout_seconds': str(settings.timeout_seconds),
        'processing.retry_delay_seconds': str(settings.retry_delay_seconds),
        'processing.max_attempts': str(settings.max_attempts),
        'providers.musicbrainz.enabled': str(settings.musicbrainz_enabled).lower(),
        'providers.musicbrainz.user_agent': settings.musicbrainz_user_agent,
        'providers.musicbrainz.host': settings.musicbrainz_host,
        'providers.musicbrainz.request_delay_seconds': str(settings.musicbrainz_request_delay_seconds),
        'providers.acoustid.enabled': str(settings.acoustid_enabled).lower(),
        'providers.acoustid.client_key': settings.acoustid_client_key or '',
        'artwork.enabled': str(settings.artwork_enabled).lower(),
    }
    now = datetime.now(UTC)
    for key, value in values.items():
        record = session.get(RuntimeSettingRecord, key)
        if record is None:
            session.add(RuntimeSettingRecord(key=key, value=value, updated_at=now))
        else:
            record.value = value
            record.updated_at = now


def _setting_value(session: Session, key: str) -> str | None:
    record = session.get(RuntimeSettingRecord, key)
    return None if record is None else record.value


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
