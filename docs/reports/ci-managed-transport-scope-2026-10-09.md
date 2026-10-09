# CI managed-transport fixture scope correction

Date: 2026-10-09. Checked revision: `4dd7272` (the changes described below are
uncommitted working-tree edits).

## Trigger

Latest CI run
[37915702409](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/37915702409)
failed in the native platform matrix at the managed-tool transport fixture step.
The `task fixtures:managed-transport` provisioning step resolves and downloads
approved FFmpeg release artifacts from the network and failed with HTTP `403`
before the Windows tests in that job ran, so the matrix did not reach the native
backend/frontend test and build steps.

## Owner decision

External network tests must not run in CI. Managed-transport fixture
provisioning resolves and downloads real release archives, so it is an online
check and cannot be part of the hermetic CI gate.

## Change

- `.github/workflows/ci.yml`: removed the `Provision approved managed-tool
  transport fixture and run evidence tests` step and the `Upload managed-tool
  transport evidence` step from the `platforms` job. The native `go test`,
  `npm test`, frontend build, asset staging and backend build steps are
  unchanged and remain hermetic.
- `Taskfile.yml`: kept `fixtures:provision` and `fixtures:managed-transport`,
  and clarified their descriptions as manual online acceptance, explicitly not
  an automated CI gate.
- `frontend/src/features/setup/SetupManager.test.tsx`: restored the awaited
  `waitFor` around the synchronous `getAllByText` count in the full-wizard
  test, using the default `waitFor` budget while retaining the test's `10_000`
  ms total budget. This addresses the verify wizard timeout.

No auth or token handling changed. No production code, dependency, command,
test or gate semantics changed beyond the CI steps removed and the frontend
test assertion restored.

## Scope

Managed-tool transport evidence is now a manual online lane, not an automated
CI gate. The evidence and platform caveats recorded in
[source-io-evidence-2026-10-03.md](source-io-evidence-2026-10-03.md) remain
historical and are not re-run or re-verified here.

## Limitations

This report records the CI failure and the scope correction. It does not claim
that the managed-transport tests pass after the change: they are no longer run
in CI and require an explicit manual `task fixtures:managed-transport` run with
network access.
