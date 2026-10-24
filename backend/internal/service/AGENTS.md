# backend/internal/service

**Score: 13** (distinct domain: 5 files, module boundary, code-heavy)

## OVERVIEW
Business logic layer. SetupService (first-run wizard) and Operations (state machine for long-running async work). Consumed by api handlers and River jobs.

## WHERE TO LOOK
- setup.go - SetupService: State/SaveRuntimeUpdate/Complete for first-run setup wizard
- operations.go - Operations state machine, retries, cleanup, and SSE wake-ups
- installations.go - Installations service (tool installation management)
- operations_test.go, setup_test.go - unit tests

## STRUCTURE
Core services and workflow areas:
- SetupService: first-run wizard state and persistence
- Operations: async work state machine with SSE notifications
- Installations: tool installation lifecycle
- Source inventory, per-file analysis, staged inputs, and output reset
- Provider execution, incoming grouping, automatic/manual matching, and local library

All services depend on persistence layer repos (persistence.SetupManagerRepository) and settings.Registry.

## CONVENTIONS
**Operation state machine**: Start -> Running -> (Succeeded|Failed). Retry resets to Running. Dismiss marks user-acknowledged. Cleanup removes completed/dismissed.

**Durable worker args**: River job args carry only operation ID; workers always reload inputs from snapshot via Operations.Get.

**SSE wake-up only**: Operations.Subscribe provides SSE channel for errors/reconnect, never data-bearing events. Client re-reads snapshot after wake.

**Safe errors required**: Operations.Fail errors out if safe error message is empty (user-facing failures must have safe text).

**Error handling**: `fmt.Errorf("context: %w", err)` with lowercase messages; no custom error types.

**Publication fault tests**: Claim the delivery before reading guarded preparation
inputs. The live publication contract repeats complete private preparation after
failure, not journal/ACK recovery. Inject a late result-transaction failure after
actual installation, prove the next real River delivery prepares again, and prove
completed redelivery needs neither source nor attempt. A SQL rejection proves
rollback, not a committed result with a lost worker acknowledgement. Do not gate
native atomic replacement on directory-sync support. River shutdown can redeliver
the same attempt number; process-local coordination serializes execution while
the refreshed claim invalidates former guards.
Manual-lyrics fixtures must save the actual desired envelope and matching revision
before freezing inputs, and use `source` format for tag-only rewrites. Establish a
valid baseline before testing stale authority. Native path fixtures canonicalize
temporary roots (`filepath.EvalSymlinks`) before opening no-follow filesystem roots.
When removing a publication directory-sync gate, remove obsolete Windows-only
failure expectations too: native nested publication must reach the same success
assertions on every supported platform. Cover-preservation fixtures must acquire
automatic lyrics explicitly; a saved manual value intentionally suppresses lookup
and does not silently apply itself during first publication.
Native artwork-preservation tests that observe publication assets need real encoded
images (for example tiny JPEG/PNG fixtures), not opaque placeholder bytes: asset
observation derives MIME from content. Probe case equivalence on the actual test
filesystem rather than inferring it from GOOS. Extension-only path tests must use
a supported transform; do not request ALAC without verified capability evidence.
Initial-lyrics lookup uses frozen analysis metadata, never a source/copy read.
Preserve fractional duration from saved ffprobe JSON, not TagLib's millisecond
properties. Admission rechecks metadata freshness; delivery must not invalidate
frozen inputs merely because the canonical metadata row was refreshed later.
Use Bun `?` placeholders in Bun queries, including fixture SQL; `$1` is not
interchangeable. For a scalar JSONB scan, select `::text` into a string rather
than scanning directly into `json.RawMessage` as if it were a scalar driver type.
For lifecycle fixtures, use named scenario configuration rather than extending a
positional boolean list. Assert the intended frozen input kind and observed
provider calls: a green test name alone does not prove that its branch ran.
When injecting metadata races through Bun SQL, pass marshalled JSON as a string
to `?::jsonb`, not a plain `[]byte` (which becomes a bytea/hex literal). Assert
the mutation affected the intended row and revision before claiming rejection:
an error returned from a lookup stub may become a nonfatal provider warning.
Cover-only rewrites must call the native cover-only writer, not the optional-assets
writer that clears and rewrites tags. Preserve published tags/lyrics, not pending
desired metadata. Container-derived bitrate may change with artwork size; verify
encoded payload preservation separately instead of treating that estimate as an
audio mutation. Receipt-carry fixtures must establish the actual desired metadata
revision before asserting generation continuity.

## ANTI-PATTERNS
- Empty safe error messages in Operations.Fail (enforced at runtime)
- Passing full operation inputs in River job args (args = ID only, reload from snapshot)
- Using SSE as data channel (it's wake-up only)

## NOTES
Operations.Progress allows partial updates (e.g., download progress) without changing state. Subscribe returns a channel that closes on operation completion or error, never sends intermediate data.
