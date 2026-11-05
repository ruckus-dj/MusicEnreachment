//go:build integration

package persistence_test

// This regression exercises the real v2 automatic-admission and delivery
// fences. The accepted-catalog seal is intentionally not fabricated here:
// the publisher's sealed-scope check is covered by the sealed-worker
// integration test, while this test targets the target-capture handoff.
