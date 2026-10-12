//go:build integration

package persistence_test

import "testing"

// This exercises continuation against a real exhausted finite fallback flow.
// Keep this separate from the authority-only seal tests: Continue must consume
// the saved wait and register exactly one evaluation wake, not merely build a
// seal. The shared flow fixture also verifies stale attempts/revisions and
// transactional rollback on handoff insertion failure before the valid retry.
func TestAutomaticFallbackExhaustedContinuationRegistersOneWakePostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowCalculatesAcceptedStageOneReceiptsPostgreSQL(t, false, false, true, true, false)
}
