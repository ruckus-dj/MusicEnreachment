from __future__ import annotations

from pathlib import Path

import yaml
from pydantic import BaseModel, Field

ROOT = Path(__file__).parents[1]


class Dependency(BaseModel):
    condition: str


class Service(BaseModel):
    image: str = ''
    environment: dict[str, str] = Field(default_factory=dict)
    volumes: list[str] = Field(default_factory=list)
    depends_on: dict[str, Dependency] = Field(default_factory=dict)
    command: list[str] = Field(default_factory=list)
    healthcheck: dict[str, str | int | list[str]] = Field(default_factory=dict)
    restart: str = ''


class LocalServices(BaseModel):
    postgres: Service
    music_ingest: Service = Field(alias='music-ingest')
    lidarr: Service
    navidrome: Service
    lidarr_webhook: Service = Field(alias='lidarr-webhook')


class LocalCompose(BaseModel):
    services: LocalServices


class ProductionServices(BaseModel):
    music_ingest: Service = Field(alias='music-ingest')


class ProductionStack(BaseModel):
    services: ProductionServices


def test_local_stand_when_rendered_contains_the_complete_runtime_topology() -> None:
    # Given: the checked-in local Docker Compose topology.
    compose = LocalCompose.model_validate(
        yaml.safe_load((ROOT / 'test_stand' / 'compose.yaml').read_text(encoding='utf-8'))
    )

    # When: its service contracts are inspected without starting containers.
    services = compose.services

    # Then: all runtime dependencies, readiness checks, and a shared processing filesystem exist.
    assert services.postgres
    assert services.music_ingest.healthcheck
    assert services.lidarr.healthcheck
    assert services.navidrome.healthcheck
    assert services.music_ingest.volumes == [
        './data/incoming:/data/incoming:ro',
        './data/media:/data/media',
        './appdata/music-ingest:/appdata/music-ingest',
    ]
    assert services.lidarr.volumes == [
        './config/lidarr:/config',
        './data/downloads:/data/downloads',
        './data/incoming:/data/incoming',
    ]
    assert services.navidrome.volumes == ['./config/navidrome:/data', './data/media:/music:ro']
    assert services.lidarr_webhook.depends_on['lidarr'].condition == 'service_healthy'
    assert services.lidarr_webhook.depends_on['music-ingest'].condition == 'service_healthy'
    assert './scripts/configure-lidarr-webhook.sh:/configure-lidarr-webhook.sh:ro' in services.lidarr_webhook.volumes
    assert services.lidarr_webhook.restart == 'no'
    assert not services.lidarr_webhook.healthcheck


def test_production_stack_when_deployed_runs_the_runtime_with_external_storage() -> None:
    # Given: the Komodo-only production stack definition.
    stack = ProductionStack.model_validate(
        yaml.safe_load((ROOT / 'komodo' / 'music-ingest.stack.yaml').read_text(encoding='utf-8'))
    )

    # When: its service runtime contract is inspected.
    service = stack.services.music_ingest

    # Then: it starts the API/worker process against external PostgreSQL and mounted data roots.
    assert service.command == ['python', '-m', 'music_ingest', 'serve']
    assert service.image.startswith('${MUSIC_INGEST_IMAGE:?')
    assert 'replace-at-deploy' not in service.image
    environment = service.environment
    assert environment['MUSIC_INGEST_DATABASE_URL'].startswith('infisical://')
    assert environment['MUSIC_INGEST_INCOMING_ROOT'] == '/data/incoming'
    assert environment['MUSIC_INGEST_STAGING_ROOT'] == '/appdata/music-ingest/staging'
    assert environment['MUSIC_INGEST_MEDIA_ROOT'] == '/data/publish/music'
    assert not {
        'MUSIC_INGEST_RETENTION_ROOT',
        'MUSIC_INGEST_QUARANTINE_ROOT',
        'MUSIC_INGEST_PROVENANCE_ROOT',
    }.intersection(environment)
    assert '/mnt/pool/data/music-incoming:/data/incoming:ro' in service.volumes
    assert '/mnt/pool/data/media:/data/publish/music' in service.volumes
    assert '/mnt/ssd/appdata/music-ingest:/appdata/music-ingest' in service.volumes
    assert service.healthcheck
