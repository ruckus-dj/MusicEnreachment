//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// This PostgreSQL-backed check verifies that the lookup identity is derived
// from the selected persisted fingerprint provenance, not merely the work ID.
func TestAcoustIDRequestIdentityFollowsCurrentFingerprintWinnerPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixtureWithSelectedFingerprint(t, ctx, db)
	captures, err := persistence.NewIncomingGroupingRepository(db).CurrentCaptures(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var capture *persistence.IncomingGroupingCapture
	for i := range captures {
		if captures[i].WorkID == fixture.workA {
			capture = &captures[i]
			break
		}
	}
	if capture == nil || capture.FingerprintResultID == nil || capture.FingerprintWinner == nil || capture.Fingerprint == nil {
		t.Fatalf("fixture has no selected fingerprint provenance: %#v", capture)
	}
	first, err := persistence.AcoustIDRequestIdentityForCapture(*capture)
	if err != nil {
		t.Fatalf("derive AcoustID request identity: %v", err)
	}
	firstKey, err := persistence.AcoustIDRequestCacheKey(first)
	if err != nil {
		t.Fatalf("derive AcoustID request cache key: %v", err)
	}
	changed := *capture
	changed.Fingerprint = acoustIDEvidenceStringPtr("different-current-fingerprint")
	second, err := persistence.AcoustIDRequestIdentityForCapture(changed)
	if err != nil {
		t.Fatalf("derive changed AcoustID request identity: %v", err)
	}
	secondKey, err := persistence.AcoustIDRequestCacheKey(second)
	if err != nil {
		t.Fatalf("derive changed AcoustID request cache key: %v", err)
	}
	if firstKey == secondKey {
		t.Fatalf("different selected fingerprints shared cache key %q", firstKey)
	}
	changed = *capture
	changed.WorkID = uuid.New()
	third, err := persistence.AcoustIDRequestIdentityForCapture(changed)
	if err != nil {
		t.Fatalf("derive changed-work AcoustID request identity: %v", err)
	}
	thirdKey, err := persistence.AcoustIDRequestCacheKey(third)
	if err != nil {
		t.Fatalf("derive changed-work AcoustID request cache key: %v", err)
	}
	if firstKey == thirdKey {
		t.Fatalf("different works shared cache key %q", firstKey)
	}
}

func acoustIDEvidenceStringPtr(value string) *string { return &value }
