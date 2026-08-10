from __future__ import annotations

from pathlib import Path
from sys import argv

from uvicorn import run

from music_ingest.cli.dry_run import DryRunMutationError, run_dry_run


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
        msg = 'usage: python -m music_ingest serve | dry-run SOURCE_DIRECTORY REPORT_DIRECTORY'
        raise SystemExit(msg)
    raise SystemExit('music-ingest: unknown command')


if __name__ == '__main__':
    main()
