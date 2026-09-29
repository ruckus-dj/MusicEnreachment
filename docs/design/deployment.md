# Deployment and first setup

## Server paths

Browser-selected values always refer to the machine running MeloTrove, never to
the browser workstation. In the base Compose deployment the host output path
`${MELOTROVE_OUTPUT_DIR:-./music}` is mounted at
`/var/lib/melotrove/output`; configure this container path in Setup.

The output directory must be new or empty. MeloTrove does not import an
existing publication library during initial setup. The configured tools and
output roots must be absolute, writable and non-overlapping.

## Managed tools directory

The persistent Compose volume at `/var/lib/melotrove/tools` is intentionally
not part of the image. It may be a mixed directory containing administrator
files. MeloTrove owns only installations recorded in PostgreSQL and stored in
the versioned `ffmpeg/<version>/` and `fpcalc/<version>/` layout. Unknown files
are never discovered by scanning the tools root or deleted; an unknown file at
an exact managed executable
target requires explicit overwrite confirmation. Only exact
paths recorded for database-managed installations are written or removed.

## Platform recovery

The first database-backed startup fixes `GOOS` and `GOARCH` for the instance.
Do not move its database between platforms expecting automatic tool migration.
A mismatch leaves diagnostic and UI endpoints available, but readiness reports
failure and Setup/product operations are blocked. There is no automatic platform
or tool migration. Restore the recorded platform or use a separately initialized
database.
