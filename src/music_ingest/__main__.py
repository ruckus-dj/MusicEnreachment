from __future__ import annotations

from pathlib import Path
from sys import argv

from pydantic import ValidationError
from uvicorn import run

from music_ingest.cli.dry_run import DryRunMutationError, run_dry_run
from music_ingest.config import PolicyYamlError, load_policy_bundle, render_safe_summary


def main() -> None:
    if len(argv) == 2 and argv[1] == 'serve':
        run('music_ingest.api.server:create_runtime_app', factory=True, host='0.0.0.0', port=8000)  # noqa: S104
        return
    if len(argv) == 4 and argv[1] == 'dry-run':
        try:
            reports = run_dry_run(Path(argv[2]), Path(argv[3]))
        except (DryRunMutationError, FileNotFoundError) as error:
            raise SystemExit(f'music-ingest: dry-run failed: {error}') from error
        print(f'dry-run reports: {reports.summary} {reports.review} {reports.proposals}')
        return
    if len(argv) != 2:
        msg = 'usage: python -m music_ingest serve | POLICY_DIRECTORY | dry-run SOURCE_DIRECTORY REPORT_DIRECTORY'
        raise SystemExit(msg)
    try:
        bundle = load_policy_bundle(Path(argv[1]))
    except (PolicyYamlError, ValidationError) as error:
        raise SystemExit('music-ingest: invalid policy configuration') from error
    print(render_safe_summary(bundle))


if __name__ == '__main__':
    main()
