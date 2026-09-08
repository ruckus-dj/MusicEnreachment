from __future__ import annotations

import json
from dataclasses import replace
from datetime import UTC, datetime, timedelta, tzinfo
from hashlib import sha256
from pathlib import Path
from shutil import which
from subprocess import run
from types import SimpleNamespace
from typing import Final, cast

import pytest
from sqlalchemy import create_engine, event, select
from sqlalchemy.orm import Session

import music_ingest.processing.media_stage as media_stage
import music_ingest.processing.worker as processing
import music_ingest.publication.service as publication_service
from music_ingest.dto import CandidateEvidencePayload
from music_ingest.enrichment.fingerprints import FingerprintResult, FingerprintState
from music_ingest.inspectors._tool import ToolEvidence, ToolState
from music_ingest.inspectors.media_capabilities import MediaCapability, MediaCapabilityInspection
from music_ingest.matching.evidence import ProviderEvidenceResult
from music_ingest.matching.providers import (
    FixtureProvenance,
    MusicBrainzLookupRequest,
    MusicBrainzMatch,
    ReleaseCandidate,
)
from music_ingest.matching.scoring import CandidateScore, MatchDecision, MatchResult
from music_ingest.models import (
    Base,
    CandidateRecord,
    EffectiveSourceDecisionRecord,
    JobAttemptRecord,
    JobRecord,
    LibraryEventRecord,
    LibraryRecord,
    ProviderAttemptRecord,
    ProviderCandidateRunRecord,
    ProviderScheduleRecord,
    ReviewDecisionRecord,
    SourceRecord,
    SourceRootRecord,
    UnsortedFilenameCounterRecord,
)
from music_ingest.models.jobs import ClaimedJob
from music_ingest.normalize.metadata import MetadataWriteRequest, MetadataWriteResult
from music_ingest.normalize.tags import read_normalized_tags, write_normalized_tags
from music_ingest.processing import ProcessingConfig, ProcessingWorker
from music_ingest.processing.remux import RemuxRequest
from music_ingest.processing.support import evidence as source_evidence
from music_ingest.processing.worker import _latest_candidate_run, _release_candidates_for_recording
from music_ingest.publication.service import PublicationError, PublicationResult
from tests.support.providers import AcoustIdFixtureProvider, MusicBrainzFixtureProvider

_FFMPEG: Final[str] = which('ffmpeg') or ''
assert _FFMPEG


def _flac(path: Path) -> Path:
    completed = run(  # noqa: S603
        [
            _FFMPEG,
            '-hide_banner',
            '-loglevel',
            'error',
            '-f',
            'lavfi',
            '-i',
            'sine=frequency=440:duration=10',
            '-c:a',
            'flac',
            str(path),
        ],
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert completed.returncode == 0, completed.stderr
    _ = write_normalized_tags(
        path,
        (
            ('TITLE', 'Fixture Track'),
            ('ARTIST', 'Fixture Artist; Fixture Guest'),
            ('ALBUM', 'Fixture Album'),
            ('ALBUMARTIST', 'Fixture Artist; Fixture Guest'),
            ('DATE', '2026'),
            ('TRACKNUMBER', '1'),
            ('TRACKTOTAL', '1'),
            ('DISCNUMBER', '1'),
            ('DISCTOTAL', '1'),
            ('GENRE', 'Hip Hop; Alternative Rock'),
        ),
    )
    return path


def test_folder_selection_uses_each_source_file_parent_directory() -> None:
    # Given: one source file below a tracks subdirectory and one beside that directory.
    track_path = '/library/2015 - _кустик_/tracks/01.flac'
    album_path = '/library/2015 - _кустик_/album.flac'

    # When: the worker determines the folder-selection root for each source.
    track_folder = processing._folder_selection_root(track_path)
    album_folder = processing._folder_selection_root(album_path)

    # Then: only files with the same immediate parent directory share a group.
    assert track_folder == Path('/library/2015 - _кустик_/tracks')
    assert album_folder == Path('/library/2015 - _кустик_')
    assert track_folder != album_folder


def test_folder_selection_is_queued_when_only_candidate_selection_jobs_remain(tmp_path: Path) -> None:
    # Given: two sources have completed provider analysis and are awaiting their coordination jobs.
    config = _config(tmp_path)
    album_directory = tmp_path / 'album'
    album_directory.mkdir()
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "folder-selection.db"}')
    Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    with Session(engine) as session:
        first = _source(session, _flac(album_directory / '01.flac'))
        second = _source(session, _flac(album_directory / '02.flac'))
        first.library_record = LibraryRecord(id='record-first', created_at=now, updated_at=now)
        second.library_record = LibraryRecord(id='record-second', created_at=now, updated_at=now)
        current_job = JobRecord(
            id='candidate-selection-first',
            source_id=first.id,
            kind='candidate_selection',
            state='queued',
            created_at=now,
        )
        session.add_all(
            (
                current_job,
                JobRecord(
                    id='candidate-selection-second',
                    source_id=second.id,
                    kind='candidate_selection',
                    state='queued',
                    created_at=now,
                ),
            )
        )
        session.commit()

        # When: the worker processes one coordination job after provider analysis is complete.
        assert ProcessingWorker(session, config).run_once()

        # Then: a single folder-wide release-selection job is ready to resolve the album.
        queued = session.scalar(
            select(JobRecord)
            .where(JobRecord.kind == 'folder_release_selection')
            .where(JobRecord.folder_path == str(album_directory))
        )
        assert queued is not None
        assert queued.state == 'queued'


def test_musicbrainz_candidate_persistence_separates_release_and_recording_evidence() -> None:
    candidate = ReleaseCandidate(
        'release-id',
        'Fixture Album',
        'Fixture Artist',
        215,
        ('recording-id',),
        recording_title='Target Song',
        track_number=2,
        release_artist_name='Fixture Artist',
        recording_artist_names=('Fixture Artist',),
    )
    request = processing.MatchingRequest(
        'Fixture Artist',
        'Fixture Album',
        215,
        recording_title='Target Song',
        track_number=2,
    )

    records = processing._candidate_records(
        'source-id',
        candidate,
        CandidateScore('release-id', 0.61, artist_component=0.4, release_component=0.6),
        request,
    )

    assert [record.candidate_key for record in records] == ['release-id:recording-id']
    match = CandidateEvidencePayload.model_validate_json(records[0].evidence)
    assert match.entity == 'recording_release'
    assert match.release_mbid == 'release-id'
    assert match.recording_mbid == 'recording-id'
    assert match.compatible_ids == ()
    assert match.score == 1.0


def test_musicbrainz_candidate_persistence_scores_release_when_match_result_lacks_entry() -> None:
    # Given: a MusicBrainz candidate has a request but no precomputed result-map entry.
    candidate = ReleaseCandidate('release-id', 'Fixture Album', 'Fixture Artist', 215, ('recording-id',))
    request = processing.MatchingRequest('Fixture Artist', 'Fixture Album', 215)

    # When: candidate evidence is persisted without an incoming release score.
    records = processing._candidate_records('source-id', candidate, None, request)

    # Then: the release receives the same component score as every other candidate.
    match = CandidateEvidencePayload.model_validate_json(records[0].evidence)
    assert match.score is not None
    assert match.score > 0.0


def test_musicbrainz_releases_keep_every_acoustid_recording_association() -> None:
    # Given: MusicBrainz returned several releases for one AcousticID recording, including
    # a release whose title differs from the source filename.
    candidates = (
        ReleaseCandidate(
            'release-mismatch',
            'Promo Compilation',
            'Fixture Artist',
            recording_mbids=('acoustid-recording',),
            recording_title='Different Provider Title',
        ),
        ReleaseCandidate(
            'release-match',
            'Fixture Album',
            'Fixture Artist',
            recording_mbids=('acoustid-recording',),
            recording_title='Fixture Track',
        ),
        ReleaseCandidate(
            'release-unrelated',
            'Other Album',
            'Other Artist',
            recording_mbids=('other-recording',),
        ),
    )

    # When: the worker associates MusicBrainz release results with the AcousticID recording.
    associated = _release_candidates_for_recording('acoustid-recording', candidates)

    # Then: every release attached to that recording remains available for scoring and review.
    assert tuple(candidate.release_mbid for candidate in associated) == ('release-mismatch', 'release-match')


def test_latest_candidate_run_uses_maximum_id_not_relationship_order() -> None:
    # Given: candidate runs are attached in an order unrelated to their database IDs.
    source = SourceRecord(
        id='source-id',
        source_path='/source/track.flac',
        device=1,
        inode=1,
        size_bytes=1,
        sha256='a' * 64,
        duration_seconds=1,
        origin='manual',
        intake_state='present',
        candidate_runs=[
            ProviderCandidateRunRecord(id=2178, provider_name='musicbrainz', created_at=datetime.now(UTC)),
            ProviderCandidateRunRecord(id=1776, provider_name='musicbrainz', created_at=datetime.now(UTC)),
        ],
    )

    # When: the worker resolves the latest MusicBrainz candidate run.
    latest = _latest_candidate_run(source, 'musicbrainz')

    # Then: the highest persisted run ID is selected regardless of relationship order.
    assert latest is not None and latest.id == 2178


def test_musicbrainz_candidate_tags_preserve_release_disambiguation_in_album() -> None:
    # Given: MusicBrainz identifies an edition through its release disambiguation.
    candidate = ReleaseCandidate(
        '3b98979b-6494-4a7c-8de6-2165902f8a87',
        'юность в стиле панк',
        'Кис-Кис',
        disambiguation='baby punk version',
    )

    # When: canonical tags are built from the release candidate.
    tags = processing._candidate_tags(candidate)

    # Then: the edition remains visible in the persisted album tag.
    assert tags['ALBUM'] == 'юность в стиле панк (baby punk version)'


