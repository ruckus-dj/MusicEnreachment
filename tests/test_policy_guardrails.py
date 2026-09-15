from __future__ import annotations

from pathlib import Path

import pytest

from music_ingest.cli.dry_run import DryRunMutationError, run_dry_run


def test_policy_guardrails_when_dry_run_targets_source_tree_rejects_source_mutation(tmp_path: Path) -> None:
    # Given: an audio source directory that has no external report location.
    source_directory = tmp_path / 'library'
    source_directory.mkdir()
    _ = (source_directory / 'sample.flac').write_bytes(b'fixture')

    # When: dry-run output is requested inside that source tree.
    with pytest.raises(DryRunMutationError, match='outside the source tree'):
        _ = run_dry_run(source_directory, source_directory / 'reports')

    # Then: the read-only source boundary rejects the write before scanning.
