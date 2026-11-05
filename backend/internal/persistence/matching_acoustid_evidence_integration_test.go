//go:build integration

package persistence_test

// This PostgreSQL-backed check verifies that the lookup identity is derived
// from the selected persisted fingerprint provenance, not merely the work ID.

func acoustIDEvidenceStringPtr(value string) *string { return &value }
