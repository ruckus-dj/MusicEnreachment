# Plan 08 acceptance — final browser/runtime evidence (2026-10-08)

**Status: ACCEPTED in the approved Plan 08 scope.** Independent reconciliation:
[review report](plan08-independent-review-2026-10-08.md). Historical interim
limitations below are superseded only by the explicitly dated final addenda.

This report records final browser/runtime evidence required by
[Plan 08, section «Свидетельства»](../plans/done/08-settings-and-setup-design-corrections.md).
The initial sections below preserve the historically accurate interim record. The
final revision, reported gate and completed browser/runtime checks are recorded in
the dated final-results addendum. This is not the independent review; do not infer
that it has been completed.

Do not read this draft as a product contract or as new requirements. It does not
reinterpret `docs/design/`; owner decisions there remain authoritative.

## Provenance and scope (interim)

- Docs/status revision at time of drafting: `591952db4eaf435707b8fb22a7b522fe1c29bc72`
  (`docs(plan08): mark corrections implementation in progress`).
- Reported gate: `task verify` passed at `591952d`. Reported by the primary session;
  **not re-run while writing this draft** (no tests/build/lint were executed here).
- Reported hook evidence: every listed commit ran the pre-commit hook as an isolated
  staged check and passed. Reported, not independently re-executed here.
- Reported interim test count: **182 unit/RTL tests**. This is an interim figure for
  the current state; it is not a final gate count.
- Working tree observation: at drafting time the worktree is **not clean** — several
  uncommitted modifications are present (frontend `SettingsScreen.tsx` /
  `SettingsScreen.test.tsx` and backend `api/tools.go`, `persistence/settings.go`,
  `persistence/setup_manager.go`, `persistence/source_analysis_coordination.go`),
  and `test_stand/` is untracked. The exact set is a moving target because work is
  ongoing in parallel; none of these uncommitted changes is part of the verified
  `591952d` revision and none is attributed to a C-item here. **PENDING:** fix the
  final revision only once the worktree is clean and all C-items are integrated.

### Revision table (committed C-items)

| Item | Commit | Subject |
| --- | --- | --- |
| C01 | `7fad509a754573d61f90ab79e791f33a148aa575` | `fix(settings): bind move confirmation to checked inputs` |
| C02 | `a80000d45e6b9d45bb254c5ae0012d95dfe56037` | `fix(settings): keep dialog errors and focus inside modals` |
| C05 | `0becf4c415d08d713fc5d22f1bc5e10bb124732b` | `fix(settings): theme the surrounding application shell` |
| C06 | `6a981d5c219ba24b7a84fd3ce954568c7769a843` | `fix(settings): serialize tools and output root mutations` |
| C08 | `0202ee75d449a1a18aff7d233a78dda1535801bf` | `docs: correct detached application stop instructions` |

C03, C04, C07 have **no** commit and are **not implemented/verified** at this
interim. Historical reports and audits are not rewritten by this draft.

## C01–C08 matrix (interim)

Legend: **IMPL** = committed implementation exists at the revision above;
**TESTS/STATIC** = committed automated/static evidence exists; **MANUAL** = manual /
browser / runtime acceptance — all PENDING at this interim.

| ID | Area | Status | TESTS/STATIC (committed) | MANUAL |
| --- | --- | --- | --- | --- |
| C01 | Settings move dialog bound to checked inputs | IMPL (`7fad509`) | 2 RTL tests (names below) | PENDING (real move with checkbox change after preflight) |
| C02 | Move/download dialog error + focus | IMPL (`a80000d`) | 7 RTL tests (names below) | PENDING (real browser keyboard / native inert) |
| C03 | Settings drafts survive refresh/save | NOT IMPL | none | PENDING |
| C04 | Settings initial-load retry | NOT IMPL | none | PENDING |
| C05 | Settings + AppShell theme tokens | IMPL (`0becf4c`) | 4 RTL tests + CSS (names below) | PENDING (light/dark screenshots 1440/375) |
| C06 | Serialize tools/output root mutations | IMPL (`6a981d5`) | 2 new PostgreSQL integration tests (names below) | PENDING (concurrency/runtime settlement) |
| C07 | Single successful package version in initial Setup | NOT IMPL | none | PENDING |
| C08 | README stop instruction | IMPL (`0202ee7`) | static doc change | PENDING (verify against `Taskfile.yml:run/stop`) |

