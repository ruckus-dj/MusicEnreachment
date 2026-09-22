from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from datetime import timedelta
from functools import cached_property
from types import MappingProxyType

from sqlalchemy.orm import Session

from music_ingest.adapters.external.acoustid import AcoustIdV2Adapter
from music_ingest.contracts import RuntimeSettings
from music_ingest.services.enrichment.artwork import (
    ArtworkProvider,
)
from music_ingest.services.matching.musicbrainz import MusicBrainzProviderAdapter
from music_ingest.services.matching.providers import (
    AcoustIdProvider,
    MusicBrainzProvider,
)
from music_ingest.services.settings import SettingKey, get_setting_values
from music_ingest.workers.config import ProcessingConfig


@dataclass(frozen=True)
class RuntimeProcessingSettings:
    session: Session
    config: ProcessingConfig

    @cached_property
    def values(self) -> Mapping[SettingKey, str]:
        return MappingProxyType(get_setting_values(self.session, tuple(SettingKey)))

    def configured_providers(
        self,
    ) -> tuple[MusicBrainzProvider | None, AcoustIdProvider | None, ArtworkProvider | None]:
        return self._providers

    @cached_property
    def _providers(
        self,
    ) -> tuple[MusicBrainzProvider | None, AcoustIdProvider | None, ArtworkProvider | None]:
        if self.config.live_transport is None:
            return self.config.musicbrainz_provider, self.config.acoustid_provider, self.config.artwork_provider
        settings = self.values
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
        value = self.values.get(SettingKey.CONFIDENCE_THRESHOLD)
        if value is None:
            return self.config.confidence_threshold
        try:
            parsed = float(value)
        except ValueError:
            return self.config.confidence_threshold
        return parsed if 0.0 <= parsed <= 1.0 else self.config.confidence_threshold

    def timeout_seconds(self) -> float:
        if self.config.live_transport is not None:
            value = self.values.get(SettingKey.TIMEOUT_SECONDS)
            if value is not None:
                try:
                    return float(value)
                except ValueError:
                    pass
        return self.config.timeout_seconds

    def max_attempts(self) -> int:
        if self.config.live_transport is not None:
            value = self.values.get(SettingKey.MAX_ATTEMPTS)
            if value is not None:
                try:
                    return int(value)
                except ValueError:
                    pass
        return self.config.max_attempts

    def artwork_enabled(self) -> bool:
        value = self.values.get(SettingKey.ARTWORK_ENABLED)
        return value is None or value.casefold() == 'true'

    def retry_delay(self) -> timedelta:
        if self.config.live_transport is None:
            return self.config.retry_delay
        return timedelta(
            seconds=float(self.values.get(SettingKey.RETRY_DELAY_SECONDS) or self.config.retry_delay.total_seconds())
        )
