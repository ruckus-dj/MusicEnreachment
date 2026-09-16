from __future__ import annotations

from pathlib import Path
from sys import argv

from uvicorn import run

from music_ingest.cli.dry_run import DryRunMutationError, run_dry_run
from music_ingest.cli.media_stage import MediaStageCliError, run_media_stage_cli


def main() -> None:
    if len(argv) > 1 and argv[1] == 'encoding-backfill':
        from music_ingest.cli.encoding_backfill import main as encoding_backfill_main

        encoding_backfill_main(argv[2:])
        return
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
    if len(argv) == 5 and argv[1] == 'media-stage':
        try:
            result = run_media_stage_cli(Path(argv[2]), Path(argv[3]), Path(argv[4]))
        except (MediaStageCliError, FileNotFoundError) as error:
            raise SystemExit(f'music-ingest: media-stage failed: {error}') from error
        print(f'media-stage output: {result.output_path}')
        return
    if len(argv) != 2:
        msg = (
            'usage: python -m music_ingest serve | '
            'dry-run SOURCE_DIRECTORY REPORT_DIRECTORY | '
            'media-stage INPUT OUTPUT_DIRECTORY TMP_DIRECTORY'
        )
        raise SystemExit(msg)
    raise SystemExit('music-ingest: unknown command')


if __name__ == '__main__':
    main()
