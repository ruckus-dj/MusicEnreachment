//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestAutomaticMatchingAnalysisInputCaptureUsesCanonicalSelectedInputsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()

	path := "/srv/automatic-matching-input-fence"
	root := &SourceRoot{
		ID: uuid.New(), ConfiguredPath: path, DisplayName: "automatic matching input fence",
		Enabled: true, ScanGeneration: 1, InventoryPath: &path, Status: SourceRootStatusAvailable,
	}
	if err := NewSourceInventoryRepository(database).CreateSourceRoot(ctx, root); err != nil {
		t.Fatal(err)
	}
	location := SourceLocation{
		ID: uuid.New(), SourceRootID: root.ID, RelativePath: "track.flac", SizeBytes: 12,
		Mtime: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), LastSeenScanGeneration: 1,
		ProbeStatus: SourceProbeStatusAudio,
	}
	if _, err := database.NewInsert().Model(&location).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	workID := uuid.New()
	if _, err := database.NewRaw(`INSERT INTO source_analysis_work
		(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, workID, location.ID, root.ID, path, path, location.RelativePath, location.SizeBytes,
		location.Mtime, true, uuid.New()).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	metadataInput := json.RawMessage(`{"schema_version":1,"selected":{"artist":"first"}}`)
	if _, err := database.NewRaw(`INSERT INTO source_analysis_step(work_id,step,state,input_snapshot)
		VALUES (?,'metadata','pending',?::jsonb)`, workID, string(metadataInput)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.NewRaw(`INSERT INTO source_analysis_step(work_id,step,state,input_snapshot)
		VALUES (?,'fingerprint','pending',?::jsonb)`, workID, `{"schema_version":1,"selected":{"enabled":true}}`).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	load := func() []automaticArtistAnalysisInput {
		t.Helper()
		var result []automaticArtistAnalysisInput
		err := database.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			inputs, err := loadAutomaticArtistAnalysisInputs(ctx, tx, []IncomingGroupingCapture{{WorkID: workID}})
			if err != nil {
				return err
			}
			result = inputs[workID]
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := load()
	if len(first) != 4 {
		t.Fatalf("captured analysis step count = %d, want all four step kinds", len(first))
	}
	byStep := make(map[string]automaticArtistAnalysisInput, len(first))
	for _, input := range first {
		byStep[input.Step] = input
	}
	metadata := byStep["metadata"]
	wantCanonical := `{"schema_version":1,"selected":{"artist":"first"}}`
	if string(metadata.InputSnapshot) != wantCanonical || metadata.InputDigest == nil {
		t.Fatalf("metadata capture = %+v, want canonical snapshot and digest", metadata)
	}
	wantDigest := sha256.Sum256([]byte(wantCanonical))
	if *metadata.InputDigest != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("metadata input digest = %s, want canonical digest %x", *metadata.InputDigest, wantDigest)
	}
	if byStep["sha256"].State != "missing" || byStep["sha256"].InputSnapshot == nil || string(byStep["sha256"].InputSnapshot) != "null" {
		t.Fatalf("missing SHA input is not explicit: %+v", byStep["sha256"])
	}
	resultReference := uuid.New()
	metadata.ResultReference = &resultReference
	for index := range first {
		if first[index].Step == "metadata" {
			first[index].ResultReference = &resultReference
		}
	}
	initialCaptureDigest := automaticArtistCaptureDigestForTest(t, first)

	// JSONB key ordering is not a semantic input change, but a changed selected
	// input must alter the captured digest even when the selected result is not.
	if _, err := database.NewRaw(`UPDATE source_analysis_step SET input_snapshot=?::jsonb WHERE work_id=? AND step='metadata'`,
		`{"selected":{"artist":"first"},"schema_version":1}`, workID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	reordered := byStepFromInputs(load(), "metadata")
	if reordered.InputDigest == nil || *reordered.InputDigest != *metadata.InputDigest {
		t.Fatalf("JSONB key reordering changed input digest: before=%v after=%v", metadata.InputDigest, reordered.InputDigest)
	}
	if _, err := database.NewRaw(`UPDATE source_analysis_step SET input_snapshot=?::jsonb WHERE work_id=? AND step='metadata'`,
		`{"schema_version":1,"selected":{"artist":"second"}}`, workID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	changed := byStepFromInputs(load(), "metadata")
	if changed.InputDigest == nil || *changed.InputDigest == *metadata.InputDigest {
		t.Fatalf("changed selected input retained digest %v", changed.InputDigest)
	}
	changed.ResultReference = metadata.ResultReference
	if changed.ResultReference == nil || *changed.ResultReference != resultReference {
		t.Fatalf("selected result reference changed with input: %+v", changed.ResultReference)
	}
	changedInputs := load()
	for index := range changedInputs {
		if changedInputs[index].Step == "metadata" {
			changedInputs[index].ResultReference = metadata.ResultReference
		}
	}
	changedCaptureDigest := automaticArtistCaptureDigestForTest(t, changedInputs)
	if changedCaptureDigest == initialCaptureDigest {
		t.Fatal("automatic target capture digest ignored changed analysis input with unchanged result reference")
	}
}

func byStepFromInputs(inputs []automaticArtistAnalysisInput, step string) automaticArtistAnalysisInput {
	for _, input := range inputs {
		if input.Step == step {
			return input
		}
	}
	return automaticArtistAnalysisInput{}
}

func automaticArtistCaptureDigestForTest(t *testing.T, inputs []automaticArtistAnalysisInput) [32]byte {
	t.Helper()
	capture := automaticArtistTargetCapture{
		SchemaVersion: 2,
		GroupID:       uuid.Nil,
		GroupRevision: "revision",
		Members: []automaticArtistTargetMember{{
			VariantID: uuid.Nil, AnalysisInputs: inputs,
		}},
	}
	encoded, err := json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}
	_, digest, err := automaticArtistTargetCaptureDigest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