## Quoted committed test names (via codegraph)

Test names are quoted verbatim from the committed test files at the revisions
above. They are static/unit evidence only and do not substitute for the pending
manual/browser/runtime acceptance.

### C01 — `frontend/src/features/settings/SettingsScreen.test.tsx` (added by `7fad509`)

- `invalidates a move token on every input edit, including removeOld true to false and reverting the path`
- `ignores move preflight responses from edited inputs, older requests, and closed dialog sessions`

### C02 — `frontend/src/features/settings/SettingsScreen.test.tsx` (added by `a80000d`)

- `keeps an installation preflight refusal on the screen and focuses its screen alert`
- `shows move preflight errors and retries inside the move dialog`
- `keeps a rejected download in its dialog, focuses it, and permits retry`
- `invalidates a refused move token, leaves inputs editable, and retries with a fresh token`
- `ignores late install and move start refusals after closing and reopening their dialogs`
- `registers late successful install starts without closing a newer dialog or clearing its busy state`
- `registers late successful move starts without closing a newer dialog or clearing its busy state`

### C05 — `frontend/src/routes/AppShell.test.tsx` (added by `0becf4c`)

- `uses the themed Settings shell for the settings route`
- `uses the themed Settings shell for the completed landing route`
- `keeps the setup shell for an incomplete instance`
- `keeps the sources shell for the sources route`

### C06 — `backend/internal/persistence/tools_root_race_integration_test.go` (added by `6a981d5`)

- `TestRuntimeRootsRespectActiveMoveAndKeepNonconflictingOutputUpdatesPostgreSQL`
- `TestGenericSettingsWritesSerializeActiveSelectionsAndCanonicalizeRootsPostgreSQL`

Pre-existing, still relevant to the same invariant (not added by `6a981d5`):

- `TestToolsRootUpdateAndInstallEnqueueSerializeWithPostgreSQL`

## C06 lock-order short map (interim record)

Recorded short map for the runtime-settings / tools-move mutation paths, to keep the
single tools-move gate rather than a second lock system:

**G → C (when needed) → stable packages → MB → installation → operation → settings**

- **G** — the existing tools-move gate (`lockToolsMoveGateExclusive` /
  `lockToolsMoveGateShared`), taken exclusive for runtime root changes and shared for
  package-selection writes.
- **C (when needed)** — setup-completion coordination (`lockSetupCompletion`) only on
  the paths that need it.
- **stable packages** — the package-activation lock set
  (`lockInstallationPackages` / `lockPackageActivations`).
- **MB** — MusicBrainz configuration advisory lock (`lockMusicBrainzIfNeeded`).
- **installation / operation** — row-level checks over tool installations and active
  tools operations.
- **settings** — the final sorted settings write within the same transaction.

No filesystem probes are moved inside the long DB transaction; the gate and short DB
transactions are not held across download/copy. This is a short map recorded for the
draft; **PENDING:** confirm against the final C06 diff and independent review.

## Runtime bootstrap (interim, separate)

An interim runtime bootstrap exists at `591952d`. It is **separate** and **not
final**; the final runtime acceptance is forthcoming. The copied native binary
has SHA-256 `97f7000012338b0263d02011c5e336fdb9f434dad9d77122624f13b07335c5c1`.
The isolated macOS arm64 stand downloaded FFmpeg/ffprobe 9.0.2 from Martin Riedl
and fpcalc 1.6.1 from Chromaprint, verified their executable versions and completed
Setup with a successful real MusicBrainz connection check. Catalog responses
reported checksum metadata for these artifacts; checksum bytes are not exposed
by the API and are not invented here. This bootstrap does not establish C07 on
the forthcoming final revision.

Interim evidence: `bootstrap-evidence.json`, `http.jsonl`, and `manifest.json` in
`/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan08-runtime-20261008`.
No move acceptance was performed during this bootstrap.

**PENDING — final runtime:** platform, managed-tool versions and binary-vs-revision
correspondence; real move with checkbox change after preflight and fresh re-check;
successful move without removing prior files; error inside modal; dirty output
preserved across tool-operation completion; initial-load retry; Settings both themes
and sizes.

