from __future__ import annotations

from pathlib import Path

import pytest

from tests.integration import task_9a_reconciliation as driver


def test_arguments_when_upgrade_pin_is_supplied_preserves_both_immutable_pins() -> None:
    # Given: the documented pinned acceptance invocation.
    current = 'sha256:' + 'a' * 64
    upgrade = 'sha256:' + 'b' * 64

    # When: the command boundary parses the invocation.
    arguments = driver.parse_arguments(
        ('--lidarr-url', 'http://lidarr.test', '--pin', current, '--upgrade-pin', upgrade)
    )

    # Then: the lifecycle receives both distinct pins rather than a mutable tag.
    assert arguments.pin == current
    assert arguments.upgrade_pin == upgrade


def test_arguments_when_upgrade_pin_is_absent_fails_before_fixture_creation() -> None:
    # Given: a command missing the mandatory controlled-upgrade pin.
    current = 'sha256:' + 'a' * 64

    # When: the CLI boundary parses the incomplete invocation.
    # Then: it fails before any test-stand path can be mutated.
    with pytest.raises(RuntimeError, match='--upgrade-pin is required'):
        _ = driver.parse_arguments(('--lidarr-url', 'http://lidarr.test', '--pin', current))


def test_failure_artifact_when_preflight_blocks_reports_no_mutation(tmp_path: Path) -> None:
    # Given: a failure result before the lifecycle enters fixture creation.
    result = driver.PreflightResult(
        enabled=False,
        baseline_capture_allowed=False,
        reasons=('Lidarr container is unavailable',),
        compose_image=None,
        requested_pin='sha256:' + 'a' * 64,
        observed_image=None,
        observed_version=None,
        upgrade_pin='sha256:' + 'b' * 64,
        container_error='missing',
    )
    evidence = tmp_path / 'evidence.json'

    # When: the fail-closed artifact is persisted.
    driver.write_failure_artifact(evidence, result)

    # Then: it records disabled reconciliation and no created fixture path.
    report = evidence.read_text(encoding='utf-8')
    assert '"enabled": false' in report
    assert '"created_paths": []' in report
