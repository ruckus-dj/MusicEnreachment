//go:build integration

package persistence_test

// This covers the production worker boundary: a durable apply intent is claimed,
// materialized and receipted by Work, rather than applying through the candidate
// repository's direct convenience method.