## Pending placeholders at interim drafting time (superseded by final addendum where noted)

- **PENDING — final revision:** fix the verified revision once the worktree is clean
  and all C-items are integrated; record its commit hash here.
- **PENDING — final gate:** `task verify` result on that final revision (exit code and
  log path), plus the final test count replacing the interim 182.
- **PENDING — browser screenshots:** Settings light/dark at 1440 and 375 px, keyboard
  traversal and dialogs, long-path wrapping and no horizontal overflow. Screenshots
  are not a full WCAG certification.
- **PENDING — native platform limitations:** macOS-only pass does not prove the CI /
  runtime matrix; other OS/architecture combinations and native dialog/inert behavior
  remain unproven.
- **PENDING — independent review:** separate
  `docs/reports/plan08-independent-review-<date>.md` matching criteria, code/tests and
  manual evidence. This draft is not that review.
- **PENDING — C03/C04/C07:** implementation, tests and verification are outstanding.
- **PENDING — C01/C02/C05/C06/C08 manual acceptance:** the manual/browser/runtime
  rows above are not yet performed.

## Limits of this draft

- Static/committed evidence only for C01/C02/C05/C06/C08; no browser, runtime or
  concurrency acceptance was performed here.
- No `task verify`, tests, build or lint were run while producing this draft; the gate
  and hook results are reported by the primary session.
- This draft does not rewrite historical dated reports or audits, and does not move
  Plan 08 to `done/`.

## Final results addendum — 2026-10-08

### Revision, gate provenance, and immutable runtime

- Final HEAD: `a70ac2b04a38245d24cb455281b8b67b2ca894a8` (C03+C04); C07 commit
  `7d481dc`. The tracked tree was reported clean by the owner; this report and
  `test_stand/` were untracked and not touched here.
- Owner reports both mandatory pre-commit `task verify` gates passed. No gate was
  re-run in this acceptance session (per instruction).
- Final server binary copied to
  `/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan08-runtime-20261008/bin/server`,
  SHA-256 `d91985ada3dcd5cc66ba38bc1d5f7e9b29bedb4970ba4f74c68d9064e264065e`;
  this matched the owner-provided build hash before launch. Runtime manifest and
  logs are alongside it. Isolated PostgreSQL 17 container
  `plan08-runtime-20261008-pg`, DB `plan08_accept_final_20261008`, port 32770;
  app ran on macOS arm64 at `127.0.0.1:18088`.
- Real catalog/download/verification/Setup completed on this binary: FFmpeg/ffprobe
  9.0.2 (Martin Riedl) and fpcalc 1.6.1 (Chromaprint); successful public
  MusicBrainz check and completed Setup. Detailed observed version banners and API
  records: `bootstrap-evidence.json`; request/response log: `http.jsonl` in the
  isolated runtime directory. Catalog checksum availability is recorded as
  metadata; checksum bytes are not exposed by API and are not claimed here.

### Browser acceptance — Chrome, final frontend build

Harness: `/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan08-browser-20261008/browser-acceptance.mjs`,
run against the current Vite frontend at `http://127.0.0.1:4173`; evidence directory:
`/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan08-browser-20261008/final`.
This harness intentionally mocks HTTP/SSE and is UX evidence, not backend evidence.

Passed: first Settings GET 503 followed by explicit retry; `removeOld=true → false`
invalidates old confirmation and requires fresh preflight/token; exact token/conflict
submission; native `dialog:modal`, focus containment when attempting outside focus,
Escape close and focus return; move-preflight refusal displayed/focused in modal and
successful retry; dirty output preserved across mocked operation completion and
settings refresh; incomplete-Setup smoke (no manual activation offered); Sources
route smoke. Settings light/dark at 1440/375 had no horizontal overflow and sampled
canvas/panel/input/button colors were legible and theme-appropriate. Screenshots:
`settings-{1440,375}-{light,dark}.png`; detailed actions/calls:
`browser-actions.txt`.

