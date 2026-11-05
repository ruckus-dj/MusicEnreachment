//go:build integration

package service_test

// TestScanRetryThroughTheOperationsServiceWithPostgreSQL drives the retry the
// API reaches: the production Operations service is built over the repository
// that owns the operation table and a real River client, so the scan retry has
// to be discovered there, drop the candidates of the failed attempt and be
// delivered under the scan job kind instead of the generic operation kind.
