# backend/internal/service

**Score: 13** (distinct domain: 5 files, module boundary, code-heavy)

## OVERVIEW
Business logic layer. SetupService (first-run wizard) and Operations (state machine for long-running async work). Consumed by api handlers and River jobs.

## WHERE TO LOOK
- setup.go - SetupService: State/SaveRuntime/Complete for first-run setup wizard
- operations.go (189 lines) - Operations state machine: Start/Get/Progress/Succeed/Fail/Retry/Dismiss/Cleanup/Subscribe/notify (13 methods, 57+ references)
- installations.go - Installations service (tool installation management)
- operations_test.go, setup_test.go - unit tests

## STRUCTURE
Three main services:
- SetupService: first-run wizard state and persistence
- Operations: async work state machine with SSE notifications
- Installations: tool installation lifecycle

All services depend on persistence layer repos (persistence.SetupManagerRepository) and settings.Registry.

## CONVENTIONS
**Operation state machine**: Start -> Running -> (Succeeded|Failed). Retry resets to Running. Dismiss marks user-acknowledged. Cleanup removes completed/dismissed.

**Durable worker args**: River job args carry only operation ID; workers always reload inputs from snapshot via Operations.Get.

**SSE wake-up only**: Operations.Subscribe provides SSE channel for errors/reconnect, never data-bearing events. Client re-reads snapshot after wake.

**Safe errors required**: Operations.Fail errors out if safe error message is empty (user-facing failures must have safe text).

**Error handling**: `fmt.Errorf("context: %w", err)` with lowercase messages; no custom error types.

## ANTI-PATTERNS
- Empty safe error messages in Operations.Fail (enforced at runtime)
- Passing full operation inputs in River job args (args = ID only, reload from snapshot)
- Using SSE as data channel (it's wake-up only)

## NOTES
Operations.Progress allows partial updates (e.g., download progress) without changing state. Subscribe returns a channel that closes on operation completion or error, never sends intermediate data.