Install-start modal refusal was not completed in the browser harness. Keyboard Tab
cycling was not asserted after its first probe failed the harness assertion; this
was not diagnosed as a product defect. The run had harness issues that were fixed:
Vite's `/src/api/*` module was initially intercepted by an overly broad route, and
the retry button locator incorrectly searched inside the alert paragraph. A later
install-modal refusal locator timed out, so that subcheck was removed; move-modal
error coverage passed. These are disclosed gaps, not passing evidence.

### Real runtime move and dirty-draft evidence

- Real API preflights were issued for the same target with `remove_old_files=true`
  and then `false`; distinct tokens were returned. The fresh false-token was started.
  Operation `f6fa5305-2b01-4b09-925b-f5a70d61655a` succeeded (`switched`); all 3
  managed executable files remained in the old root and target SHA-256 values
  matched. Exact records: `final-move-evidence.json`.
- A subsequent real-browser move from the current root to `moved-tools-final` also
  succeeded (operation `f2edc76e-b420-4e8e-b329-3f371b1a658f`, corrected against live
  `GET http://127.0.0.1:18088/api/operations`). While it was
  running, the browser held unsaved Output directory draft
  `/tmp/plan08-real-dirty-output`; after settlement the Settings screenshot showed
  that value still present while the server snapshot showed the new tools root.
  Screenshot: `real-move-dirty-output.png`. The screenshot and matching succeeded
  operation record jointly evidence draft preservation across real completion.
- The standalone real-browser driver ultimately exited nonzero because it queried
  an accessibility group locator that did not match the operation row after the
  browser had completed the move. The persisted server operation record is
  `succeeded`, the screenshot shows the updated root and unsaved draft, and this
  locator failure is not represented as a product failure. A later attempt to
  preflight the already-moved destination was refused; no mutation occurred.

### Acceptance limits / outstanding independent review

This provides final macOS arm64 runtime evidence and browser UX evidence, not a
cross-platform/native-platform matrix or full WCAG certification. Deterministic
mocked concurrency is not a substitute for PostgreSQL integration tests; those are
covered by the committed tests and the owner-reported verify gate, not manually
replayed here. Install-start modal refusal and full Tab-loop behavior remain untested
in this browser run. Independent review is still required before representing Plan
08 as fully accepted/COMPLETE or moving it to `done/`.

### Supplementary two-check browser attempt — 2026-10-08

The omissions were revisited in Chrome using a separate narrow Playwright script:
`/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan08-browser-20261008/focused-two-checks.mjs`.
Neither check completed. Its mock fixture left the install action disabled because
it did not provide a selectable release; the script timed out before opening the
confirmation dialog. This is a harness/fixture failure, not evidence of a product
defect or a pass. No Tab-loop assertions ran and no evidence JSON was generated.
Both checks remain outstanding pending a corrected fixture and rerun.

### Supplemental final-binary acceptance — 2026-10-08 (supersedes earlier interim hash/runtime details)

This addendum retains the prior historical runs above, but replaces the earlier
interim final-binary provenance and fills the install-start and keyboard checks.
No production source or tests were changed for these checks.

- Final gate evidence is `/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan08-final-clean-gate.log` (`task verify`, exit 0); Vitest reports **195 passed** (16 files). This gate was already run by the owning session; it was not rerun here. The exact built artifact is Go 1.27.1, `darwin/arm64`, VCS revision
  `a70ac2b04a38245d24cb455281b8b67b2ca894a8`, `vcs.modified=true`.
- Important provenance correction: the earlier interim runtime copy had hash
  `d91985ada3dcd5cc66ba38bc1d5f7e9b29bedb4970ba4f74c68d9064e264065e`. The
  final-gate artifact `build/backend/server` instead hashes to
  `0046901c2e5b2fda3389155ae3de731dde784769a2fc5ca1d1ef1b38ac3c6325`.
  That latter exact artifact was copied to the owned runtime's `bin/server` and
  verified before start; `go version -m` confirms the matching VCS revision and
  `vcs.modified=true`. The discrepancy is artifact provenance (old interim copy
  versus final-gate build), not a hash collision. No claim is made that the VCS
  revision alone guarantees byte-for-byte identity.
- Tracked worktree diff is empty. At this point `git status --short` showed only
  untracked acceptance/review docs and `test_stand/`; none were modified by this
  production-code acceptance work, except the reports updated to record results.
  No `test_stand` content or unrelated containers were touched.
