//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestCheckPublicationSourceHoldForDeliveryRequiresLiveAnchoredHoldPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPublicationExecutionFixture(t)
	_, rootID, variantID, operationID := pubSourceProtectionHoldWithFixture(t, f)

	mtime := time.Now().UTC().Truncate(time.Microsecond)
	sourcePath, err := os.MkdirTemp(t.TempDir(), "source-")
	if err != nil {
		t.Fatal(err)
	}
	// Make the source hold correspond to a canonical variant and explicitly
	// selected track source. Use returned fixture identifiers rather than relying
	// on unrelated source IDs.
	var locationID, workID uuid.UUID
	if err := f.db.NewRaw(`SELECT source_location_id, work_id FROM publication_source_hold WHERE operation_id=?`, operationID).Scan(ctx, &locationID, &workID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, workID, variantID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,metadata) SELECT ?,track_id,?,true,true,'manual','{}'::jsonb FROM release_track WHERE id=?`, uuid.New(), variantID, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	probeID := uuid.New()
	probeVersion := "ffprobe 1"
	policyVersion := 1
	inspectedAt := mtime
	probeOperationID := operationID
	probeJSON := json.RawMessage(`{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2}]}`)
	if _, err := f.db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count) VALUES (?,?,?, ?,?::jsonb,'{}'::jsonb,?,?,1)`, probeID, 0, policyVersion, probeVersion, string(probeJSON), inspectedAt, probeOperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state,success_probe_variant_id,success_reuse_origin)
		VALUES (?,'probe','succeeded',?,'executed')`, workID, probeID); err != nil {
		t.Fatal(err)
	}
	for _, update := range []struct {
		query string
		args  []any
	}{
		{`UPDATE source_root SET configured_path=?,inventory_path=?,enabled=true,status='available' WHERE id=?`, []any{sourcePath, sourcePath, rootID}},
		{`UPDATE source_analysis_work SET configured_path=?,inventory_path=? WHERE id=?`, []any{sourcePath, sourcePath, workID}},
		{`UPDATE publication_source_hold SET configured_path=?,inventory_path=? WHERE operation_id=?`, []any{sourcePath, sourcePath, operationID}},
	} {
		if _, err := f.db.ExecContext(ctx, update.query, update.args...); err != nil {
			t.Fatal(err)
		}
	}
	var root SourceRoot
	if err := f.db.NewSelect().Model(&root).Where("id = ?", rootID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if !root.Enabled || root.Status != SourceRootStatusAvailable || root.Stale() || root.InventoryPath == nil || *root.InventoryPath != sourcePath || root.ConfiguredPath != sourcePath {
		t.Fatalf("source root is not a live, non-stale fixture: %+v", root)
	}

	var frozen PublicationInputs
	frozen = PublicationInputs{
		Version: PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion, IntentKind: "audio", InitialLyrics: &PublicationInitialLyricsInput{ExpectedMetadataRevision: 0}, DesiredTags: map[string][]string{},
		Format: "source", Tools: []SourceAnalysisToolSelection{
			{PackageKind: "ffmpeg", InstallationID: uuid.New(), Executable: "ffmpeg", RelativePath: "bin/ffmpeg", Version: "1", VersionBanner: "ffmpeg version 1"},
			{PackageKind: "ffmpeg", InstallationID: uuid.New(), Executable: "ffprobe", RelativePath: "bin/ffprobe", Version: "1", VersionBanner: "ffprobe version 1"},
		},
	}
	// Frozen selectors and factual stream evidence are required by strict decoding.
	install := frozen.Tools[0].InstallationID
	frozen.Tools[1].InstallationID = install
	lossless := true
	channels := 2
	frozen.Audio = &PublicationAudioInput{WorkID: workID, RootID: rootID, LocationID: locationID, MediaVariantID: variantID, ProbeResultID: probeID,
		ConfiguredRootPath: sourcePath, InventoryRootPath: sourcePath, RelativePath: "track.flac", SizeBytes: 0, MTime: mtime,
		AnalysisPolicyVersion: 1, FFProbeVersion: "ffprobe 1", AudioFacts: PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Channels: &channels, Container: "flac", SupportedSourceContainer: true}}
	encoded, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=?::jsonb WHERE operation_id=?`, string(encoded), operationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE source_location SET mtime=? WHERE id=?`, mtime, locationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE source_analysis_work SET mtime=? WHERE id=?`, mtime, workID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE publication_source_hold SET mtime=? WHERE operation_id=?`, mtime, operationID); err != nil {
		t.Fatal(err)
	}
	var sourceLocation SourceLocation
	if err := f.db.NewSelect().Model(&sourceLocation).Where("id = ?", locationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if sourceLocation.SizeBytes != frozen.Audio.SizeBytes || !sourceLocation.Mtime.Equal(frozen.Audio.MTime) {
		t.Fatalf("source location stat = %d bytes at %s, want %d bytes at %s", sourceLocation.SizeBytes, sourceLocation.Mtime, frozen.Audio.SizeBytes, frozen.Audio.MTime)
	}

	claim, err := NewSetupManagerRepository(f.db).ClaimPublicationDelivery(ctx, f.guard)
	if err != nil || claim != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", claim, err)
	}
	var holdUUID uuid.UUID
	if err := f.db.NewRaw(`SELECT hold_uuid FROM publication_source_hold WHERE operation_id=?`, operationID).Scan(ctx, &holdUUID); err != nil {
		t.Fatal(err)
	}
	repo := NewSetupManagerRepository(f.db)
	got, ok, err := repo.CheckPublicationSourceHoldForDelivery(ctx, f.guard, holdUUID)
	if err != nil || !ok || got.HoldUUID != holdUUID {
		t.Fatalf("valid hold = %+v, %t, %v", got, ok, err)
	}
	if _, ok, err := repo.CheckPublicationSourceHoldForDelivery(ctx, f.guard, uuid.New()); err != nil || ok {
		t.Fatalf("wrong hold UUID valid=%t, err=%v", ok, err)
	}
	var holdCount int
	countHolds := func() int {
		t.Helper()
		if err := f.db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, operationID).Scan(ctx, &holdCount); err != nil {
			t.Fatal(err)
		}
		return holdCount
	}

	// Each changed durable fence makes the existing hold stale, without
	// consuming or deleting it. Restore the original value before the next case.
	mutations := []struct {
		name   string
		mutate func(*PublicationDeliveryGuard)
	}{
		{"operation", func(guard *PublicationDeliveryGuard) { guard.OperationID = uuid.New() }},
		{"application attempt", func(guard *PublicationDeliveryGuard) { guard.ApplicationAttempt++ }},
		{"river job", func(guard *PublicationDeliveryGuard) { guard.RiverJobID++ }},
		{"boot", func(guard *PublicationDeliveryGuard) { guard.BootUUID = uuid.New() }},
		{"delivery token", func(guard *PublicationDeliveryGuard) { guard.DeliveryToken = uuid.NewString() }},
		{"attempt epoch", func(guard *PublicationDeliveryGuard) { guard.Epoch = uuid.New() }},
		{"position", func(guard *PublicationDeliveryGuard) { guard.ExpectedPositionID = uuid.New() }},
		{"river attempt", func(guard *PublicationDeliveryGuard) { guard.RiverAttempt++ }},
		{"claim", func(guard *PublicationDeliveryGuard) { guard.ClaimUUID = uuid.New() }},
		{"candidate generation", func(guard *PublicationDeliveryGuard) { guard.CandidateGeneration = uuid.New() }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			guard := f.guard
			mutation.mutate(&guard)
			_, valid, err := repo.CheckPublicationSourceHoldForDelivery(ctx, guard, holdUUID)
			if err != nil || valid {
				t.Fatalf("mutated fence valid=%t, err=%v", valid, err)
			}
			if count := countHolds(); count != 1 {
				t.Fatalf("hold row count = %d, want 1", count)
			}
		})
	}
	if changed, err := repo.MarkPublicationQuiescent(ctx, f.guard); err != nil || !changed {
		t.Fatalf("mark quiescent = %t, %v", changed, err)
	}
	if _, valid, err := repo.CheckPublicationSourceHoldForDelivery(ctx, f.guard, holdUUID); err != nil || valid {
		t.Fatalf("quiescent claim valid=%t, err=%v", valid, err)
	}
	if count := countHolds(); count != 1 {
		t.Fatalf("hold row count after quiescence = %d, want 1", count)
	}
	if _, valid, err := repo.CheckPublicationSourceHoldForDelivery(ctx, f.guard, uuid.New()); err != nil || valid {
		t.Fatalf("missing hold valid=%t, err=%v", valid, err)
	}
	if count := countHolds(); count != 1 {
		t.Fatalf("hold row count after missing lookup = %d, want 1", count)
	}
}
