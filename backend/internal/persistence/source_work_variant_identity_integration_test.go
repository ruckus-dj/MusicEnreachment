//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
)

func applyIdentityProbe(t *testing.T, fixture *analysisStepFixture, size int64, version string) *persistence.SourceMediaVariant {
	t.Helper()
	attempt := fixture.claim(persistence.SourceStepProbe)
	result, err := fixture.repository.ApplySourceProbe(fixture.ctx, persistence.SourceProbeApply{
		WorkID: fixture.work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
		JobID: *fixture.operation.RiverJobID, StepAttempt: attempt, SizeBytes: size, AnalysisPolicy: persistence.SourceAnalysisPolicyVersion,
		FFProbeVersion: version, FFProbeJSON: json.RawMessage(`{"format":{"format_name":"flac"}}`),
		ObservedTags: json.RawMessage(`{"ARTIST":["fixture"]}`), InspectedAt: time.Now().UTC().Truncate(time.Microsecond), AudioStreamCount: 1,
	})
	if err != nil {
		t.Fatalf("apply identity probe: %v", err)
	}
	return result
}

func applyIdentityMetadata(t *testing.T, fixture *analysisStepFixture) {
	t.Helper()
	attempt := fixture.claim(persistence.SourceStepMetadata)
	_, err := fixture.repository.ApplySourceMetadata(fixture.ctx, persistence.SourceMetadataApply{
		WorkID: fixture.work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
		JobID: *fixture.operation.RiverJobID, StepAttempt: attempt,
		ObservedTags: json.RawMessage(`{"TITLE":["fixture"]}`), Provenance: json.RawMessage(`{"source":"fixture"}`),
		ObservedAt: time.Now().UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("apply identity metadata: %v", err)
	}
}

func applyIdentitySHA(t *testing.T, fixture *analysisStepFixture, digest []byte) *persistence.SourceMediaVariant {
	t.Helper()
	attempt := fixture.claim(persistence.SourceStepSHA256)
	result, err := fixture.repository.ApplySourceSHA256(fixture.ctx, persistence.SourceSHA256Apply{
		WorkID: fixture.work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
		JobID: *fixture.operation.RiverJobID, StepAttempt: attempt, SHA256: digest,
		CalculatedAt: time.Now().UTC().Truncate(time.Microsecond), Algorithm: "SHA-256",
	})
	if err != nil {
		t.Fatalf("apply identity SHA: %v", err)
	}
	return result
}

func sourceWorkVariantID(t *testing.T, ctx context.Context, db *bun.DB, workID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.NewRaw(`SELECT media_variant_id FROM source_work_variant_identity WHERE work_id=?`, workID).Scan(ctx, &id); err != nil {
		t.Fatalf("read work variant identity: %v", err)
	}
	return id
}

func assertIdentityLocation(t *testing.T, ctx context.Context, db *bun.DB, locationID, want uuid.UUID) {
	t.Helper()
	var got *uuid.UUID
	if err := db.NewRaw(`SELECT media_variant_id FROM source_location WHERE id=?`, locationID).Scan(ctx, &got); err != nil || got == nil || *got != want {
		t.Fatalf("location identity = %v, %v; want %s", got, err, want)
	}
}
