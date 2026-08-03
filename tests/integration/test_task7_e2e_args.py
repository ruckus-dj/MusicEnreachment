from __future__ import annotations

from pathlib import Path

from .task7_e2e import parse_arguments


def test_arguments_when_omitted_use_task7_stand_defaults() -> None:
    # Given: the task-7 runner invoked without endpoint overrides.
    # When: its CLI boundary parses the command.
    arguments = parse_arguments(())

    # Then: it targets the isolated local stack and task-specific evidence artifact.
    assert arguments.stand_root == Path(__file__).parents[2] / 'test_stand'
    assert arguments.api_url == 'http://localhost:8787'
    assert arguments.navidrome_url == 'http://localhost:4533'
    assert arguments.evidence == Path(__file__).parents[2] / '.omo/evidence/music-ingest-platform/task-7-e2e.json'
