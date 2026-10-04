# Plan 06 independent review — 2026-10-04

**Verdict: COMPLETE** for the scope agreed at the time of review. Reviewed code revision:
`a42f09fded69fbd6b90f22122392aeb27c69e7f1`. All nine audit findings A01–A09
have a corresponding implementation mechanism and regression coverage; final
native CI and the real manual scenario passed. This is a focused independent
review of the plan requirements and evidence, not an exhaustive certification
of every code path or possible interleaving. Windows UNC/SMB remains an
historically agreed exclusion in that slice, not supported behavior. The owner
decision dated 2026-10-04 subsequently clarifies that this is not a permanent
product ban or a future feature blocker.

Plan: [completed audit-corrections plan](../plans/done/06-done-plans-audit-corrections.md).
Historical baseline: [2026-10-02 audit](../audit/done-plans-validation-2026-10-02.md).
Evidence: [manual acceptance](plan06-acceptance-2026-10-03.md),
[managed source-I/O and CI](source-io-evidence-2026-10-03.md).

## A01–A09 review matrix

| Finding | Implemented protection and focused regression evidence | Review result |
| --- | --- | --- |
| A01 — installation deletion after tools-root move | Delete's filesystem cleanup uses the currently protected tools root within the repository operation; exact managed-file cleanup and move/delete interleavings are covered in setup-manager persistence/service tests. Manual acceptance additionally moved tools and deleted an inactive installation, confirming active files and foreign/operator copies remained intact. | PASS; cleanup is rooted in the transaction-protected state, not a stale pre-read. |
| A02 — root update vs install enqueue | Runtime tools-root changes and install/move enqueue share persistence-level protection and compare the expected root under that protection. The root-change/install barrier and both commit orders are covered by setup-manager integration regressions. | PASS; no successful stale preflight is accepted across a root change. |
| A03 — Setup filesystem probe races | Probe coordination is keyed to the actual normalized probe directory, including an existing ancestor for a missing output; Setup state, completion, runtime save, and path validation share the serialization contract. Coverage includes `TestValidatePathsMissingOutputSharesExistingAncestorProbeGuard`, `TestValidatePathsMissingOutputUnderCompletedOutputUsesAncestor`, and probe cleanup/semantics tests. | PASS; unrelated user contents are not ignored, and candidate-path empty behavior is not inferred from the manual call. |
| A04 — inventory rollback with retained scan rows | The down migration removes incompatible scan records before restoring the legacy CHECK; migration integration coverage exercises retained production-shaped scan history through migrator rollback/re-up. | PASS; forward history was not rewritten. |
| A05 — stale source-root delete confirmation | Delete rechecks the confirmed configured path and location count while holding the root in its delete transaction. Persistence/service integration tests cover concurrent update/scan confirmation conflict without deleting source state. | PASS; preflight is not the authority for deletion. |
| A06 — scan traversal through replaced ancestor | Source traversal, final file confirmation, and probing use root-scoped pinned directory/file handles rather than unsafe absolute-path reads. Swap, symlink, nested traversal and reuse/failure cases are exercised; final CI includes `TestSourceScanDoesNotFollowAncestorSwappedToExternalSymlink` on macOS. Windows separately passed `TestWindowsAdapterUsesPinnedDirectoryAcrossJunctionSwap` and `TestWindowsAdapterRejectsJunctionsAndClosesFailedOpens` (not skipped). | PASS; no external symlink object is followed by scan. Windows analysis ancestor-swap attempt was denied by the OS; it is not claimed as a successful swap. |
| A07 — overlap validation writing into source | Source overlap checking is read-only and fails closed where alias semantics cannot be established; filesystem tests observe create attempts, including case-related/alias paths and missing managed suffixes. | PASS; no source write probe is used by source create/edit/revalidation. |
| A08 — inconsistent inspector snapshot | Persistence reads root/location/variant/active operation as one read-only consistent snapshot; query-barrier integration tests cover first apply and unlink/orphan cleanup races, plus missing/foreign resources and repeat analysis. | PASS; no mixed independent-read detail state is exposed. |
| A09 — technical analysis opening a swapped path | Analysis safely opens the source relative to the pinned root, checks the snapshot metadata on that opened object, passes the opened object to the seekable managed ffprobe transport, and checks namespace freshness; prior result is retained on failure. Transient file/ancestor swap, pinned bytes, seek fixture, stale/apply/cancel and cleanup tests cover the relevant paths across native CI. | PASS; the implementation does not rely on pre/post pathname `Lstat` as the read boundary. |

Test names above are representative focus points, not a claim that each listed
suite exhausts all interleavings. Detailed per-platform transport cases, actual
manifest identities, and explicit Windows caveats are in the source-I/O
evidence report.

## Acceptance and gates

- **Independent full gate:** `task verify` exited 0 at the reviewed revision.
  Retained output: `/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/step12-independent-a42f09f-verify.log`. The Go
  test output used Go's test cache where applicable; this is reported honestly
  and is not described as a cache-free rebuild. The gate covers generation/drift checks,
  formatting/lint, Go integration tests, frontend tests/typecheck/build and
  project build tasks as defined by `task verify`.
- **Native CI:** [run 37153819171](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/37153819171)
  at the same revision — every job succeeded, including verify, Compose, all
  five native platform targets, and publish. Native managed ffprobe transport
  exercised the approved real tools and seek-dependent media. Windows junction
  pinning/rejection tests passed without skip. The Windows ancestor namespace
  attempt returned `ERROR_ACCESS_DENIED`, left the namespace unchanged, and
  passed; this is not an actual successful ancestor-swap observation.
- **Manual end-to-end:** actual macOS managed ffmpeg/fpcalc lifecycle, Setup,
  source creation, real WAV scan and analysis, repeat analysis, controlled
  analysis failure preserving the prior result, source deletion returning 204
  then 404, and source-byte hash unchanged. Two simultaneous Setup GETs were
  healthy, two concurrent completion calls returned 204, and the saved-path
  validation call returned 200. The validation request omitted candidate path
  fields, so it did not manually check empty or explicit candidate paths.
  Full details and cleanup verification are in the linked acceptance record.

## Limits / blockers

No remaining blocker within the accepted scope. At that time, the implementation
excluded Windows UNC/SMB: create/edit/revalidation reject before filesystem access
and scan/analysis refuse with an actionable unsupported-path error; existing
stored roots remain readable. The subsequent owner decision makes clear this is
not a permanent product ban; the current behavior is not UNC support or a
safe-SMB promise. The gate
and CI do not prove immutable source bytes, arbitrary mount topology behavior,
or every conceivable race. The real manual validation request omitted candidate
path fields as noted above. No independent archive-byte rehash was performed;
retained manifests record catalog-published SHA-256 values verified during CI
provisioning. These are explicit limitations, not blockers for completion.