def test_single_scored_musicbrainz_candidate_ignores_related_recording_evidence() -> None:
    source = SimpleNamespace(
        candidate_runs=(),
        candidates=(
            CandidateRecord(
                candidate_key='release-id',
                evidence=json.dumps(
                    {
                        'provider': 'musicbrainz',
                        'entity': 'recording_release',
                        'score': 0.97,
                        'release_mbid': 'release-id',
                        'recording_mbid': 'recording-id',
                        'tags': {},
                    },
                ),
            ),
            CandidateRecord(
                candidate_key='recording-id',
                evidence=json.dumps(
                    {'provider': 'musicbrainz', 'entity': 'recording', 'score': 0.91, 'tags': {}},
                ),
            ),
        ),
    )

    selected = processing._single_scored_candidate(cast(SourceRecord, cast(object, source)), 'musicbrainz', 0.70)

    assert selected is not None
    assert selected[0] == 'release-id'


def test_single_scored_candidate_selects_strict_best_release_above_threshold() -> None:
    # Given: two qualifying releases, with one clearly scoring higher than the other.
    source = SimpleNamespace(
        candidate_runs=(),
        candidates=(
            CandidateRecord(
                candidate_key='baby-punk-release',
                evidence=json.dumps(
                    {
                        'provider': 'musicbrainz',
                        'entity': 'recording_release',
                        'score': 1.0,
                        'release_mbid': 'baby-punk-release',
                        'recording_mbid': 'recording-id',
                        'tags': {},
                    }
                ),
            ),
            CandidateRecord(
                candidate_key='standard-release',
                evidence=json.dumps(
                    {
                        'provider': 'musicbrainz',
                        'entity': 'recording_release',
                        'score': 0.87,
                        'release_mbid': 'standard-release',
                        'recording_mbid': 'recording-id',
                        'tags': {},
                    }
                ),
            ),
        ),
    )

    # When: the best qualifying release is selected.
    selected = processing._single_scored_candidate(cast(SourceRecord, cast(object, source)), 'musicbrainz', 0.70)

    # Then: the strict score leader is selected instead of leaving both candidates unresolved.
    assert selected is not None
    assert selected[0] == 'baby-punk-release'


def test_musicbrainz_release_without_recording_link_is_not_persisted() -> None:
    candidate = ReleaseCandidate(
        'unrelated-release-id',
        'Unrelated Album',
        'Fixture Artist',
        215,
        (),
    )

    records = processing._candidate_records('source-id', candidate, None, None)

    assert records == ()


def test_candidate_records_persist_musicbrainz_score_in_unit_interval() -> None:
    # Given: a MusicBrainz candidate keeps its provider score internally on the 0..100 scale.
    candidate = ReleaseCandidate(
        'release-id',
        'Fixture Album',
        'Fixture Artist',
        215,
        ('recording-id',),
        musicbrainz_score=84,
    )

    # When: worker evidence is serialized for persistence and API consumers.
    records = processing._candidate_records('source-id', candidate, None, None)

    # Then: both release and recording evidence expose the normalized 0..1 provider score.
    assert len(records) == 1
    assert json.loads(records[0].evidence)['musicbrainz_score'] == 0.84


def _tagless_flac(path: Path) -> Path:
    source = _flac(path)
    _ = write_normalized_tags(source, ())
    return source


def _source(session: Session, path: Path) -> SourceRecord:
    stat = path.stat()
    root = session.get(SourceRootRecord, 'legacy')
    if root is None:
        now = datetime.now(UTC)
        session.add(
            SourceRootRecord(
                id='legacy',
                display_name='legacy',
                canonical_path=str(path.parent.resolve()),
                enabled=True,
                scan_state='scanned',
                created_at=now,
                updated_at=now,
            )
        )
    source = SourceRecord(
        id=sha256(f'{stat.st_dev}:{stat.st_ino}'.encode()).hexdigest(),
        source_path=str(path),
        device=stat.st_dev,
        inode=stat.st_ino,
        size_bytes=stat.st_size,
        sha256=sha256(path.read_bytes()).hexdigest(),
        duration_seconds=1,
        origin='lidarr',
        intake_state='discovered',
    )
    session.add(source)
    session.flush()
    return source


def _config(tmp_path: Path) -> ProcessingConfig:
    return ProcessingConfig(
        incoming_root=tmp_path / 'incoming',
        staging_root=tmp_path / 'staging',
        media_root=tmp_path / 'media',
    )


def test_locked_source_refreshes_an_already_loaded_source_record(tmp_path: Path) -> None:
    # Given: this worker session loaded a source before another transaction updated it.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "locked-source.db"}')
    Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    with Session(engine) as seed_session:
        seed_session.add(
            SourceRootRecord(
                id='root',
                display_name='Incoming',
                canonical_path=str(tmp_path),
                enabled=True,
                scan_state='scanned',
                created_at=now,
                updated_at=now,
            )
        )
        seed_session.add(
            SourceRecord(
                id='source',
                source_path='/incoming/stale.flac',
                device=1,
                inode=2,
                size_bytes=3,
                sha256='a' * 64,
                origin='manual',
                intake_state='present',
                source_root_id='root',
            )
        )
        seed_session.commit()

    with Session(engine) as worker_session:
        stale_source = worker_session.get(SourceRecord, 'source')
        assert stale_source is not None and stale_source.source_path == '/incoming/stale.flac'
        with Session(engine) as concurrent_session:
            concurrent_source = concurrent_session.get(SourceRecord, 'source')
            assert concurrent_source is not None
            concurrent_source.source_path = '/incoming/fresh.flac'
            concurrent_session.commit()

        claimed = ClaimedJob(
            JobRecord(
                id='analysis-job',
                source_id='source',
                kind='musicbrainz_analysis',
                state='running',
                created_at=now,
            ),
            JobAttemptRecord(job_id='analysis-job', attempt_number=1, state='running'),
        )

        # When: analysis loads its source under the row lock.
        locked_source = ProcessingWorker(worker_session, _config(tmp_path))._sources.locked_source(claimed)

        # Then: it receives the post-lock database state, not the identity-map snapshot from before it.
        assert locked_source is stale_source
        assert locked_source.source_path == '/incoming/fresh.flac'


def test_initial_job_without_identity_defers_before_media_staging(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: an initial source with ordinary metadata but no explicit MusicBrainz identities.
    config = _config(tmp_path)
    source_path = _flac(tmp_path / 'incoming.flac')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "initial-defer.db"}')
    Base.metadata.create_all(engine)
    now = datetime(2026, 8, 17, tzinfo=UTC)
    monkeypatch.setattr(
        processing, 'inspect_source_capability', lambda path, timeout_seconds: MediaCapability('flac', 'flac')
    )

    def fail_if_media_is_staged(request: object) -> None:
        _ = request
        pytest.fail('initial source without identity must not stage media')

    monkeypatch.setattr(processing, 'process_media', fail_if_media_is_staged)
    with Session(engine) as session:
        source = _source(session, source_path)
        session.add(
            JobRecord(id='initial-defer', source_id=source.id, kind='filesystem_scan', state='queued', created_at=now)
        )
        session.commit()

        # When: the initial source job is processed.
        assert ProcessingWorker(session, config).run_once()
        session.commit()

        # Then: provenance is retained, but no derived audio or staging directory is created.
        assert not config.staging_root.exists()
        assert not config.media_root.exists()
        refreshed = session.get(SourceRecord, source.id)
        assert refreshed is not None
        assert refreshed.intake_state == 'present'
        event = session.scalar(
            select(LibraryEventRecord).where(LibraryEventRecord.kind == 'publication_deferred_for_identity')
        )
        assert event is not None


def test_initial_job_accepts_wav_through_unified_audio_intake(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    # Given: a real PCM WAV source with no metadata and a fingerprint result for the next stage.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    source_path = config.incoming_root / 'incoming.wav'
    completed = run(  # noqa: S603
        [
            _FFMPEG,
            '-hide_banner',
            '-loglevel',
            'error',
            '-f',
            'lavfi',
            '-i',
            'sine=frequency=440:duration=0.1',
            '-c:a',
            'pcm_s16le',
            str(source_path),
        ],
        capture_output=True,
        check=False,
        text=True,
        timeout=10,
    )
    assert completed.returncode == 0, completed.stderr
    fingerprint = FingerprintResult(FingerprintState.SUCCESS, 'wav-fingerprint', 0.1, 'test', 'a' * 64, None, None)
    monkeypatch.setattr(source_evidence, 'fingerprint_source', lambda *_args, **_kwargs: fingerprint)
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "wav-intake.db"}')
    Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    with Session(engine) as session:
        source = _source(session, source_path)
        session.add(
            JobRecord(id='wav-intake', source_id=source.id, kind='filesystem_scan', state='queued', created_at=now)
        )
        session.commit()

        # When: the worker runs the initial source intake.
        assert ProcessingWorker(session, config).run_once()
        session.commit()

        # Then: WAV is accepted by the same ffprobe/ffmpeg/Mutagen path as other audio sources.
        job = session.get(JobRecord, 'wav-intake')
        persisted = session.get(SourceRecord, source.id)
        assert job is not None and job.state == 'completed'
        assert persisted is not None and persisted.intake_state == 'present'
        assert persisted.media_codec == 'PCM_S16LE'


def test_automatic_match_persists_acoustid_recording_and_release_identity() -> None:
    # Given: source album matching selected a MusicBrainz release and verified its AcousticID recording.
    record = LibraryRecord(id='record-id', created_at=datetime.now(UTC), updated_at=datetime.now(UTC))
    match_result = MatchResult(
        MatchDecision.AUTO_SELECTED,
        'd5c9ba44-448a-4b07-9f06-e6626032c19d',
        CandidateScore('47d13484-9eed-4460-babd-bca3a19fcd77', 0.9945609),
        CandidateScore('d5c9ba44-448a-4b07-9f06-e6626032c19d', 0.8),
        None,
    )

    # When: the worker accepts the automatic provider match.
    record.match_state = 'matched'
    record.musicbrainz_release_id = match_result.selected_release_mbid
    record.musicbrainz_recording_id = match_result.recording_score.candidate_mbid

    # Then: the UI can render the selected AcousticID recording instead of asking for a choice.
    assert record.match_state == 'matched'
    assert record.musicbrainz_recording_id == '47d13484-9eed-4460-babd-bca3a19fcd77'
    assert record.musicbrainz_release_id == 'd5c9ba44-448a-4b07-9f06-e6626032c19d'


