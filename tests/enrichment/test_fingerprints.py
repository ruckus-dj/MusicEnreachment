from __future__ import annotations

from hashlib import sha256
from pathlib import Path

from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from music_ingest.adapters.inspectors._tool import ToolEvidence, ToolState
from music_ingest.adapters.inspectors.flac import FlacFinding, FlacFindingKind, FlacInspectionResult, InspectionState
from music_ingest.models import Base, FingerprintRecord, SourceRecord
from music_ingest.services.enrichment.fingerprints import (
    FingerprintRequest,
    FingerprintState,
    calculate_fingerprint,
    fingerprint_source,
)
from music_ingest.services.intake.service import SourceId


def _source(session: Session, source_path: Path) -> SourceRecord:
    source = SourceRecord(
        id='a' * 64,
        source_path=str(source_path),
        device=1,
        inode=2,
        size_bytes=source_path.stat().st_size,
        sha256=sha256(source_path.read_bytes()).hexdigest(),
        duration_seconds=None,
        origin='manual',
        intake_state='needs_review',
    )
    session.add(source)
    session.flush()
    return source


def _valid_flac_inspection() -> FlacInspectionResult:
    return FlacInspectionResult(
        state=InspectionState.VALID,
        findings=(FlacFinding(FlacFindingKind.FLAC_MARKER, 0, 4),),
        flac_test=ToolEvidence(ToolState.SUCCESS, 0, '', ''),
    )


def _quarantined_flac_inspection() -> FlacInspectionResult:
    return FlacInspectionResult(
        state=InspectionState.QUARANTINE,
        findings=(FlacFinding(FlacFindingKind.MALFORMED_CONTAINER, 0, None),),
        flac_test=ToolEvidence(ToolState.FAILED, 1, '', 'malformed'),
    )


def _fake_fpcalc(directory: Path, body: str) -> Path:
    executable = directory / 'fpcalc'
    _ = executable.write_text(f'#!/bin/sh\n{body}\n', encoding='utf-8')
    executable.chmod(0o755)
    return executable


def test_fingerprint_source_when_inspection_is_valid_persists_local_evidence(tmp_path: Path) -> None:
    # Given: a persisted valid FLAC and a deterministic local fpcalc executable.
    source_path = tmp_path / 'valid.flac'
    _ = source_path.write_bytes(b'not decoded by the fake executable')
    fpcalc_body = '; '.join(
        (
            'if [ "$1" = "-version" ]; then printf \'%s\\n\' \'fpcalc version 1.6.0\'',
            'else printf \'%s\\n\' \'{"duration":241,"fingerprint":"12345"}\'; fi',
        )
    )
    executable = _fake_fpcalc(tmp_path, fpcalc_body)
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "fingerprints.db"}')
    Base.metadata.create_all(engine)

    # When: local fingerprinting consumes the successful structural inspection.
    with Session(engine) as session:
        source = _source(session, source_path)
        source_id = source.id
        result = fingerprint_source(
            session,
            FingerprintRequest(
                source_id=SourceId(source.id), source_path=source_path, inspection=_valid_flac_inspection()
            ),
            fpcalc_command=str(executable),
        )
        repeated = fingerprint_source(
            session,
            FingerprintRequest(
                source_id=SourceId(source.id), source_path=source_path, inspection=_valid_flac_inspection()
            ),
            fpcalc_command=str(executable),
        )
        session.commit()
        persisted = session.query(FingerprintRecord).all()

    # Then: durable evidence contains only parsed local result data and source bytes stay unchanged.
    assert result.state is FingerprintState.SUCCESS
    assert result.fingerprint == '12345'
    assert result.duration_seconds == 241
    assert result.tool_version == '1.6.0'
    assert repeated.state == result.state
    assert repeated.fingerprint == result.fingerprint
    assert repeated.duration_seconds == result.duration_seconds
    assert repeated.tool_version == result.tool_version
    assert len(persisted) == 1
    assert all(item.source_id == source_id for item in persisted)
    assert all(item.fingerprint == '12345' for item in persisted)
    assert all(item.duration_seconds == 241 for item in persisted)
    assert all(item.tool_version == '1.6.0' for item in persisted)
    assert result.tool is not None
    assert all(item.output_sha256 == sha256(result.tool.stdout.encode()).hexdigest() for item in persisted)
    assert source_path.read_bytes() == b'not decoded by the fake executable'


def test_calculate_fingerprint_when_inspection_is_valid_runs_without_database(tmp_path: Path) -> None:
    # Given: a valid inspection and a deterministic local fpcalc executable.
    source_path = tmp_path / 'valid.flac'
    _ = source_path.write_bytes(b'not decoded by the fake executable')
    executable = _fake_fpcalc(
        tmp_path,
        '; '.join(
            (
                'if [ "$1" = "-version" ]; then printf \'%s\\n\' \'fpcalc version 1.6.0\'',
                'else printf \'%s\\n\' \'{"duration":241,"fingerprint":"12345"}\'; fi',
            )
        ),
    )

    # When: the database-free fingerprint calculation runs.
    result = calculate_fingerprint(source_path, _valid_flac_inspection(), fpcalc_command=str(executable))

    # Then: the parsed result is returned without requiring a Session or persistence record.
    assert result.state is FingerprintState.SUCCESS
    assert result.fingerprint == '12345'
    assert result.duration_seconds == 241
    assert result.tool_version == '1.6.0'


