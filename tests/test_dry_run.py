from __future__ import annotations

import csv
from hashlib import sha256
from pathlib import Path

import pytest

import music_ingest.cli.dry_run as dry_run
from music_ingest.cli.dry_run import DryRunMutationError, MutationGuard, run_dry_run


def _snapshot(path: Path) -> tuple[str, int]:
    return sha256(path.read_bytes()).hexdigest(), path.stat().st_mtime_ns


def test_dry_run_reports_audio_nfo_and_lrc_without_mutating_sources(tmp_path: Path) -> None:
    source = tmp_path / 'library'
    source.mkdir()
    audio = source / 'Anacondaz - Sample.flac'
    nfo = source / 'Noize MC - Sample.nfo'
    lrc = source / 'Linkin Park - Sample.lrc'
    _ = audio.write_bytes(b'not-a-valid-flac')
    _ = nfo.write_text('<album>untrusted external text</album>', encoding='utf-8')
    _ = lrc.write_text('[00:00.00]untrusted external text', encoding='utf-8')
    before = {path: _snapshot(path) for path in (audio, nfo, lrc)}

    reports = run_dry_run(source, tmp_path / 'reports')

    assert reports.summary.exists()
    assert reports.review.exists()
    assert reports.proposals.exists()
    assert {path: _snapshot(path) for path in (audio, nfo, lrc)} == before
    summary = reports.summary.read_text(encoding='utf-8')
    assert '"files_scanned": 3' in summary
    assert '"audio": 1' in summary
    assert '"lrc": 1' in summary
    assert '"nfo": 1' in summary
    with reports.review.open(newline='', encoding='utf-8') as review_file:
        assert {row['path'] for row in csv.DictReader(review_file)} == {
            'Anacondaz - Sample.flac',
            'Linkin Park - Sample.lrc',
            'Noize MC - Sample.nfo',
        }
    proposals = reports.proposals.read_text(encoding='utf-8')
    assert proposals.count('"proposal": "review_one_album_at_a_time"') == 3
    assert 'untrusted external text' not in proposals


def test_dry_run_rejects_report_output_inside_source_tree(tmp_path: Path) -> None:
    source = tmp_path / 'library'
    source.mkdir()
    _ = (source / 'Sample.flac').write_bytes(b'not-a-valid-flac')

    with pytest.raises(DryRunMutationError, match='outside the source tree'):
        _ = run_dry_run(source, source / 'reports')


def test_mutation_guard_detects_a_source_write(tmp_path: Path) -> None:
    source = tmp_path / 'library'
    source.mkdir()
    track = source / 'Sample.lrc'
    _ = track.write_text('[00:00.00]before', encoding='utf-8')
    guard = MutationGuard.capture(source)
    _ = track.write_text('[00:00.00]after', encoding='utf-8')

    with pytest.raises(DryRunMutationError, match='source changed'):
        _ = guard.verify()


def test_dry_run_when_interrupted_between_reports_never_returns_success_and_recovers(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    source = tmp_path / 'library'
    source.mkdir()
    track = source / 'Linkin Park - Sample.lrc'
    _ = track.write_text('[00:00.00]unchanged', encoding='utf-8')
    before = _snapshot(track)
    reports = tmp_path / 'reports'

    def interrupt(_destination: Path, records: tuple[dry_run.ScanRecord, ...]) -> None:
        assert records
        raise KeyboardInterrupt

    monkeypatch.setattr(dry_run, '_write_review_csv', interrupt)

    for _ in range(2):
        with pytest.raises(KeyboardInterrupt):
            _ = run_dry_run(source, reports)

    assert (reports / 'summary.json').exists()
    assert not (reports / 'review.csv').exists()
    assert not (reports / 'proposals.jsonl').exists()
    assert _snapshot(track) == before
    monkeypatch.undo()

    recovered = run_dry_run(source, reports)

    assert recovered.summary.exists()
    assert recovered.review.exists()
    assert recovered.proposals.exists()
    assert _snapshot(track) == before