def test_musicbrainz_lookup_when_acoustid_supplies_recording_mbid_works_without_source_tags(tmp_path: Path) -> None:
    # Given: a fingerprint match with no source tags and an available MusicBrainz provider.
    config = replace(
        _config(tmp_path),
        musicbrainz_provider=MusicBrainzFixtureProvider(Path(__file__).parent / 'fixtures' / 'musicbrainz'),
    )
    fingerprint = FingerprintResult(
        FingerprintState.SUCCESS,
        'fixture-fingerprint',
        10,
        'fixture',
        'a' * 64,
        None,
        None,
    )
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)

    # When: the MusicBrainz stage uses the AcousticID recording MBID.
    with Session(engine) as session:
        result = ProcessingWorker(session, config)._lookup_providers(
            (),
            fingerprint,
            datetime.now(UTC),
            recording_mbid='f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a',
            run_acoustid=False,
        )

    # Then: missing tags cannot disable the authoritative MBID lookup.
    assert result is not None
    assert isinstance(result.musicbrainz, MusicBrainzMatch)


def test_musicbrainz_lookup_uses_artist_and_title_without_album(tmp_path: Path) -> None:
    # Given: source metadata has an artist and title but no album.
    requests: list[MusicBrainzLookupRequest] = []

    class Provider:
        def lookup(self, request: MusicBrainzLookupRequest, now: datetime | None = None) -> MusicBrainzMatch:
            _ = now
            requests.append(request)
            return MusicBrainzMatch(
                FixtureProvenance(Path(__file__).parent / 'fixtures' / 'musicbrainz' / 'success.json', 'a' * 64),
                ReleaseCandidate('release-id', 'Страна дождей', 'Noize MC'),
            )

    config = replace(_config(tmp_path), musicbrainz_provider=Provider())
    fingerprint = FingerprintResult(
        FingerprintState.SUCCESS, 'fixture-fingerprint', 138, 'fixture', 'a' * 64, None, None
    )
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)

    # When: the worker builds a provider lookup from the available source tags.
    with Session(engine) as session:
        result = ProcessingWorker(session, config)._lookup_providers(
            (('ARTIST', 'Noize MC'), ('TITLE', 'Страна дождей')),
            fingerprint,
            datetime.now(UTC),
            run_acoustid=False,
        )

    # Then: the text query keeps both available fields instead of becoming empty.
    assert result is not None
    assert len(requests) == 1
    assert requests[0].query == 'artist:"Noize MC" recording:"Страна дождей"'


def test_unique_acoustid_album_match_selects_the_only_matching_recording() -> None:
    # Given: two AcousticID recordings, only one of which MusicBrainz resolves to the source album.
    provenance = FixtureProvenance(Path('fixture.json'), 'a' * 64)
    wrong_result = ProviderEvidenceResult(
        MusicBrainzMatch(
            provenance,
            ReleaseCandidate('release-vol-2', 'The Greatest Hits Vol.2', 'Noize MC', recording_mbids=('vol-2',)),
        ),
        None,
    )
    correct_result = ProviderEvidenceResult(
        MusicBrainzMatch(
            provenance,
            ReleaseCandidate('release-vol-1', 'The Greatest Hits Vol.1', 'Noize MC', recording_mbids=('vol-1',)),
        ),
        None,
    )
    wrong_match = MatchResult(
        MatchDecision.NEEDS_REVIEW,
        None,
        CandidateScore('vol-2', 0.99),
        CandidateScore('release-vol-2', 0.4),
        None,
    )
    correct_match = MatchResult(
        MatchDecision.AUTO_SELECTED,
        'release-vol-1',
        CandidateScore('vol-1', 0.99),
        CandidateScore('release-vol-1', 0.8),
        None,
    )

    # When: the worker considers all resolved AcousticID recordings.
    selected = processing._unique_acoustid_album_match(
        ((wrong_result, wrong_match), (correct_result, correct_match)),
    )

    # Then: it selects the only recording verified for the source album.
    assert selected == (correct_result, correct_match)


def test_unique_acoustid_album_match_selects_highest_scored_release() -> None:
    # Given: two verified editions for the same track, with disambiguation making one release stronger.
    provenance = FixtureProvenance(Path('fixture.json'), 'a' * 64)
    standard_result = ProviderEvidenceResult(
        MusicBrainzMatch(
            provenance,
            ReleaseCandidate(
                'release-standard', 'юность в стиле панк', 'Кис-Кис', recording_mbids=('recording-standard',)
            ),
        ),
        None,
    )
    baby_punk_result = ProviderEvidenceResult(
        MusicBrainzMatch(
            provenance,
            ReleaseCandidate(
                'release-baby-punk',
                'юность в стиле панк',
                'Кис-Кис',
                recording_mbids=('recording-baby-punk',),
                disambiguation='baby punk version',
            ),
        ),
        None,
    )
    standard_match = MatchResult(
        MatchDecision.AUTO_SELECTED,
        'release-standard',
        CandidateScore('recording-standard', 0.9999341),
        CandidateScore('release-standard', 0.8723404255319149),
        None,
    )
    baby_punk_match = MatchResult(
        MatchDecision.AUTO_SELECTED,
        'release-baby-punk',
        CandidateScore('recording-baby-punk', 0.9999341),
        CandidateScore('release-baby-punk', 1.0),
        None,
    )

    # When: the worker evaluates both verified release editions.
    selected = processing._unique_acoustid_album_match(
        ((standard_result, standard_match), (baby_punk_result, baby_punk_match)),
    )

    # Then: the strongest release and its linked recording are selected together.
    assert selected == (baby_punk_result, baby_punk_match)


def test_unique_acoustid_recording_match_selects_verified_recording_without_release_match() -> None:
    # Given: AcousticID identifies one recording with sufficient confidence, while its release needs review.
    provenance = FixtureProvenance(Path('fixture.json'), 'a' * 64)
    provider_result = ProviderEvidenceResult(
        MusicBrainzMatch(
            provenance,
            ReleaseCandidate('release-vol-1', 'The Greatest Hits Vol.1', 'Noize MC', recording_mbids=('vol-1',)),
        ),
        None,
    )
    recording_score = CandidateScore('vol-1', 0.99)

    # When: the worker evaluates the verified AcousticID recording independently from the release decision.
    selected = processing._unique_acoustid_recording_match(((provider_result, recording_score),), 0.8)

    # Then: it preserves the recording selection despite the unresolved release.
    assert selected == (provider_result, recording_score)


def test_single_scored_candidate_selects_the_only_current_candidate_despite_other_provider_failure() -> None:
    # Given: persisted successful AcousticID evidence and an unrelated failed MusicBrainz attempt.
    source = SourceRecord(
        id='source-id',
        source_path='/source.flac',
        device=1,
        inode=1,
        size_bytes=1,
        sha256='a' * 64,
        duration_seconds=1,
        origin='manual',
        intake_state='present',
        candidates=[
            CandidateRecord(
                candidate_key='recording-id',
                evidence=json.dumps(
                    {
                        'provider': 'acoustid',
                        'score': 0.95,
                        'tags': {'MUSICBRAINZ_TRACKID': 'recording-id'},
                    }
                ),
            )
        ],
    )

    # When: stored evidence is evaluated after a provider retry failed.
    selected = processing._single_scored_candidate(source, 'acoustid', 0.7)

    # Then: the qualifying sole candidate remains selectable.
    assert selected is not None
    assert selected[0] == 'recording-id'


def test_single_scored_recording_candidate_uses_musicbrainz_when_acoustid_is_missing() -> None:
    # Given: one qualifying MusicBrainz recording candidate and no AcousticID result.
    source = SourceRecord(
        id='source-id',
        source_path='/source.flac',
        device=1,
        inode=1,
        size_bytes=1,
        sha256='a' * 64,
        duration_seconds=1,
        origin='manual',
        intake_state='present',
        candidates=[
            CandidateRecord(
                candidate_key='release-id',
                evidence=json.dumps(
                    {
                        'provider': 'musicbrainz',
                        'entity': 'recording_release',
                        'score': 1.0,
                        'recording_mbid': 'recording-id',
                        'tags': {
                            'MUSICBRAINZ_ALBUMID': 'release-id',
                            'MUSICBRAINZ_RECORDINGID': 'recording-id',
                        },
                    }
                ),
            ),
            CandidateRecord(
                candidate_key='recording-id',
                evidence=json.dumps(
                    {
                        'provider': 'musicbrainz',
                        'entity': 'recording',
                        'score': 1.0,
                        'recording_mbid': 'recording-id',
                        'tags': {
                            'MUSICBRAINZ_ALBUMID': 'release-id',
                            'MUSICBRAINZ_RECORDINGID': 'recording-id',
                        },
                    }
                ),
            ),
        ],
    )

    # When: the provider-independent recording candidate is selected.
    selected = processing._single_scored_recording_candidate(source, 0.7)

    # Then: the MusicBrainz recording identity is eligible for the common association path.
    assert selected is not None
    assert selected[0] == 'recording-id'


def test_single_scored_recording_candidate_prefers_release_compatible_recording() -> None:
    # Given: two recording candidates and a selected release linked to the first one.
    source = SourceRecord(
        id='source-id',
        source_path='/source.flac',
        device=1,
        inode=1,
        size_bytes=1,
        sha256='a' * 64,
        duration_seconds=1,
        origin='manual',
        intake_state='present',
        candidates=[
            CandidateRecord(
                candidate_key='release-id',
                evidence=json.dumps(
                    {
                        'provider': 'musicbrainz',
                        'entity': 'recording_release',
                        'score': 1.0,
                        'recording_mbid': 'preferred-recording',
                        'tags': {'MUSICBRAINZ_RECORDINGID': 'preferred-recording'},
                    }
                ),
            ),
            CandidateRecord(
                candidate_key='preferred-recording',
                evidence=json.dumps(
                    {
                        'provider': 'musicbrainz',
                        'entity': 'recording',
                        'score': 0.997,
                        'recording_mbid': 'preferred-recording',
                        'tags': {'MUSICBRAINZ_RECORDINGID': 'preferred-recording'},
                    }
                ),
            ),
            CandidateRecord(
                candidate_key='other-recording',
                evidence=json.dumps(
                    {
                        'provider': 'musicbrainz',
                        'entity': 'recording',
                        'score': 1.0,
                        'recording_mbid': 'other-recording',
                        'tags': {'MUSICBRAINZ_RECORDINGID': 'other-recording'},
                    }
                ),
            ),
        ],
    )

    # When: the release-compatible recording is selected.
    selected = processing._single_scored_recording_candidate(source, 0.7, 'preferred-recording')

    # Then: release compatibility wins over an unrelated marginally higher recording score.
    assert selected is not None
    assert selected[0] == 'preferred-recording'


