from __future__ import annotations

from dataclasses import dataclass

from sqlalchemy.orm import Session

from music_ingest.dto import RuntimeSettings
from music_ingest.enrichment.artwork import (
    ArtworkProvider,
)
from music_ingest.external.acoustid import AcoustIdV2Adapter
from music_ingest.matching.musicbrainz import MusicBrainzProviderAdapter
from music_ingest.matching.providers import (
    AcoustIdProvider,
    MusicBrainzProvider,
)
from music_ingest.models import (
    RuntimeSettingRecord,
)
from music_ingest.processing.config import ProcessingConfig
from music_ingest.settings import SettingKey, get_setting_value, get_setting_values


@dataclass(frozen=True, slots=True)
class RuntimeProcessingSettings:
    session: Session
    config: ProcessingConfig

    def configured_providers(
        self,
    ) -> tuple[MusicBrainzProvider | None, AcoustIdProvider | None, ArtworkProvider | None]:
        if self.config.live_transport is None:
            return self.config.musicbrainz_provider, self.config.acoustid_provider, self.config.artwork_provider
        settings = get_setting_values(
            self.session,
            (
                SettingKey.MUSICBRAINZ_ENABLED,
                SettingKey.MUSICBRAINZ_USER_AGENT,
                SettingKey.MUSICBRAINZ_HOST,
                SettingKey.ACOUSTID_ENABLED,
                SettingKey.ACOUSTID_CLIENT_KEY,
                SettingKey.ARTWORK_ENABLED,
            ),
        )
        defaults = RuntimeSettings()
        musicbrainz = (
            MusicBrainzProviderAdapter(
                self.config.live_transport,
                settings.get(SettingKey.MUSICBRAINZ_USER_AGENT, defaults.musicbrainz_user_agent),
                settings.get(SettingKey.MUSICBRAINZ_HOST, defaults.musicbrainz_host),
            )
            if settings.get(SettingKey.MUSICBRAINZ_ENABLED, str(defaults.musicbrainz_enabled).lower()) == 'true'
            else None
        )
        acoustid = (
            AcoustIdV2Adapter(self.config.live_transport, client_key)
            if settings.get(SettingKey.ACOUSTID_ENABLED, str(defaults.acoustid_enabled).lower()) == 'true'
            and (client_key := settings.get(SettingKey.ACOUSTID_CLIENT_KEY, ''))
            else None
        )
        artwork = (
            musicbrainz
            if settings.get(SettingKey.ARTWORK_ENABLED, str(defaults.artwork_enabled).lower()) == 'true'
            else None
        )
        return musicbrainz, acoustid, artwork

    def confidence_threshold(self) -> float:
        if self.config.live_transport is not None:
            value = get_setting_value(self.session, SettingKey.CONFIDENCE_THRESHOLD)
            if value is None:
                return self.config.confidence_threshold
            try:
                parsed = float(value)
            except ValueError:
                return self.config.confidence_threshold
            return parsed if 0.0 <= parsed <= 1.0 else self.config.confidence_threshold
        setting = self.session.get(RuntimeSettingRecord, 'matching.confidence_threshold')
        if setting is None:
            return self.config.confidence_threshold
        try:
            value = float(setting.value)
        except ValueError:
            return self.config.confidence_threshold
        return value if 0.0 <= value <= 1.0 else self.config.confidence_threshold

    def timeout_seconds(self) -> float:
        if self.config.live_transport is not None:
            value = get_setting_value(self.session, SettingKey.TIMEOUT_SECONDS)
            if value is not None:
                try:
                    return float(value)
                except ValueError:
                    pass
        return self.config.timeout_seconds

    def max_attempts(self) -> int:
        if self.config.live_transport is not None:
            value = get_setting_value(self.session, SettingKey.MAX_ATTEMPTS)
            if value is not None:
                try:
                    return int(value)
                except ValueError:
                    pass
        return self.config.max_attempts

    def artwork_enabled(self) -> bool:
        value = get_setting_value(self.session, SettingKey.ARTWORK_ENABLED)
        return value is None or value.casefold() == 'true'
