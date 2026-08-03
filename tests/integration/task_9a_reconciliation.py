from __future__ import annotations

import json
import re
import shutil
from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path
from subprocess import CompletedProcess, TimeoutExpired, run
from sys import argv
from typing import Final

from tests.integration.task_9a_lifecycle import lifecycle_preconditions

_DIGEST = re.compile(r'^sha256:[0-9a-f]{64}$')
_LIDARR_IMAGE = re.compile(r'(?ms)^  lidarr:\n(?P<body>.*?)(?=^  [a-z][^\n]*:\n|\Z)')
_IMAGE = re.compile(r'^    image: (?P<image>\S+)$', re.MULTILINE)
_IMAGE_DEFAULT = re.compile(r'^\$\{LIDARR_IMAGE:-(?P<image>[^}]+)\}$')
type JsonValue = str | int | bool | None | list['JsonValue'] | dict[str, 'JsonValue']


@dataclass(frozen=True, slots=True)
class Arguments:
    lidarr_url: str
    pin: str
    stand_root: Path
    evidence: Path
    container: str
    upgrade_pin: str | None


@dataclass(frozen=True, slots=True)
class PreflightInput:
    compose_path: Path
    requested_pin: str
    observed_image: str | None
    observed_version: str | None
    container_available: bool
    upgrade_pin: str | None = None
    container_error: str | None = None


@dataclass(frozen=True, slots=True)
class PreflightResult:
    enabled: bool
    baseline_capture_allowed: bool
    reasons: tuple[str, ...]
    compose_image: str | None
    requested_pin: str
    observed_image: str | None
    observed_version: str | None
    upgrade_pin: str | None
    container_error: str | None


@dataclass(frozen=True, slots=True)
class ContainerObservation:
    available: bool
    image: str | None
    version: str | None
    error: str | None


@dataclass(frozen=True, slots=True)
class LifecycleResult:
    enabled: bool
    assertions: tuple[str, ...]
    created_paths: tuple[str, ...]
    captures: Mapping[str, JsonValue]


def evaluate_preflight(preflight: PreflightInput) -> PreflightResult:
    compose_image = _lidarr_image(preflight.compose_path)
    reasons: list[str] = []
    if not _DIGEST.fullmatch(preflight.requested_pin):
        reasons.append('requested Lidarr pin is not an immutable sha256 digest')
    if compose_image is None:
        reasons.append('compose Lidarr image is unavailable')
    elif '@sha256:' not in compose_image:
        reasons.append('compose Lidarr image is mutable')
    elif compose_image.rsplit('@', maxsplit=1)[1] != preflight.requested_pin:
        reasons.append('compose Lidarr image does not match requested pin')
    if not preflight.container_available:
        reasons.append('Lidarr container is unavailable')
    elif preflight.observed_image != preflight.requested_pin:
        reasons.append('running Lidarr image does not match requested pin')
    if not preflight.observed_version:
        reasons.append('running Lidarr version is unavailable')
    if preflight.upgrade_pin is None:
        reasons.append('controlled upgrade image pin is unavailable')
    elif not _DIGEST.fullmatch(preflight.upgrade_pin):
        reasons.append('controlled upgrade image pin is not an immutable sha256 digest')
    elif preflight.upgrade_pin == preflight.requested_pin:
        reasons.append('controlled upgrade image pin must differ from the running image')
    enabled = not reasons
    return PreflightResult(
        enabled=enabled,
        baseline_capture_allowed=enabled,
        reasons=tuple(reasons),
        compose_image=compose_image,
        requested_pin=preflight.requested_pin,
        observed_image=preflight.observed_image,
        observed_version=preflight.observed_version,
        upgrade_pin=preflight.upgrade_pin,
        container_error=preflight.container_error,
    )


def write_failure_artifact(
    destination: Path, result: PreflightResult, lifecycle: LifecycleResult | None = None
) -> None:
    completed_lifecycle = lifecycle or LifecycleResult(False, (), (), {})
    report: dict[str, JsonValue] = {
        'task': '12',
        'verdict': 'blocked',
        'reconciliation': {'enabled': False, 'baseline_capture_allowed': result.baseline_capture_allowed},
        'image': {
            'compose_reference': result.compose_image,
            'requested_pin': result.requested_pin,
            'observed_digest': result.observed_image,
            'observed_version': result.observed_version,
            'upgrade_pin': result.upgrade_pin,
            'container_inspection_error': result.container_error,
        },
        'commands': {
            'container_inspect': "docker inspect --format '{{.Image}}\\n{{.Config.Image}}' music-enrichment-lidarr",
            'driver': (
                'PYTHONPATH=. uv run python tests/integration/task_9a_reconciliation.py '
                f'--lidarr-url http://localhost:8686 --pin {result.requested_pin} '
                f'--upgrade-pin {result.upgrade_pin}'
            ),
        },
        'failures': [*result.reasons, *completed_lifecycle.assertions],
        'driver_exit_code': 1,
        'probes': {
            'malformed_sidecars': 'not_applicable: fixture creation did not begin',
            'repeated_interruptions': 'not_applicable: pathname replacement did not begin',
            'misleading_success_output': 'not_applicable: no acceptance command ran',
            'hung_commands': 'docker inspection is bounded to 10 seconds',
            'network_egress': 'not_attempted: preflight makes no HTTP or provider request',
            'stale_state': 'not_applicable: no running Lidarr container was observed',
            'dirty_worktree': _dirty_worktree(),
            'common_data_filesystem': 'not_applicable: baseline capture did not begin',
            'media_write_restrictions': 'not_applicable: baseline capture did not begin',
            'controlled_upgrade': 'not_applicable: baseline capture did not begin',
        },
        'captures': dict(completed_lifecycle.captures),
        'cleanup': {
            'created_paths': list(completed_lifecycle.created_paths),
            'created_paths_removed': not completed_lifecycle.created_paths,
            'incoming_pathname_replaced': False,
        },
        'residual_risks': [
            'Reconciliation remained disabled because the lifecycle stopped at the first unsupported assertion.',
            'Raw incoming and normalized media remain separate by policy until a full pinned acceptance pass.',
        ],
    }
    _ = destination.parent.mkdir(parents=True, exist_ok=True)
    _ = destination.write_text(json.dumps(report, ensure_ascii=True, indent=2, sort_keys=True) + '\n', encoding='utf-8')