- The owned server process was verified by PID and executable path before stopping
  the old interim process, then restarted at `127.0.0.1:18088` as PID `60513` from
  the immutable verified `bin/server`. It connected to the owned PostgreSQL 17
  container `plan08-runtime-20261008-pg`, host port `32770`, DB
  `plan08_accept_final_20261008`, user `plan08`. The DB identity was queried
  directly; startup migrations and `/api/setup` returned successfully. Full
  manifest/process metadata is in
  `/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan08-runtime-20261008/manifest.json`.

#### Corrected focused mocked-browser checks

The fixed script is
`/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan08-browser-20261008/focused-two-checks.mjs`.
It explicitly selects `release-1` via `getByLabel('Версия ffmpeg')` before
installing, matches POST before the GET installations mock, and uses the actual
confirm action name `Подтвердить перечисленные конфликты` from `SettingsScreen`.
The first install start returned the mocked 409; the alert text
`Install start refused by mock` was the active element inside `dialog:modal`. A
second confirmed start succeeded, closed the dialog, and the harness recorded
2 start requests. Evidence: the script's `focused-two-checks/evidence.json`.

Tab traversal was tested from both controls in Chrome. Forward Tab reached
`document.body` at the native modal boundary once; the next Tab returned focus to
the first modal button. Reverse Shift+Tab cycled between the two modal buttons.
At all steps the dialog remained `:modal`; the only non-contained active element
was BODY (not an interactive outside control), and it re-entered the dialog on
the next forward step. This reproduces a Chrome native-dialog boundary focus
transition; it did not demonstrate focus on an outside interactive element. No
production change was made. Browser-specific focus behavior beyond this Chrome
run remains unverified.

#### Real final-runtime move with checkbox reversal and dirty draft

A fresh real-browser run used a unique destination under the owned temp stand:
`.../moved-tools-gate-final-0046901c-20261008`. The real API recorded a first
preflight with `remove_old_files=true`, changing the checkbox to false removed
its old confirmation, and a second preflight with `remove_old_files=false` was
issued. The move used the fresh second token. The browser driver then fetched the
actual operation snapshot by the ID returned from the move-start HTTP response
(no guessed operation-row locator); operation
`55d0fcc9-59e0-433f-922d-4d8f00596e33` reached `succeeded` / `switched`, and the
server settings reported the unique target as current. The unsaved Output
Directory draft remained
`.../dirty-output-preserved-20261008` after refresh. Evidence JSON and screenshot:
`/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan08-runtime-20261008/real-browser-move-gate-evidence.json` and
`real-move-gate-dirty-output.png`.

The first version of this rerun incorrectly selected an older succeeded move from
`GET /api/operations`; its assertion failed (and its start was refused). That
harness defect is corrected: final evidence uses the ID from the actual start
response and independently checks the server settings root and dirty draft. The
refused attempt is not counted as a pass or product defect.

These results close the previously missing narrow checks only. They do not
replace the stated cross-platform/native-platform and independent-review limits;
Plan 08 should not be represented as fully accepted/COMPLETE on this evidence
alone.

### Final retained-files verification

After the final-gate move `55d0fcc9-59e0-433f-922d-4d8f00596e33`, the primary
session independently compared all three executable files in `moved-tools-final`
and `moved-tools-gate-final-0046901c-20261008`: old files still exist and each
SHA-256 equals its target copy. Evidence: runtime directory
`final-gate-retained-files.json`. Thus no-removal is verified on the new
final-gate binary too, not only on the earlier runtime artifact.

### Final acceptance decision

Independent static review and supplemental evidence reconciliation found no
remaining blockers for C01–C08; Plan 08 is accepted within its approved scope.
C03+C04 were committed together (`a70ac2b`) because their validated changes were
intertwined; C07 is `7d481dc`. Earlier pending paragraphs remain historical,
not the final status. Chrome BODY boundary navigation, `vcs.modified=true` from
untracked files, macOS-only runtime and non-certification of WCAG remain disclosed
limitations, not claims of broader validation. The new binary reused the already
verified managed tools/Setup DB; downloads were not repeated on that last artifact.
