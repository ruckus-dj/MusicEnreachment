# Plan 10: real-tools Docker runtime acceptance

**Result: partial; first-run acceptance blocked at MusicBrainz verification.**

- **Code revision:** `9d4b1ca4c9dfe09c0295a04e5b12c1f44926a25f`
- **Date:** 2026-10-09
- **Runtime:** isolated Docker Compose project `plan10-runtime-20261009`, Linux/arm64, built from the repository's `deploy/docker/Dockerfile`.

## Completed

- Built and started the application and PostgreSQL in an isolated Compose project. Its database and managed-tools volumes were project-scoped; runtime output and test audio copies were under the approved `/private/var/folders/.../T/opencode/plan10-runtime-20261009` scratch directory. No existing application stack was used.
- Confirmed the clean instance starts in incomplete Setup and accepts the temporary tools/output paths. Saved the runtime paths and `source` publication format for this disposable instance.
- Read the real compatible Linux/arm64 tool catalog and downloaded and verified its current listed releases through the application preflight/tool operations (no manually supplied binaries):
  - FFmpeg package release `9.0` from `btbn`; managed `ffmpeg` and `ffprobe` both passed lifecycle version verification and report `n9.0.2-23-g27b46f0fbc-20261008`.
  - `fpcalc` release `v1.6.1` from `chromaprint`; managed version reports `1.6.1`.
  - Both install operations reached `succeeded`; both installations were `ready` and active in the temporary database.
- Selected three audio files from the specifically approved source folder and copied them only into two temporary test roots. Both roots were mounted read-only into the app. A write attempt against a mounted root failed with `Read-only file system`. All three original source files retained the same SHA-256, size, and mtime after the run; each scratch copy also matched its original.

## Blocker and unrun acceptance steps

The real Setup MusicBrainz check (`POST /api/setup/check-musicbrainz`) returned `success: false` on three attempts. The app request took about 10 seconds each time, matching the configured request timeout. Consequently Setup remained incomplete with only `musicbrainz not verified` in configuration-health problems. No stub or alternate verification was used.

For diagnosis, the exact public MusicBrainz artist endpoint returned HTTP 200 both from the host and with `curl` in the app container's network namespace (about 0.2 seconds). The application check still timed out. This establishes a discrepancy in the current Go-checker runtime path; it does not establish that Setup can complete or that the app's check succeeded.

Because completed Setup is a prerequisite, the run did **not** complete Setup, create source roots, start scans, execute ffprobe/fpcalc analysis, assert staged-analysis artifact/matching behavior, or run artifact cleanup. Those steps remain unverified; this report is not a pass for the full scenario. No source-root or cleanup operation was created. The clean-instance SHA-256 setting was left as initialized; no test-specific setting change or source analysis was performed.

## Validation and cleanup

- Docker image build, PostgreSQL health check, Setup/path APIs, real catalog/preflight/tool-download APIs, tool operation snapshots, and verified-version records were observed directly through the running app.
- No lint, unit/integration test suite, or `task verify` was run; this was a local runtime-acceptance attempt only.
- The original user-owned source files were only read. The test Compose stack and its project volumes, scratch copies, and temporary evidence were removed after capture. No existing stack or volume was stopped or deleted.