def test_single_scored_recording_candidate_does_not_let_acoustid_mask_composite_evidence() -> None:
    # Given: AcousticID has a higher raw score, while MusicBrainz has the composite evidence for the same MBID.
    source = SourceRecord(
        id='source-id',
        source_path='/source.flac',
        device=1,
        inode=1,
        size_bytes=1,
        sha256='a' * 64,
        duration_seconds=1,
        origin='manual',
        intake_state='present',
        candidates=[
            CandidateRecord(
                candidate_key='preferred-recording',
                evidence=json.dumps(
                    {
                        'provider': 'acoustid',
                        'entity': 'recording',
                        'recording_mbid': 'preferred-recording',
                        'score': 0.95,
                    }
                ),
            ),
            CandidateRecord(
                candidate_key='preferred-recording',
                evidence=json.dumps(
                    {
                        'provider': 'musicbrainz',
                        'entity': 'recording',
                        'recording_mbid': 'preferred-recording',
                        'score': 0.7,
                        'score_components': {'artist': 0.4},
                    }
                ),
            ),
        ],
    )

    # When: the folder-selection path asks for that release-compatible recording.
    selected = processing._single_scored_recording_candidate(source, 0.7, 'preferred-recording')

    # Then: the composite MusicBrainz evidence remains eligible despite the higher raw AcousticID score.
    assert selected is not None
    assert selected[0] == 'preferred-recording'
    assert selected[1].provider == 'musicbrainz'


def test_worker_reassociates_musicbrainz_only_source_into_existing_recording(tmp_path: Path) -> None:
    # Given: a MusicBrainz-only source and an existing record with its recording MBID.
    now = datetime(2026, 8, 19, tzinfo=UTC)
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "musicbrainz-only-merge.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        target = LibraryRecord(
            id='target-record',
            musicbrainz_recording_id='recording-id',
            musicbrainz_release_id='release-id',
            source_state='present',
            processing_state='complete',
            match_state='matched',
            publication_state='current',
            metadata_state='final',
            created_at=now,
            updated_at=now,
        )
        duplicate = LibraryRecord(
            id='duplicate-record',
            source_state='present',
            processing_state='analyzing',
            match_state='needs_review',
            publication_state='absent',
            metadata_state='original',
            created_at=now,
            updated_at=now,
        )
        source = SourceRecord(
            id='musicbrainz-only-source',
            source_path='/source/track.mp3',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=1,
            origin='manual',
            intake_state='present',
            source_root_id='legacy',
            library_record=duplicate,
            provider_attempts=[
                ProviderAttemptRecord(
                    provider_name='musicbrainz',
                    outcome='musicbrainzmatch',
                    snapshot_sha256='b' * 64,
                    snapshot='{}',
                    created_at=now,
                )
            ],
            candidates=[
                CandidateRecord(
                    candidate_key='release-id:recording-id',
                    evidence=json.dumps(
                        {
                            'provider': 'musicbrainz',
                            'entity': 'recording_release',
                            'release_mbid': 'release-id',
                            'score': 1.0,
                            'recording_mbid': 'recording-id',
                            'tags': {
                                'MUSICBRAINZ_ALBUMID': 'release-id',
                                'MUSICBRAINZ_RECORDINGID': 'recording-id',
                            },
                        }
                    ),
                ),
                CandidateRecord(
                    candidate_key='recording-id',
                    evidence=json.dumps(
                        {
                            'provider': 'musicbrainz',
                            'entity': 'recording',
                            'score': 1.0,
                            'recording_mbid': 'recording-id',
                            'tags': {
                                'MUSICBRAINZ_ALBUMID': 'release-id',
                                'MUSICBRAINZ_RECORDINGID': 'recording-id',
                            },
                        }
                    ),
                ),
            ],
        )
        session.add(
            SourceRootRecord(
                id='legacy',
                display_name='legacy',
                canonical_path='/source',
                created_at=now,
                updated_at=now,
            )
        )
        session.add_all(
            (
                target,
                duplicate,
                source,
                JobRecord(
                    id='musicbrainz-only-selection',
                    source_id=source.id,
                    kind='candidate_selection',
                    state='queued',
                    created_at=now,
                ),
            )
        )
        session.commit()

        # When: candidate selection processes the source without an AcousticID match.
        assert ProcessingWorker(session, _config(tmp_path)).run_once()
        session.commit()
        assert ProcessingWorker(session, _config(tmp_path)).run_once()
        session.commit()

        # Then: the folder selection reassociates the source into the existing recording aggregate.
        refreshed = session.get(SourceRecord, source.id)
        assert refreshed is not None
        assert refreshed.library_record_id == target.id
        assert any(event.kind == 'source_recording_reassigned' for event in target.events)


def test_candidate_selection_queries_folder_membership_in_sql(tmp_path: Path) -> None:
    # Given: an incomplete candidate selection with unrelated source evidence persisted.
    now = datetime(2026, 8, 19, tzinfo=UTC)
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "candidate-selection-loader.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        root = SourceRootRecord(
            id='legacy',
            display_name='legacy',
            canonical_path='/source',
            created_at=now,
            updated_at=now,
        )
        record = LibraryRecord(
            id='candidate-record',
            source_state='present',
            processing_state='analyzing',
            match_state='unknown',
            publication_state='absent',
            metadata_state='original',
            created_at=now,
            updated_at=now,
        )
        source = SourceRecord(
            id='candidate-source',
            source_path='/source/album/track.flac',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=1,
            origin='manual',
            intake_state='present',
            source_root=root,
            library_record=record,
            provider_attempts=[
                ProviderAttemptRecord(
                    provider_name='musicbrainz',
                    outcome='nomatch',
                    snapshot_sha256='b' * 64,
                    snapshot='{}',
                    created_at=now,
                )
            ],
        )
        session.add_all(
            (
                source,
                JobRecord(
                    id='candidate-selection-loader',
                    source_id=source.id,
                    kind='candidate_selection',
                    state='queued',
                    created_at=now,
                ),
            )
        )
        session.commit()
    statements: list[str] = []

    def listener(_connection, _cursor, statement, _parameters, _context, _executemany) -> None:
        statements.append(statement.lower())

    with Session(engine) as worker_session:
        # When: the worker marks the incomplete evidence set as reviewable.
        assert ProcessingWorker(worker_session, _config(tmp_path)).run_once(
            on_claimed=lambda _job_id, _kind: event.listen(engine, 'before_cursor_execute', listener)
        )
        worker_session.commit()
    event.remove(engine, 'before_cursor_execute', listener)

    # Then: candidate selection constrains members to the source folder in SQL.
    assert any('source_records.source_path like' in statement for statement in statements)


def test_candidate_selection_queues_folder_selection_for_single_file_folder(tmp_path: Path) -> None:
    # Given: one source file with a completed MusicBrainz candidate run.
    now = datetime(2026, 8, 19, tzinfo=UTC)
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "single-file-folder-selection.db"}')
    Base.metadata.create_all(engine)
    source_id = 'single-folder-source'
    with Session(engine) as session:
        root = SourceRootRecord(
            id='single-folder-root',
            display_name='single-folder-root',
            canonical_path='/source',
            created_at=now,
            updated_at=now,
        )
        record = LibraryRecord(
            id='single-folder-record',
            source_state='present',
            processing_state='analyzing',
            match_state='unknown',
            publication_state='absent',
            metadata_state='original',
            created_at=now,
            updated_at=now,
        )
        run_record = ProviderCandidateRunRecord(
            source_id=source_id,
            provider_name='musicbrainz',
            created_at=now,
            candidates=[
                CandidateRecord(
                    source_id=source_id,
                    candidate_key='release-one:recording-one',
                    evidence=json.dumps(
                        {
                            'provider': 'musicbrainz',
                            'entity': 'recording_release',
                            'release_mbid': 'release-one',
                            'score': 1.0,
                            'compatible_ids': ['recording-one'],
                            'tags': {'MUSICBRAINZ_RECORDINGID': 'recording-one'},
                        }
                    ),
                ),
                CandidateRecord(
                    source_id=source_id,
                    candidate_key='release-two:recording-one',
                    evidence=json.dumps(
                        {
                            'provider': 'musicbrainz',
                            'entity': 'recording_release',
                            'release_mbid': 'release-two',
                            'score': 1.0,
                            'compatible_ids': ['recording-one'],
                            'tags': {'MUSICBRAINZ_RECORDINGID': 'recording-one'},
                        }
                    ),
                ),
            ],
        )
        source = SourceRecord(
            id=source_id,
            source_path='/source/single-folder/track.flac',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=1,
            origin='manual',
            intake_state='present',
            source_root=root,
            library_record=record,
            candidate_runs=[run_record],
        )
        session.add_all(
            (
                source,
                JobRecord(
                    id='single-folder-candidate-selection',
                    source_id=source_id,
                    kind='candidate_selection',
                    state='queued',
                    created_at=now,
                ),
            )
        )
        session.commit()

    with Session(engine) as session:
        # When: candidate selection completes for the single-file folder.
        assert ProcessingWorker(session, _config(tmp_path)).run_once()
        session.commit()
        folder_job = session.scalar(select(JobRecord).where(JobRecord.kind == 'folder_release_selection'))

    # Then: the same folder-selection job is queued as for a multi-file folder.
    assert folder_job is not None
    assert folder_job.folder_path == '/source/single-folder'


