from __future__ import annotations

from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path

from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.adapters.inspectors._tool import ToolState
from music_ingest.models import Base, SourceRecord
from music_ingest.models.entities import DecoderEvidenceRecord
from music_ingest.repositories.persistence import DecoderEvidenceRepository


def test_decoder_evidence_when_same_source_and_command_reuses_success(tmp_path: Path) -> None:
    # Given: an immutable source observation with a successful decoder invocation.
    source_path = tmp_path / 'fixture.flac'
    _ = source_path.write_bytes(b'fixture')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "evidence.db"}')
    Base.metadata.create_all(engine)

    with Session(engine) as session:
        source = SourceRecord(
            id='a' * 64,
            source_path=str(source_path),
            device=source_path.stat().st_dev,
            inode=source_path.stat().st_ino,
            size_bytes=source_path.stat().st_size,
            sha256=sha256(source_path.read_bytes()).hexdigest(),
            duration_seconds=None,
            origin='manual',
            intake_state='present',
        )
        session.add(source)
        _ = DecoderEvidenceRepository(session).add_evidence(
            DecoderEvidenceRecord(
                source_id=source.id,
                decoder_command='ffmpeg',
                tool_state=ToolState.SUCCESS.value,
                return_code=0,
                output_sha256=sha256(b'').hexdigest(),
                checked_at=datetime.now(UTC),
            )
        )
        session.commit()

        # When: the same source generation requests the same decoder command.
        cached = DecoderEvidenceRepository(session).successful_evidence(source.id, 'ffmpeg')

        # Then: the durable success evidence is available without another tool invocation.
        assert cached is not None
        assert cached.source_id == source.id
        assert cached.tool_state == ToolState.SUCCESS.value


def test_decoder_evidence_when_command_changes_does_not_reuse_success(tmp_path: Path) -> None:
    # Given: an immutable source observation with evidence for one decoder command.
    source_path = tmp_path / 'fixture.flac'
    _ = source_path.write_bytes(b'fixture')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "evidence.db"}')
    Base.metadata.create_all(engine)

    with Session(engine) as session:
        source = SourceRecord(
            id='a' * 64,
            source_path=str(source_path),
            device=source_path.stat().st_dev,
            inode=source_path.stat().st_ino,
            size_bytes=source_path.stat().st_size,
            sha256=sha256(source_path.read_bytes()).hexdigest(),
            duration_seconds=None,
            origin='manual',
            intake_state='present',
        )
        session.add(source)
        _ = DecoderEvidenceRepository(session).add_evidence(
            DecoderEvidenceRecord(
                source_id=source.id,
                decoder_command='ffmpeg',
                tool_state=ToolState.SUCCESS.value,
                return_code=0,
                output_sha256=sha256(b'').hexdigest(),
                checked_at=datetime.now(UTC),
            )
        )
        session.commit()

        # When: a different decoder command requests reusable evidence.
        cached = DecoderEvidenceRepository(session).successful_evidence(source.id, 'ffmpeg-next')

        # Then: command identity prevents reuse.
        assert cached is None
