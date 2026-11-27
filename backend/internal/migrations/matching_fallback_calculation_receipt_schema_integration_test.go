//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// The collapsed baseline records only the current calculation receipt format:
// one schema version plus the canonical input digest. Historical version tuple
// columns were redundant persistence fields and are intentionally absent.
func TestMatchingFallbackCalculationReceiptConstraintIsValidated(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	var definition string
	var validated bool
	if err := db.NewRaw(`SELECT pg_get_constraintdef(oid), convalidated FROM pg_constraint
		WHERE conrelid='public.matching_fallback_calculation_receipt'::regclass
		  AND conname='matching_fallback_calculation_receipt_layout_decision_check'`).
		Scan(ctx, &definition, &validated); err != nil {
		t.Fatalf("read matching fallback calculation receipt constraint: %v", err)
	}
	if !validated {
		t.Fatal("matching_fallback_calculation_receipt_layout_decision_check is not validated; want no NOT VALID on a clean baseline")
	}
	for _, fragment := range []string{"schema_version = 1", "layout_decision", "decision_digest", "pair_threshold"} {
		if !strings.Contains(definition, fragment) {
			t.Fatalf("receipt constraint definition %q is missing %q", definition, fragment)
		}
	}
	for _, column := range []string{"calculator_version", "digest_version"} {
		var count int
		if err := db.NewRaw(`SELECT count(*) FROM information_schema.columns
			WHERE table_schema='public' AND table_name='matching_fallback_calculation_receipt' AND column_name=?`, column).
			Scan(ctx, &count); err != nil {
			t.Fatalf("read matching fallback calculation receipt column %q: %v", column, err)
		}
		if count != 0 {
			t.Fatalf("matching fallback calculation receipt still has redundant column %q", column)
		}
	}
}
