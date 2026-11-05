//go:build integration

package persistence_test

// This fixture tests the manual reader continuation boundary, not automatic
// evaluation publication. It uses a real automatic v2 admission/capture and
// seeds the existing legacy evaluation row with that capture's exact authority.