def _lidarr_image(compose_path: Path) -> str | None:
    try:
        contents = compose_path.read_text(encoding='utf-8')
    except FileNotFoundError:
        return None
    match = _LIDARR_IMAGE.search(contents)
    if match is None:
        return None
    image = _IMAGE.search(match.group('body'))
    if image is None:
        return None
    configured = image.group('image')
    default = _IMAGE_DEFAULT.fullmatch(configured)
    return configured if default is None else default.group('image')


def _inspect_container(container: str) -> ContainerObservation:
    command: Final = (
        'docker',
        'inspect',
        '--format',
        '{{.Image}}\n{{.Config.Image}}\n{{index .Config.Labels "org.opencontainers.image.version"}}',
        container,
    )
    try:
        completed: CompletedProcess[str] = run(command, capture_output=True, check=False, text=True, timeout=10)  # noqa: S603
    except FileNotFoundError:
        return ContainerObservation(False, None, None, 'docker executable is unavailable')
    except TimeoutExpired:
        return ContainerObservation(False, None, None, 'docker inspection timed out after 10 seconds')
    except OSError as error:
        return ContainerObservation(False, None, None, str(error))
    if completed.returncode != 0:
        return ContainerObservation(False, None, None, completed.stderr.strip() or 'docker inspection failed')
    image, separator, remainder = completed.stdout.strip().partition('\n')
    configured_image, separator, version = remainder.partition('\n')
    if not separator or not image or not configured_image or not version:
        return ContainerObservation(False, None, None, 'docker inspection omitted image digest or configured version')
    return ContainerObservation(True, image, version, None)


def _dirty_worktree() -> str:
    git = shutil.which('git')
    if git is None:
        return 'unknown: git executable is unavailable'
    try:
        completed: CompletedProcess[str] = run(  # noqa: S603
            (git, 'status', '--porcelain'), capture_output=True, check=False, text=True, timeout=10
        )
    except TimeoutExpired:
        return 'unknown: git status timed out after 10 seconds'
    except OSError as error:
        return f'unknown: {error}'
    if completed.returncode != 0:
        return f'unknown: {completed.stderr.strip() or "git status failed"}'
    return 'dirty' if completed.stdout else 'clean'


def parse_arguments(raw_arguments: tuple[str, ...] | None = None) -> Arguments:
    arguments = tuple(argv[1:]) if raw_arguments is None else raw_arguments
    default_stand_root = str(Path(__file__).parents[2] / 'test_stand')
    default_evidence = str(Path(__file__).parents[2] / '.omo/evidence/task-12-music-ingestion.json')
    return Arguments(
        _required_option('--lidarr-url', arguments),
        _required_option('--pin', arguments),
        Path(_optional_option('--stand-root', default_stand_root, arguments)),
        Path(_optional_option('--evidence', default_evidence, arguments)),
        _optional_option('--container', 'music-enrichment-lidarr', arguments),
        _required_option('--upgrade-pin', arguments),
    )


def _required_option(name: str, raw_arguments: tuple[str, ...]) -> str:
    value = _optional_option(name, '', raw_arguments)
    if not value:
        raise RuntimeError(f'{name} is required')
    return value


def _optional_option(name: str, default: str, raw_arguments: tuple[str, ...]) -> str:
    try:
        index = raw_arguments.index(name)
    except ValueError:
        return default
    if index + 1 == len(raw_arguments):
        raise RuntimeError(f'{name} requires a value')
    return raw_arguments[index + 1]


def execute_lifecycle(arguments: Arguments, preflight: PreflightResult) -> LifecycleResult:
    assertions, captures = lifecycle_preconditions(arguments.lidarr_url, arguments.stand_root, preflight.enabled)
    return LifecycleResult(False, assertions, (), captures)


def main() -> int:
    arguments = parse_arguments()
    observation = _inspect_container(arguments.container)
    result = evaluate_preflight(
        PreflightInput(
            compose_path=arguments.stand_root / 'compose.yaml',
            requested_pin=arguments.pin,
            observed_image=observation.image,
            observed_version=observation.version,
            container_available=observation.available,
            upgrade_pin=arguments.upgrade_pin,
            container_error=observation.error,
        )
    )
    lifecycle = execute_lifecycle(arguments, result)
    write_failure_artifact(arguments.evidence, result, lifecycle)
    return 0 if lifecycle.enabled else 1


if __name__ == '__main__':
    raise SystemExit(main())
