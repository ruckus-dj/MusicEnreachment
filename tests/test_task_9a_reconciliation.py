from __future__ import annotations

from pathlib import Path

from tests.integration import task_9a_reconciliation as driver


def test_preflight_when_compose_uses_mutable_lidarr_tag_blocks_before_baseline_capture(tmp_path: Path) -> None:
    # Given: a running image digest but a mutable compose source reference.
    compose = tmp_path / 'compose.yaml'
    _ = compose.write_text('services:\n  lidarr:\n    image: lscr.io/linuxserver/lidarr:latest\n', encoding='utf-8')

    # When: the Task 9a gate evaluates the deployment identity.
    result = driver.evaluate_preflight(
        driver.PreflightInput(
            compose_path=compose,
            requested_pin='sha256:' + 'a' * 64,
            observed_image='sha256:' + 'a' * 64,
            observed_version='2.14.1.4716-ls123',
            container_available=True,
        )
    )

    # Then: reconciliation is disabled before any baseline or pathname mutation.
    assert result.enabled is False
    assert result.baseline_capture_allowed is False
    assert 'compose Lidarr image is mutable' in result.reasons


def test_preflight_when_container_is_unavailable_writes_disabled_failure_artifact(tmp_path: Path) -> None:
    # Given: no runnable Docker container and an immutable compose reference.
    compose = tmp_path / 'compose.yaml'
    digest = 'sha256:' + 'b' * 64
    _ = compose.write_text(f'services:\n  lidarr:\n    image: lscr.io/linuxserver/lidarr@{digest}\n', encoding='utf-8')

    # When: the driver records the unavailable stand preflight.
    result = driver.evaluate_preflight(
        driver.PreflightInput(
            compose_path=compose,
            requested_pin=digest,
            observed_image=None,
            observed_version=None,
            container_available=False,
        )
    )
    evidence = tmp_path / 'task-12.json'
    driver.write_failure_artifact(evidence, result)

    # Then: the persisted gate is disabled and no reconciliation baseline exists.
    report = evidence.read_text(encoding='utf-8')
    assert '"enabled": false' in report
    assert '"baseline_capture_allowed": false' in report
    assert 'Lidarr container is unavailable' in report


def test_preflight_when_controlled_upgrade_pin_is_missing_blocks_before_baseline_capture(tmp_path: Path) -> None:
    # Given: a pinned, running Lidarr that has no distinct immutable upgrade target.
    compose = tmp_path / 'compose.yaml'
    digest = 'sha256:' + 'c' * 64
    _ = compose.write_text(f'services:\n  lidarr:\n    image: lscr.io/linuxserver/lidarr@{digest}\n', encoding='utf-8')

    # When: the Task 9a gate evaluates the complete lifecycle prerequisite.
    result = driver.evaluate_preflight(
        driver.PreflightInput(
            compose_path=compose,
            requested_pin=digest,
            observed_image=digest,
            observed_version='3.1.0.4875-ls36',
            container_available=True,
            upgrade_pin=None,
        )
    )

    # Then: no baseline may be captured because a controlled upgrade cannot run.
    assert result.enabled is False
    assert result.baseline_capture_allowed is False
    assert 'controlled upgrade image pin is unavailable' in result.reasons


def test_preflight_when_all_pinned_lifecycle_prerequisites_exist_allows_baseline_capture(tmp_path: Path) -> None:
    # Given: distinct immutable images and a reachable, versioned Lidarr test stand.
    compose = tmp_path / 'compose.yaml'
    current = 'sha256:' + 'd' * 64
    upgrade = 'sha256:' + 'e' * 64
    _ = compose.write_text(f'services:\n  lidarr:\n    image: lscr.io/linuxserver/lidarr@{current}\n', encoding='utf-8')

    # When: the Task 9a gate receives every lifecycle prerequisite.
    result = driver.evaluate_preflight(
        driver.PreflightInput(
            compose_path=compose,
            requested_pin=current,
            observed_image=current,
            observed_version='3.1.0.4875-ls36',
            container_available=True,
            upgrade_pin=upgrade,
        )
    )

    # Then: the actual lifecycle may capture its disposable fixture baseline.
    assert result.enabled is True
    assert result.baseline_capture_allowed is True
    assert result.reasons == ()