def test_fingerprint_source_when_inspection_is_quarantined_skips_fpcalc_and_persists_evidence(tmp_path: Path) -> None:
    # Given: a quarantined FLAC and an executable that would leave an invocation marker.
    source_path = tmp_path / 'invalid.flac'
    _ = source_path.write_bytes(b'malformed')
    marker = tmp_path / 'fpcalc-called'
    executable = _fake_fpcalc(tmp_path, f"touch '{marker}'")
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "fingerprints.db"}')
    Base.metadata.create_all(engine)

    # When: fingerprinting receives structural quarantine evidence.
    with Session(engine) as session:
        source = _source(session, source_path)
        result = fingerprint_source(
            session,
            FingerprintRequest(
                source_id=SourceId(source.id), source_path=source_path, inspection=_quarantined_flac_inspection()
            ),
            fpcalc_command=str(executable),
        )
        session.commit()
        persisted = session.query(FingerprintRecord).one()

    # Then: fpcalc never runs, quarantine is retained as local evidence, and no provider seam exists.
    assert result.state is FingerprintState.SKIPPED_QUARANTINED
    assert not marker.exists()
    assert persisted.state == FingerprintState.SKIPPED_QUARANTINED.value
    assert persisted.fingerprint is None
    assert persisted.tool_version is None


def test_fingerprint_source_when_fpcalc_is_missing_persists_unavailable_local_evidence(tmp_path: Path) -> None:
    # Given: a persisted valid FLAC without an available fpcalc executable.
    source_path = tmp_path / 'valid.flac'
    _ = source_path.write_bytes(b'valid')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "fingerprints.db"}')
    Base.metadata.create_all(engine)

    # When: local fingerprinting attempts the unavailable command.
    with Session(engine) as session:
        source = _source(session, source_path)
        result = fingerprint_source(
            session,
            FingerprintRequest(
                source_id=SourceId(source.id), source_path=source_path, inspection=_valid_flac_inspection()
            ),
            fpcalc_command=str(tmp_path / 'missing-fpcalc'),
        )
        session.commit()
        persisted = session.query(FingerprintRecord).one()

    # Then: failure remains typed local evidence and cannot trigger AcoustID construction or calls.
    assert result.state is FingerprintState.UNAVAILABLE
    assert persisted.state == FingerprintState.UNAVAILABLE.value
    assert persisted.fingerprint is None
    assert persisted.tool_version is None
    assert persisted.output_sha256 == sha256(b'').hexdigest()


def test_fingerprint_source_when_fpcalc_fails_persists_local_failure_without_stderr(tmp_path: Path) -> None:
    # Given: a persisted valid FLAC and a local executable with a nonzero result.
    source_path = tmp_path / 'valid.flac'
    _ = source_path.write_bytes(b'valid')
    executable = _fake_fpcalc(tmp_path, "printf '%s' 'local-only-secret' >&2\nexit 7")
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "fingerprints.db"}')
    Base.metadata.create_all(engine)

    # When: fpcalc records its local failure.
    with Session(engine) as session:
        source = _source(session, source_path)
        result = fingerprint_source(
            session,
            FingerprintRequest(
                source_id=SourceId(source.id), source_path=source_path, inspection=_valid_flac_inspection()
            ),
            fpcalc_command=str(executable),
        )
        session.commit()
        persisted = session.query(FingerprintRecord).one()

    # Then: only typed command outcome data is persisted; stderr remains at the tool boundary.
    assert result.state is FingerprintState.FAILED
    assert result.tool is not None
    assert result.tool.stderr == 'local-only-secret'
    assert persisted.state == FingerprintState.FAILED.value
    assert persisted.return_code == 7
    assert persisted.fingerprint is None
    assert persisted.tool_version is None
    assert persisted.output_sha256 == sha256(b'').hexdigest()


def test_fingerprint_source_when_fpcalc_output_is_malformed_persists_local_evidence(tmp_path: Path) -> None:
    # Given: a persisted valid FLAC and local fpcalc output that cannot cross the JSON boundary.
    source_path = tmp_path / 'valid.flac'
    _ = source_path.write_bytes(b'valid')
    executable = _fake_fpcalc(tmp_path, "printf '%s\\n' 'not-json'")
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "fingerprints.db"}')
    Base.metadata.create_all(engine)

    # When: the malformed local output is processed.
    with Session(engine) as session:
        source = _source(session, source_path)
        result = fingerprint_source(
            session,
            FingerprintRequest(
                source_id=SourceId(source.id), source_path=source_path, inspection=_valid_flac_inspection()
            ),
            fpcalc_command=str(executable),
        )
        session.commit()
        persisted = session.query(FingerprintRecord).one()

    # Then: it is stored as malformed local tool evidence without retaining raw output or stderr.
    assert result.state is FingerprintState.MALFORMED
    assert persisted.state == FingerprintState.MALFORMED.value
    assert persisted.fingerprint is None
    assert persisted.tool_version is None
    assert persisted.output_sha256 == sha256(b'not-json\n').hexdigest()