def test_stored_match_tags_preserves_verified_musicbrainz_metadata() -> None:
    # Given: one confirmed AcousticID recording and one matching MusicBrainz release candidate.
    recording = (
        'recording-id',
        CandidateEvidencePayload(provider='acoustid', score=0.95, tags={'MUSICBRAINZ_RECORDINGID': 'recording-id'}),
    )
    release = (
        'release-id',
        CandidateEvidencePayload(
            provider='musicbrainz',
            score=0.8,
            tags={
                'ALBUM': 'Fixture Release',
                'MUSICBRAINZ_ALBUMID': 'release-id',
                'MUSICBRAINZ_RECORDINGID': 'recording-id',
                'TITLE': 'Fixture Track',
            },
        ),
    )

    # When: recovery builds metadata from the persisted, cross-verified candidates.
    tags = processing._stored_match_tags(recording, release)

    # Then: provider facts remain available for analyzed and final revisions.
    assert tags == {
        'ALBUM': 'Fixture Release',
        'MUSICBRAINZ_ALBUMID': 'release-id',
        'MUSICBRAINZ_RECORDINGID': 'recording-id',
        'TITLE': 'Fixture Track',
    }


def test_stored_match_tags_preserves_musicbrainz_metadata_without_acoustid() -> None:
    # Given: MusicBrainz selected a release, while AcousticID returned no recording.
    release = (
        'release-id',
        CandidateEvidencePayload(
            provider='musicbrainz',
            score=0.8,
            tags={
                'ALBUM': 'Fixture Release',
                'MUSICBRAINZ_ALBUMID': 'release-id',
                'MUSICBRAINZ_RECORDINGID': 'recording-id',
                'TITLE': 'Fixture Track',
            },
        ),
    )

    # When: recovery reconstructs metadata from the available provider evidence.
    tags = processing._stored_match_tags(None, release)

    # Then: release metadata is retained even though recording identity remains reviewable.
    assert tags == {
        'ALBUM': 'Fixture Release',
        'MUSICBRAINZ_ALBUMID': 'release-id',
        'MUSICBRAINZ_RECORDINGID': 'recording-id',
        'TITLE': 'Fixture Track',
    }


def test_stored_match_tags_uses_release_mbid_for_composite_candidate_key() -> None:
    # Given: a unified candidate whose persisted key contains both MusicBrainz IDs.
    release = (
        'release-id:low-recording-id',
        CandidateEvidencePayload(
            provider='musicbrainz',
            entity='recording_release',
            release_mbid='release-id',
            recording_mbid='low-recording-id',
            score=0.8,
            tags={'TITLE': 'Fixture Track'},
        ),
    )

    # When: folder publication reconstructs tags from the selected unified candidate.
    tags = processing._stored_match_tags(None, release)

    # Then: the album tag contains only the release MBID, never the composite key.
    assert tags['MUSICBRAINZ_ALBUMID'] == 'release-id'


def test_stored_release_candidate_chooses_highest_scoring_pair() -> None:
    source = SimpleNamespace(
        candidate_runs=(),
        candidates=(
            CandidateRecord(
                candidate_key='release-id:high-recording-id',
                evidence=json.dumps(
                    {
                        'provider': 'musicbrainz',
                        'entity': 'recording_release',
                        'release_mbid': 'release-id',
                        'recording_mbid': 'high-recording-id',
                        'score': 0.95,
                        'tags': {},
                    }
                ),
            ),
            CandidateRecord(
                candidate_key='release-id:low-recording-id',
                evidence=json.dumps(
                    {
                        'provider': 'musicbrainz',
                        'entity': 'recording_release',
                        'release_mbid': 'release-id',
                        'recording_mbid': 'low-recording-id',
                        'score': 0.55,
                        'tags': {},
                    }
                ),
            ),
        ),
    )

    selected = processing._stored_release_candidate(cast(SourceRecord, cast(object, source)), 'release-id')

    assert selected is not None
    assert selected[0] == 'release-id:high-recording-id'


def test_stored_release_recording_mbid_comes_from_musicbrainz_tags() -> None:
    # Given: a MusicBrainz release candidate with a recording identity and no AcoustID result.
    release = (
        'release-id',
        CandidateEvidencePayload(
            provider='musicbrainz',
            score=0.8,
            tags={'MUSICBRAINZ_RECORDINGID': 'recording-id'},
        ),
    )

    # When: recovery extracts the recording identity from the release evidence.
    recording_mbid = processing._stored_release_recording_mbid(release)

    # Then: MusicBrainz supplies the LibraryRecord recording identity.
    assert recording_mbid == 'recording-id'


def test_independent_match_identity_preserves_a_selected_release_when_recording_is_unresolved() -> None:
    # Given: a record with no recording identity and one qualifying MusicBrainz release.
    record = LibraryRecord(id='record-id', created_at=datetime.now(UTC), updated_at=datetime.now(UTC))

    # When: release evidence is selected independently.
    record.musicbrainz_release_id = 'release-id'
    record.match_state = 'needs_review'

    # Then: the release remains selected while the unresolved recording stays in review.
    assert record.musicbrainz_release_id == 'release-id'
    assert record.musicbrainz_recording_id is None
    assert record.match_state == 'needs_review'


def test_match_identity_when_recording_is_already_owned_by_another_release_allows_parallel_identity(
    tmp_path: Path,
) -> None:
    # Given: another LibraryRecord already owns the recording under a different release.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "identity-conflict.db"}')
    Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    with Session(engine) as session:
        owner = LibraryRecord(
            id='record-owner',
            created_at=now,
            updated_at=now,
            musicbrainz_recording_id='recording-id',
            musicbrainz_release_id='release-owner',
        )
        target = LibraryRecord(id='record-target', created_at=now, updated_at=now)
        session.add_all((owner, target))
        session.commit()

        # When: the worker applies the same recording under another release.
        target.musicbrainz_recording_id = 'recording-id'
        target.musicbrainz_release_id = 'release-id'
        target.match_state = 'matched'

        # Then: both exact recording-release identities remain available.
        assert target.musicbrainz_recording_id == 'recording-id'
        assert target.musicbrainz_release_id == 'release-id'
        assert target.match_state == 'matched'
        session.flush()


def test_independent_match_identity_when_recording_is_already_owned_by_another_release_allows_parallel_identity(
    tmp_path: Path,
) -> None:
    # Given: another LibraryRecord already owns the stored recording under a different release.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "independent-identity-conflict.db"}')
    Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    with Session(engine) as session:
        owner = LibraryRecord(
            id='record-owner',
            created_at=now,
            updated_at=now,
            musicbrainz_recording_id='recording-id',
            musicbrainz_release_id='release-owner',
        )
        target = LibraryRecord(id='record-target', created_at=now, updated_at=now)
        session.add_all((owner, target))
        session.commit()

        # When: stored provider evidence applies the identity under the selected release.
        target.musicbrainz_recording_id = 'recording-id'
        target.musicbrainz_release_id = 'release-id'
        target.match_state = 'matched'

        # Then: the exact pair is matched without changing the other release identity.
        assert target.musicbrainz_recording_id == 'recording-id'
        assert target.musicbrainz_release_id == 'release-id'
        assert target.match_state == 'matched'
        session.flush()


