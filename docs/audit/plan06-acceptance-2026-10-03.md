# Plan 06 manual acceptance — started 2026-10-03, completed 2026-10-04

This records the actual manual acceptance runs and supersedes any earlier “not run” statements. Manual acceptance, cleanup, independent review, and full CI are complete as of 2026-10-04. The final reviewed code revision is `a42f09fded69fbd6b90f22122392aeb27c69e7f1`; see [independent review](plan06-independent-review-2026-10-04.md) and [source-I/O evidence](source-io-evidence-2026-10-03.md). No prior revision remains pending.

Target: `http://127.0.0.1:56814` (PID 46413), secondary database `plan06_twotab_20261003`.

## Evidence

- Initial `GET /api/setup`: HTTP 200; `completed:false`, `configuration_health.healthy:false`, sole problem `musicbrainz not verified`. Active ffmpeg/fpcalc installation IDs were present. The existing saved MusicBrainz mode was `public` with empty base URL, so no PUT or reinstall was needed.
- `POST /api/setup/check-musicbrainz`: HTTP 200, `{"success":true}`.
- Then two simultaneous GETs `/api/setup`, two simultaneous POSTs `/api/setup/complete`, and POST `/api/setup/paths/check` were issued via Python `ThreadPoolExecutor`. Results: GETs HTTP 200 and both healthy/not completed; completion POSTs both HTTP 204; path validation HTTP 200.
- Important limitation: the path-validation request omitted its `tools_directory` and `output_directory` JSON keys (the intended `{"tools_directory":"","output_directory":""}` was not sent). The API returned HTTP 200 using the existing saved paths. Thus the call checked those saved paths; it did **not** verify empty candidate paths or explicit non-empty candidate paths. Existing unit coverage for empty candidate paths is not a claim about this manual run. Completion closed setup before retry.
- Final stable `GET /api/setup` at `2026-10-03T20:20:09.230077+00:00`: HTTP 200, `completed:true`, `configuration_health.healthy:true`, `problems:null`, MusicBrainz verified at `2026-10-03T20:20:09.219931Z`.

The previously failed OpenAPI lookup at `/openapi.json` returned non-JSON; the actual route document was later fetched at `/api/openapi.json`. Independent full `task verify` later passed at the final revision; see the independent review record. This acceptance run itself did not run tests/build/gate.

## Primary manual scenarios (isolated stand)

- Real native macOS managed-tool lifecycle completed: ffmpeg `9.0.2` and fpcalc `v1.6.1` were downloaded, verified, and activated (operations `6e4b…`, `2ea0…`). Initial setup had MusicBrainz public check HTTP 200, followed by setup completion HTTP 204 and healthy state.
- Scanned a real managed WAV successfully (scan `941120b8-756b-457a-aca7-fb67d12360cf`, root `0c880554-497e-45d3-846c-92d4ebb790b4`, location `313423d5-db9b-48a4-a3cf-93ece6ee0031`). Analysis `8cf31173-9d20-4d0c-bb9f-d42a66429142` and repeat analysis `726433e2-9390-4778-8c6a-04200d9296e5` succeeded; inspector/reload returned the result. A controlled failure (`0550c360-d6cf-467b-a4de-483fd560ae49`) preserved the prior result. Deleting the root returned HTTP 204 and subsequent GET returned 404; the original WAV remained present with unchanged SHA-256 `a9121c305a1f712aeb0fe7157ccd67e81dd18b6f0de384d50dff318aafed7afe`.
- Installed alternate fpcalc `v1.6.0` (`a249a2fb-99a5-43d1-b9f2-c10d5aeb70d3`), moved tools to `tools-moved` (`857efa02-2c77-489f-9486-99b70e8ad95c`) with `remove_old_files:false`, then deleted the inactive installation (HTTP 204). Its managed file in B and installation row were removed; foreign B and the operator copy/foreign A were retained. Active ffmpeg `9.0.2` and fpcalc `v1.6.1` stayed active and available under the new root.
- An earlier report that the acceptance plan was missing is incorrect and superseded by this record.

## Cleanup status

Secondary server PID 46413 on port 56814 was stopped. The uniquely named,
session-owned PostgreSQL container and volume
`plan06-acceptance-e7173c2a-9f49-457c-acd4-a6adae2a933f` were removed; follow-up
Docker listings confirmed their absence. Both acceptance databases, including
`plan06_twotab_20261003`, were inside that disposable volume; no separate
database-drop or temporary-directory removal is claimed. Primary acceptance
server PID 38218 was then independently identified as `./build/backend/server`
listening on 127.0.0.1:56813 and stopped by the primary session. Existing user
deployments/databases were not modified. Evidence JSON, tools copies and source
fixture files remain in the approved temporary evidence directory.

The path-validation request omitted its `tools_directory` and `output_directory`
keys and exercised saved-path defaults only. Explicit candidate paths were not
verified manually; regression suites provide their separate coverage. Manual
acceptance ran on macOS, not Windows; native Windows evidence comes from CI.
Windows UNC/SMB exclusion was explicitly approved by the owner and is documented
in README and deployment conventions. These are scope/evidence limits, not
remaining blockers. Retained evidence directory:
`/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan06-e7173c2a-9f49-457c-acd4-a6adae2a933f`.