def test_worker_run_once_records_actual_completion_time(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    # Given: a queued job and a clock that advances during processing.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "job-timing.db"}')
    Base.metadata.create_all(engine)
    started_at = datetime(2026, 8, 10, 0, 0, 0, tzinfo=UTC)
    finished_at = started_at + timedelta(seconds=3)

    class Clock:
        values = iter((started_at, finished_at))

        @classmethod
        def now(cls, tz: tzinfo | None) -> datetime:
            _ = tz
            return next(cls.values)

    def process_initial(worker: ProcessingWorker, claimed: ClaimedJob, now: datetime) -> None:
        _ = worker, claimed, now

    monkeypatch.setattr(processing, 'datetime', Clock)
    monkeypatch.setattr(ProcessingWorker, '_process_initial', process_initial)
    with Session(engine) as session:
        source = SourceRecord(
            id='source-timing',
            source_path=str(tmp_path / 'timing.flac'),
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=1,
            origin='manual',
            intake_state='present',
        )
        session.add(
            JobRecord(
                id='timing-job', source_id=source.id, kind='filesystem_scan', state='queued', created_at=started_at
            )
        )
        session.add(source)
        session.commit()
        worker = ProcessingWorker(session, _config(tmp_path))

        # When: one worker iteration completes the job.
        assert worker.run_once()

        # Then: the attempt duration reflects processing rather than claim time.
        job = session.get(JobRecord, 'timing-job')
        assert job is not None
        assert job.state == 'completed'
        assert job.attempts[0].started_at == started_at
        assert job.attempts[0].finished_at == finished_at


def test_worker_run_once_rolls_back_failed_flush_before_retry(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    # Given: a claimed job whose processing raises after mutating the transaction.
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker-rollback.db"}')
    Base.metadata.create_all(engine)
    now = datetime.now(UTC)

    def fail_processing(worker: ProcessingWorker, claimed: ClaimedJob, processing_now: datetime) -> None:
        _ = worker, claimed, processing_now
        raise RuntimeError('simulated flush failure')

    monkeypatch.setattr(ProcessingWorker, '_process', fail_processing)
    with Session(engine) as session:
        session.add(JobRecord(id='rollback-job', kind='reconciliation_scan', state='queued', created_at=now))
        session.commit()

        # When: the worker handles the failed processing attempt.
        assert ProcessingWorker(session, _config(tmp_path)).run_once()

        # Then: retry bookkeeping succeeds on a usable transaction instead of cascading the flush error.
        job = session.get(JobRecord, 'rollback-job')
        assert job is not None
        assert job.state == 'queued'
        assert job.attempts[0].state == 'retry_wait'


def test_musicbrainz_candidate_tags_format_genres_for_metadata_display() -> None:
    # Given: MusicBrainz returns a track artist, a distinct release artist, and lowercase genre source names.
    candidate = ReleaseCandidate(
        'release-id',
        'Fixture Album',
        'Fixture Artist',
        genres=('alternative rock', 'hip hop'),
        isrcs=('USFIX2600001',),
        performers=('Fixture Performer', 'Fixture Vocalist'),
        release_artist_name='Fixture Album Artist',
    )

    # When: provider candidate metadata is converted to tags.
    tags = processing._candidate_tags(candidate)

    # Then: the published GENRE value uses readable labels.
    assert tags['GENRE'] == 'Alternative Rock; Hip Hop'
    assert tags['ARTIST'] == 'Fixture Artist'
    assert tags['ALBUMARTIST'] == 'Fixture Album Artist'
    assert tags['ISRC'] == 'USFIX2600001'
    assert tags['PERFORMER'] == 'Fixture Performer; Fixture Vocalist'


def test_musicbrainz_candidate_tags_write_featured_artists_as_multiple_values() -> None:
    # Given: MusicBrainz provides an artist credit joined by "feat.".
    candidate = ReleaseCandidate(
        'release-id',
        'We Made It',
        'Busta Rhymes feat. Linkin Park',
        release_artist_name='Busta Rhymes feat. Linkin Park',
        recording_artist_names=('Busta Rhymes', 'Linkin Park'),
        release_artist_names=('Busta Rhymes', 'Linkin Park'),
    )

    # When: candidate facts are converted to publication tags.
    tags = processing._candidate_tags(candidate)

    # Then: Navidrome receives two artists rather than a synthetic feat. artist.
    assert tags['ARTIST'] == 'Busta Rhymes; Linkin Park'
    assert tags['ALBUMARTIST'] == 'Busta Rhymes; Linkin Park'


def test_musicbrainz_reprocess_uses_latest_acoustid_or_reviewer_selected_identity() -> None:
    # Given: stale automatic identities from a prior run and newer AcousticID evidence.
    source = SourceRecord(
        id='source-id',
        source_path='source.flac',
        device=1,
        inode=1,
        size_bytes=1,
        sha256='0' * 64,
        duration_seconds=1,
        origin='lidarr',
        intake_state='present',
    )
    source.candidates.extend(
        (
            CandidateRecord(
                source_id=source.id,
                candidate_key='stale-acoustid-recording',
                evidence=json.dumps(
                    {'provider': 'acoustid', 'tags': {'MUSICBRAINZ_TRACKID': 'stale-acoustid-recording'}}
                ),
            ),
            CandidateRecord(
                source_id=source.id,
                candidate_key='fresh-acoustid-recording',
                evidence=json.dumps(
                    {'provider': 'acoustid', 'tags': {'MUSICBRAINZ_TRACKID': 'fresh-acoustid-recording'}}
                ),
            ),
        )
    )
    record = LibraryRecord(
        id='record-id',
        musicbrainz_recording_id='stale-automatic-recording',
        musicbrainz_release_id='stale-automatic-release',
        created_at=datetime.now(UTC),
        updated_at=datetime.now(UTC),
    )

    # When: a full reprocess prepares its MusicBrainz lookup.
    lookup_ids = processing.musicbrainz_lookup_ids(record, source)

    # Then: fresh AcousticID evidence replaces stale automatic identity and permits album disambiguation.
    assert lookup_ids == ('fresh-acoustid-recording', None)

    # And: a reviewer-confirmed AcousticID selection remains authoritative.
    source.review_decisions.append(
        ReviewDecisionRecord(source_id=source.id, state='acoustid_confirmed', rationale='selected by reviewer')
    )
    record.musicbrainz_recording_id = 'reviewer-recording'
    assert processing.musicbrainz_lookup_ids(record, source) == ('reviewer-recording', None)


def test_worker_when_valid_source_has_no_provider_match_stays_unpublished_and_reviewable(tmp_path: Path) -> None:
    # Given: a DB-backed queued job with a valid immutable incoming FLAC and artwork.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    _ = (config.incoming_root / 'cover.jpg').write_bytes(b'\xff\xd8\xfffixture\xff\xd9')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        session.add(
            JobRecord(
                id='job-1', source_id=source.id, kind='filesystem_scan', state='queued', created_at=datetime.now(UTC)
            )
        )
        session.commit()

    # When: one worker claims and processes the job.
    with Session(engine) as session:
        worker = ProcessingWorker(session, config)
        assert worker.run_once()
        job = session.get(JobRecord, 'job-1')
        assert job is not None
        assert job.state == 'completed'
        session.commit()

    # Then: the source remains reviewable without creating a managed publication.
    with Session(engine) as session:
        job = session.get(JobRecord, 'job-1')
        assert job is not None and job.state == 'completed'
        assert [attempt.state for attempt in job.attempts] == ['succeeded']
        source = session.get(SourceRecord, job.source_id)
        assert source is not None
        assert source.intake_state == 'present'
        assert source.media_codec == 'FLAC'
        assert source.media_bit_depth == 16
        assert source.media_sample_rate == 44_100
        assert source.media_channels == 1
        decision = session.get(EffectiveSourceDecisionRecord, source.library_record_id)
        assert decision is not None and decision.source_id is None
        assert source.review_decisions == []
        assert source.library_record is not None
        assert {revision.layer for revision in source.library_record.metadata_revisions} == {'original'}
        assert source.library_record.processing_state == 'needs_review'
        assert source.library_publications == []
    assert not config.media_root.exists()


@pytest.mark.parametrize(
    ('suffix', 'codec_name'),
    [('.m4a', 'aac'), ('.m4a', 'alac'), ('.mp3', 'mp3'), ('.opus', 'opus'), ('.ogg', 'vorbis')],
)
def test_worker_publishes_every_supported_source_as_mka_without_changing_audio_bytes(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, suffix: str, codec_name: str
) -> None:
    # Given: a declared non-FLAC source and probes that avoid external capability/decoder tools.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    source_path = config.incoming_root / f'fixture{suffix}'
    source_path.write_bytes(b'original non-flac bytes')
    original_bytes = source_path.read_bytes()
    capability = MediaCapabilityInspection(
        MediaCapability(
            'mov,mp4,m4a,3gp,3g2,mj2' if suffix == '.m4a' else 'mp3' if suffix == '.mp3' else 'ogg',
            codec_name,
        ),
        ToolEvidence(ToolState.SUCCESS, 0, '', ''),
    )
    monkeypatch.setattr(processing, 'inspect_media_capability', lambda *_args, **_kwargs: capability)
    monkeypatch.setattr(processing, 'validate_decoder', lambda *_args, **_kwargs: None)
    monkeypatch.setattr(publication_service, 'inspect_media_capability', lambda *_args, **_kwargs: capability)
    monkeypatch.setattr(publication_service, '_validate_tags', lambda *_args, **_kwargs: None)
    monkeypatch.setattr(
        media_stage,
        'read_tags',
        lambda *_args, **_kwargs: [
            ('TITLE', 'Fixture Track'),
            ('ARTIST', 'Fixture Artist; Fixture Guest'),
            ('ALBUM', 'Fixture Album'),
            ('ALBUMARTIST', 'Fixture Artist; Fixture Guest'),
            ('DATE', '2026'),
            ('TRACKNUMBER', '1'),
            ('TRACKTOTAL', '1'),
            ('DISCNUMBER', '1'),
            ('DISCTOTAL', '1'),
            ('GENRE', 'Hip Hop; Alternative Rock'),
            ('MUSICBRAINZ_RECORDINGID', 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'),
            ('MUSICBRAINZ_ALBUMID', '4d4a5ff4-4a38-4cf1-8e2f-0f64a65f4f5c'),
        ],
    )
    monkeypatch.setattr(
        publication_service,
        'read_normalized_tags',
        lambda *_args, **_kwargs: (('TITLE', 'Fixture Track'),),
    )
    monkeypatch.setattr(
        media_stage,
        'decoder_evidence',
        lambda *_args, **_kwargs: ToolEvidence(ToolState.SUCCESS, 0, '', ''),
    )

    def copy_remux(request: RemuxRequest) -> ToolEvidence:
        source = request.source_path
        output = request.output_path
        output.write_bytes(source.read_bytes())
        return ToolEvidence(ToolState.SUCCESS, 0, '', '')

    monkeypatch.setattr(media_stage, 'remux_stream_copy', copy_remux)

    def preserve_audio(request: MetadataWriteRequest) -> MetadataWriteResult:
        request.output_path.parent.mkdir(parents=True, exist_ok=True)
        request.output_path.write_bytes(request.source_path.read_bytes())
        return MetadataWriteResult(
            request.output_path,
            (
                ('TITLE', 'Fixture Track'),
                ('MUSICBRAINZ_RECORDINGID', 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'),
                ('MUSICBRAINZ_ALBUMID', '4d4a5ff4-4a38-4cf1-8e2f-0f64a65f4f5c'),
            ),
        )

    monkeypatch.setattr(
        media_stage,
        'write_canonical_metadata',
        preserve_audio,
    )
    monkeypatch.setattr(
        processing,
        'publish_release',
        lambda request: _publish_stub(request, '.mka'),
    )
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        session.add(
            JobRecord(
                id='job-non-flac',
                source_id=source.id,
                kind='lidarr_download',
                state='queued',
                created_at=datetime.now(UTC),
            )
        )
        session.commit()

    # When: the worker processes and publishes the queued source.
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()

    # Then: the worker completes and publishes the source unchanged.
    with Session(engine) as session:
        job = session.get(JobRecord, 'job-non-flac')
        assert job is not None
        assert job.state == 'completed'
        assert job.failure_reason is None
        published_audio = next(config.media_root.rglob('*.mka'))
        assert published_audio.suffix == '.mka'
        assert published_audio.read_bytes() == original_bytes


def _publish_stub(request: processing.PublicationRequest, suffix: str) -> PublicationResult:
    published_release = request.media_root / 'Artist' / 'Release'
    published_release.mkdir(parents=True, exist_ok=True)
    staged_audio = next(request.staged_release.glob(f'*{suffix}'))
    published_audio = published_release / f'track{suffix}'
    published_audio.write_bytes(staged_audio.read_bytes())
    return PublicationResult(published_release, published_audio)


def test_worker_when_source_lacks_identity_skips_decoder_and_publication(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a valid source without explicit identity.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        session.add(
            JobRecord(
                id='job-decoder',
                source_id=source.id,
                kind='filesystem_scan',
                state='queued',
                created_at=datetime.now(UTC),
            )
        )
        session.commit()

    def fail_media_stage(request: object) -> None:
        _ = request
        pytest.fail('source without identity must not invoke media staging')

    monkeypatch.setattr(processing, 'process_media', fail_media_stage)

    # When: the worker processes the queued initial job.
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()

    # Then: the job completes without media-tool work or publication.
    with Session(engine) as session:
        job = session.get(JobRecord, 'job-decoder')
        source = session.get(SourceRecord, job.source_id) if job is not None else None
        assert job is not None and job.state == 'completed'
        assert source is not None and source.intake_state == 'present'
        assert source.library_record is not None
        assert source.library_record.events[-1].kind == 'publication_deferred_for_identity'
        assert source.library_publications == []
    assert not config.staging_root.exists()
    assert not config.media_root.exists()


def test_worker_when_unexpected_processing_error_retries_without_quarantining_source(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a valid source with a queued initial job.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        session.add(
            JobRecord(
                id='job-unexpected',
                source_id=source.id,
                kind='filesystem_scan',
                state='queued',
                created_at=datetime.now(UTC),
            )
        )
        session.commit()

    def fail_unexpected(*_args: object, **_kwargs: object) -> None:
        raise RuntimeError('unexpected processing failure')

    monkeypatch.setattr(ProcessingWorker, '_process', fail_unexpected)

    # When: the worker encounters an exception outside its typed processing errors.
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()

    # Then: the source stays valid and the job remains retryable.
    with Session(engine) as session:
        job = session.get(JobRecord, 'job-unexpected')
        source = session.get(SourceRecord, job.source_id) if job is not None else None
        assert job is not None and job.state == 'queued'
        assert source is not None and source.intake_state == 'discovered'
        assert source.library_record is not None
        assert source.library_record.events[-1].kind == 'processing_retry'


def test_worker_analyzes_flac_in_staged_provider_phases(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    # Given: an import with both providers configured and durable provider schedules.
    config = replace(
        _config(tmp_path),
        musicbrainz_provider=MusicBrainzFixtureProvider(Path(__file__).parent / 'fixtures' / 'musicbrainz'),
        acoustid_provider=AcoustIdFixtureProvider(Path(__file__).parent / 'fixtures' / 'acoustid'),
    )
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    fingerprint = FingerprintResult(
        FingerprintState.SUCCESS,
        'fixture-fingerprint',
        10,
        'fixture',
        'a' * 64,
        None,
        None,
    )
    monkeypatch.setattr(media_stage, 'calculate_fingerprint', lambda *_args, **_kwargs: fingerprint)
    _ = (config.incoming_root / 'cover.jpg').write_bytes(b'\xff\xd8\xfffixture\xff\xd9')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        source_id = source.id
        session.add_all(
            (
                ProviderScheduleRecord(provider_name='musicbrainz', next_start_at=datetime.now(UTC)),
                ProviderScheduleRecord(provider_name='acoustid', next_start_at=datetime.now(UTC)),
                JobRecord(
                    id='job-phases',
                    source_id=source.id,
                    kind='filesystem_scan',
                    state='queued',
                    created_at=datetime.now(UTC),
                ),
            )
        )
        session.commit()

    # When: the worker drains initial import and both provider analyses.
    with Session(engine) as session:
        worker = ProcessingWorker(session, config)
        assert worker.run_once()
        session.commit()
        assert worker.run_once()
        session.commit()
        source = session.get(SourceRecord, source_id)
        assert source is not None
        assert [attempt.provider_name for attempt in source.provider_attempts] == ['acoustid']
        assert [job.kind for job in session.query(JobRecord).order_by(JobRecord.created_at, JobRecord.id)] == [
            'filesystem_scan',
            'acoustid_analysis',
            'musicbrainz_analysis',
        ]
        assert worker.run_once()
        session.commit()

        # Then: collection is complete and selection is deferred to a source-targeted job.
        with Session(engine) as session:
            jobs = list(session.query(JobRecord).order_by(JobRecord.created_at, JobRecord.id))
            assert [job.kind for job in jobs] == [
                'filesystem_scan',
                'acoustid_analysis',
                'musicbrainz_analysis',
                'candidate_selection',
            ]
        assert [job.state for job in jobs] == ['completed', 'completed', 'completed', 'queued']
        source = session.get(SourceRecord, source_id)
        assert source is not None and source.library_record is not None
        assert [attempt.provider_name for attempt in source.provider_attempts] == ['acoustid', 'musicbrainz']
        assert source.library_record.musicbrainz_recording_id is None
        assert source.library_record.musicbrainz_release_id is None
        assert source.library_record.processing_state == 'analyzing'
        assert source.library_publications == []
        claimed: list[str] = []
        assert worker.run_once(on_claimed=lambda _job_id, kind: claimed.append(kind))
        worker._session.commit()
        with Session(engine) as check:
            source = check.get(SourceRecord, source_id)
            job = check.query(JobRecord).filter_by(kind='candidate_selection').one()
            folder_job = check.query(JobRecord).filter_by(kind='folder_release_selection').one()
            assert claimed == ['candidate_selection']
            assert job.state == 'completed', (
                job.state,
                job.failure_reason,
                job.next_attempt_at,
                [(attempt.state, attempt.finished_at) for attempt in job.attempts],
            )
            assert folder_job.state == 'queued'
            assert source is not None and source.library_record is not None
            assert source.library_record.processing_state == 'analyzing'
            assert source.library_record.musicbrainz_recording_id is None
            assert source.library_record.musicbrainz_release_id is None


def test_worker_when_reanalysis_source_is_unchanged_reuses_decoder_and_fingerprint_evidence(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a processed FLAC source with durable decoder and fingerprint evidence.
    config = replace(
        _config(tmp_path),
        musicbrainz_provider=MusicBrainzFixtureProvider(Path(__file__).parent / 'fixtures' / 'musicbrainz'),
        acoustid_provider=AcoustIdFixtureProvider(Path(__file__).parent / 'fixtures' / 'acoustid'),
    )
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        session.add_all(
            (
                ProviderScheduleRecord(provider_name='musicbrainz', next_start_at=datetime.now(UTC)),
                ProviderScheduleRecord(provider_name='acoustid', next_start_at=datetime.now(UTC)),
                JobRecord(
                    id='initial',
                    source_id=source.id,
                    kind='filesystem_scan',
                    state='queued',
                    created_at=datetime.now(UTC),
                ),
            )
        )
        session.commit()

    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()
        source = session.scalar(select(SourceRecord))
        assert source is not None
        for job in session.scalars(select(JobRecord).where(JobRecord.state == 'queued')):
            job.state = 'completed'
        session.add(
            JobRecord(
                id='full-reanalysis',
                source_id=source.id,
                kind='filesystem_scan',
                state='queued',
                created_at=datetime.now(UTC),
            )
        )
        session.commit()

    def unexpected_media_tool(*_args: object, **_kwargs: object) -> None:
        raise AssertionError('unchanged source must reuse durable media evidence')

    monkeypatch.setattr(media_stage, 'calculate_fingerprint', unexpected_media_tool)

    # When: the full reanalysis scans the same unchanged source observation.
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()

    # Then: the scan completes without invoking ffmpeg-backed inspection or fpcalc.
    with Session(engine) as session:
        reanalysis = session.get(JobRecord, 'full-reanalysis')
        assert reanalysis is not None and reanalysis.state == 'completed'


def test_worker_when_valid_source_has_no_canonical_tags_queues_review_without_publishing(tmp_path: Path) -> None:
    # Given: a valid FLAC with no source tags and an immutable queued job.
    allocated_suffixes: list[str] = []

    def allocate_filename(suffix: str) -> str:
        allocated_suffixes.append(suffix)
        return f'Track 77{suffix}'

    config = replace(
        _config(tmp_path),
        acoustid_provider=AcoustIdFixtureProvider(Path(__file__).parent / 'fixtures' / 'acoustid'),
        unsorted_filename_allocator=allocate_filename,
    )
    config.incoming_root.mkdir()
    source_path = _tagless_flac(config.incoming_root / 'tagless.flac')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        session.add(ProviderScheduleRecord(provider_name='acoustid', next_start_at=datetime.now(UTC)))
        session.add(
            JobRecord(
                id='job-tagless',
                source_id=source.id,
                kind='filesystem_scan',
                state='queued',
                created_at=datetime.now(UTC),
            )
        )
        session.commit()

    # When: one worker processes the valid source.
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()

        # Then: the source is reviewable, the job is complete, and audio remains unpublished.
    with Session(engine) as session:
        job = session.get(JobRecord, 'job-tagless')
        assert job is not None and job.state == 'completed'
        source = session.get(SourceRecord, job.source_id)
        assert source is not None and source.intake_state == 'present'
        assert source.library_publications == []
        assert source.library_record is not None and source.library_record.processing_state == 'needs_review'
        assert [evidence.state for evidence in source.fingerprints] == ['success']
        assert source.fingerprints[0].fingerprint
        assert source.provider_attempts == []
        assert source.candidates == []
        assert [decision.rationale for decision in source.review_decisions] == []
        revisions = {
            revision.layer: json.loads(revision.tags_json) for revision in source.library_record.metadata_revisions
        }
        assert revisions == {'original': {}}
    assert allocated_suffixes == []
    assert not config.media_root.exists()


def test_worker_quarantines_source_when_persisted_root_is_disabled(tmp_path: Path) -> None:
    # Given: a queued valid FLAC owned by a root disabled after reconciliation.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    _ = write_normalized_tags(
        source_path,
        (
            *read_normalized_tags(source_path),
            ('MUSICBRAINZ_RECORDINGID', 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'),
            ('MUSICBRAINZ_ALBUMID', '4d4a5ff4-4a38-4cf1-8e2f-0f64a65f4f5c'),
        ),
    )
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "root-boundary.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        root = session.get(SourceRootRecord, 'legacy')
        assert root is not None
        root.enabled = False
        session.add(
            JobRecord(
                id='root-boundary-job',
                source_id=source.id,
                kind='filesystem_scan',
                state='queued',
                created_at=datetime.now(UTC),
            )
        )
        session.commit()

    # When: the worker enters a persisted source boundary.
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()

        # Then: it quarantines before staging or publication and records the root-boundary event.
        job = session.get(JobRecord, 'root-boundary-job')
        source = session.get(SourceRecord, job.source_id) if job is not None else None
        event = session.query(LibraryEventRecord).filter_by(kind='root_boundary').one()
        assert job is not None and job.state == 'quarantined'
        assert source is not None and source.intake_state == 'quarantined'
        assert event.source_id == source.id
        assert not (config.staging_root / 'root-boundary-job').exists()


def test_worker_when_media_root_is_a_symlink_keeps_the_published_job_succeeded(tmp_path: Path) -> None:
    # Given: a lexical media root that resolves to the physical publication root.
    config = _config(tmp_path)
    physical_media_root = tmp_path / 'physical-media'
    physical_media_root.mkdir()
    config.media_root.symlink_to(physical_media_root, target_is_directory=True)
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    _ = (config.incoming_root / 'cover.jpg').write_bytes(b'\xff\xd8\xfffixture\xff\xd9')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        session.add(
            JobRecord(
                id='job-symlink-root',
                source_id=source.id,
                kind='filesystem_scan',
                state='queued',
                created_at=datetime.now(UTC),
            )
        )
        session.commit()

    # When: publication returns its canonical physical release path.
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()

    # Then: the path is persisted relative to the canonical root and the job remains successful.
    with Session(engine) as session:
        job = session.get(JobRecord, 'job-symlink-root')
        assert job is not None and job.state == 'completed'
        assert [attempt.state for attempt in job.attempts] == ['succeeded']


def test_worker_when_interrupted_job_is_stale_reclaims_it_with_a_new_attempt(tmp_path: Path) -> None:
    # Given: a persisted job abandoned by a prior worker.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    _ = (config.incoming_root / 'cover.jpg').write_bytes(b'\xff\xd8\xfffixture\xff\xd9')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        job = JobRecord(
            id='job-1',
            source_id=source.id,
            kind='filesystem_scan',
            state='running',
            created_at=datetime.now(UTC) - timedelta(minutes=10),
        )
        job.attempts = [
            JobAttemptRecord(
                attempt_number=1,
                state='running',
                started_at=datetime.now(UTC) - timedelta(minutes=10),
                finished_at=None,
            )
        ]
        session.add(job)
        session.commit()

    # When: a restarted worker claims jobs older than its lease.
    with Session(engine) as session:
        assert ProcessingWorker(session, config, lease_age=timedelta(seconds=1)).run_once()
        session.commit()

    # Then: the abandoned attempt is retained and the retry reaches a terminal state.
    with Session(engine) as session:
        job = session.get(JobRecord, 'job-1')
        assert job is not None and job.state == 'completed'
        assert [attempt.state for attempt in job.attempts] == ['interrupted', 'succeeded']


def test_worker_when_stale_attempts_exceed_limit_blocks_without_restarting_provider(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a provider job whose third attempt was abandoned after the worker lease expired.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "stale-provider.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        job = JobRecord(
            id='stale-provider',
            source_id=source.id,
            kind='musicbrainz_analysis',
            state='running',
            created_at=datetime.now(UTC) - timedelta(minutes=20),
        )
        job.attempts = [
            JobAttemptRecord(
                attempt_number=attempt_number,
                state='interrupted' if attempt_number < 3 else 'running',
                started_at=datetime.now(UTC) - timedelta(minutes=20 - attempt_number),
                finished_at=datetime.now(UTC) - timedelta(minutes=19 - attempt_number) if attempt_number < 3 else None,
            )
            for attempt_number in range(1, 4)
        ]
        session.add(job)
        session.commit()

    processed_job_ids: list[str] = []

    def record_processing(worker: ProcessingWorker, claimed: ClaimedJob, now: datetime) -> None:
        _ = worker, now
        processed_job_ids.append(claimed.job.id)

    monkeypatch.setattr(ProcessingWorker, '_process', record_processing)

    # When: a new worker observes the expired provider attempt.
    with Session(engine) as session:
        assert ProcessingWorker(session, config, lease_age=timedelta(seconds=1)).run_once()
        session.commit()

    # Then: it preserves history, blocks the exhausted job, and never starts a fourth provider call.
    with Session(engine) as session:
        job = session.get(JobRecord, 'stale-provider')
        assert job is not None and job.state == 'blocked_infrastructure'
        assert [attempt.state for attempt in job.attempts] == [
            'interrupted',
            'interrupted',
            'interrupted',
            'blocked_infrastructure',
        ]
    assert processed_job_ids == []


def test_worker_when_source_was_replaced_supersedes_stale_analysis_job(tmp_path: Path) -> None:
    # Given: a provider job queued before its source became an immutable replaced version.
    config = _config(tmp_path)
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "replaced-source.db"}')
    Base.metadata.create_all(engine)
    now = datetime.now(UTC)
    with Session(engine) as session:
        root = SourceRootRecord(
            id='root',
            display_name='root',
            canonical_path=str(tmp_path),
            enabled=True,
            scan_state='scanned',
            created_at=now,
            updated_at=now,
        )
        record = LibraryRecord(id='record', created_at=now, updated_at=now)
        source = SourceRecord(
            id='replaced-source',
            source_path=str(tmp_path / 'replaced.flac'),
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            duration_seconds=180,
            origin='manual',
            intake_state='replaced',
            source_root=root,
            library_record=record,
        )
        job = JobRecord(
            id='stale-analysis',
            source_id=source.id,
            kind='acoustid_analysis',
            state='queued',
            created_at=now,
        )
        session.add_all((root, record, source, job))
        session.commit()

    # When: the worker claims the stale provider job.
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()

    # Then: it is superseded without processing or touching the immutable source version.
    with Session(engine) as session:
        job = session.get(JobRecord, 'stale-analysis')
        source = session.get(SourceRecord, 'replaced-source')
        assert job is not None and job.state == 'superseded'
        assert [attempt.state for attempt in job.attempts] == ['succeeded']
        assert source is not None and source.intake_state == 'replaced'


def test_worker_when_publication_is_transient_waits_before_reclaiming_then_succeeds(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Given: a queued source whose first publication attempt has a transient failure.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    _ = write_normalized_tags(
        source_path,
        (
            *read_normalized_tags(source_path),
            ('MUSICBRAINZ_RECORDINGID', 'f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a'),
            ('MUSICBRAINZ_ALBUMID', '4d4a5ff4-4a38-4cf1-8e2f-0f64a65f4f5c'),
        ),
    )
    _ = (config.incoming_root / 'cover.jpg').write_bytes(b'\xff\xd8\xfffixture\xff\xd9')
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        source = _source(session, source_path)
        session.add(
            JobRecord(
                id='job-retry',
                source_id=source.id,
                kind='filesystem_scan',
                state='queued',
                created_at=datetime.now(UTC),
            )
        )
        session.commit()
        publish = processing.replace_published_audio

    def transient_failure(*_args: object, **_kwargs: object) -> None:
        raise PublicationError('temporary publish failure')

    monkeypatch.setattr(processing, 'replace_published_audio', transient_failure)

    # When: the worker records the transient failure, then polls before the retry deadline.
    with Session(engine) as session:
        worker = ProcessingWorker(session, config)
        assert worker.run_once()
        session.commit()
        assert not worker.run_once()
        job = session.get(JobRecord, 'job-retry')
        assert job is not None and job.next_attempt_at is not None
        assert not (config.staging_root / 'job-retry').exists()
        job.next_attempt_at = datetime.now(UTC) - timedelta(seconds=1)
        session.commit()

    # Then: a due retry reclaims the source and reaches success.
    monkeypatch.setattr(processing, 'replace_published_audio', publish)
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()
        job = session.get(JobRecord, 'job-retry')
        assert job is not None and job.state == 'completed'
        assert [attempt.state for attempt in job.attempts] == ['retry_wait', 'succeeded']


def test_worker_when_source_tags_cannot_form_a_fallback_publishes_observed_tags(tmp_path: Path) -> None:
    # Given: a valid FLAC whose observed tags cannot form a canonical fallback.
    config = _config(tmp_path)
    config.incoming_root.mkdir()
    source_path = _flac(config.incoming_root / 'fixture.flac')
    tags = tuple((name, value) for name, value in read_normalized_tags(source_path) if name != 'ALBUM')
    _ = write_normalized_tags(source_path, tags)
    engine = create_engine(f'sqlite+pysqlite:///{tmp_path / "worker.db"}')
    Base.metadata.create_all(engine)
    with Session(engine) as session:
        session.add(UnsortedFilenameCounterRecord(id=1, next_number=0))
        source = _source(session, source_path)
        session.add(
            JobRecord(
                id='job-invalid-tags',
                source_id=source.id,
                kind='filesystem_scan',
                state='queued',
                created_at=datetime.now(UTC),
            )
        )
        session.commit()

    # When: the worker processes the structurally unusable fallback source.
    with Session(engine) as session:
        assert ProcessingWorker(session, config).run_once()
        session.commit()

    # Then: the claim is terminal and observed tags remain reviewable without publication.
    with Session(engine) as session:
        job = session.get(JobRecord, 'job-invalid-tags')
        source = session.get(SourceRecord, job.source_id) if job is not None else None
        assert job is not None and job.state == 'completed'
        assert [attempt.state for attempt in job.attempts] == ['succeeded']
        assert source is not None and source.intake_state == 'present'
        assert source.library_record is not None and source.library_record.processing_state == 'needs_review'
        assert source.review_decisions == []
        assert {revision.layer for revision in source.library_record.metadata_revisions} == {'original'}
        assert source.library_publications == []
    assert not config.media_root.exists()
