//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// This exercises exposure through the repository API (rather than only the
// journal codec): exposure is authorized only after the delivery claim and a
// complete, ordered preparation/staging chain have been persisted.
func TestPublicationExposureRequiresDurablePreparationAndStagePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(ctx, f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	observation := newValidPublicationStageObservation()
	if ok, err := repo.AdvancePublicationStage(ctx, f.guard, "preparing", observation); err != nil || !ok {
		t.Fatalf("preparing = %t, %v", ok, err)
	}
	if ok, err := repo.AdvancePublicationStage(ctx, f.guard, "staged", observation); err != nil || !ok {
		t.Fatalf("validated = %t, %v", ok, err)
	}
	intent := PublicationExposureIntent{
		Version: PublicationExposureVersion, StagedPath: observation.RelativePath,
		TargetCanonicalKey: "root/album/track.flac", TargetRelativePath: "root/album/track.flac",
		Expectation: "absent", PrepOwner: uuid.New(),
		OutputRootParent: PublicationDirectoryIdentity{Platform: "darwin", Volume: "volume", FileID: "root"},
		TargetParent:     PublicationDirectoryIdentity{Platform: "darwin", Volume: "volume", FileID: "target"},
	}
	if outcome, err := repo.BeginPublicationExposure(ctx, f.guard, intent, observation); err != nil || outcome != PublicationExposureStale {
		t.Fatalf("exposure without preparation = %q, %v", outcome, err)
	}
	attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil || attempt.Phase != "validated" {
		t.Fatalf("attempt changed without preparation: phase=%q err=%v", attempt.Phase, err)
	}
	var journal []json.RawMessage
	if err := json.Unmarshal(attempt.MutationJournal, &journal); err != nil || len(journal) != 2 {
		t.Fatalf("journal changed without preparation: entries=%d err=%v", len(journal), err)
	}
}

func readyPublicationExposureFixture(t *testing.T) (publicationExecutionFixture, PublicationExposureIntent, PublicationStageObservation) {
	t.Helper()
	ctx := context.Background()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(ctx, f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	input := publicationInputsFixture(t, "audio")
	frozen, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	intent := PublicationExposureIntent{
		Version: PublicationExposureVersion, StagedPath: "private/op/output.flac",
		TargetCanonicalKey: "root/final.flac", TargetRelativePath: "root/final.flac",
		Expectation: "absent", PrepOwner: uuid.New(),
		OutputRootParent: PublicationDirectoryIdentity{Platform: "darwin", Volume: "volume", FileID: "root"},
		TargetParent:     PublicationDirectoryIdentity{Platform: "darwin", Volume: "volume", FileID: "root"},
	}
	manifest := []PublicationNamespaceNode{
		{CanonicalKey: "root", RelativePath: "root", NodeKind: "dir", DirectoryScope: "root"},
		{CanonicalKey: intent.TargetCanonicalKey, RelativePath: intent.TargetRelativePath, NodeKind: "file"},
	}
	manifestJSON, err := json.Marshal(struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}{Version: 1, Nodes: manifest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE track_publication_attempt SET frozen_inputs=?::jsonb, namespace_manifest=?::jsonb WHERE operation_id=?`, string(frozen), string(manifestJSON), f.guard.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for _, node := range manifest {
		digest := sha256.Sum256([]byte(node.CanonicalKey))
		claimID := uuid.New()
		var owner any = f.guard.ExpectedPositionID
		var scope any
		if node.NodeKind == "dir" {
			owner, scope = nil, node.DirectoryScope
		}
		if _, err := f.db.NewRaw(`INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id,directory_scope_key) VALUES (?,?,?,?,?,?,?,?)`, claimID, f.guard.Epoch, digest[:], node.CanonicalKey, node.RelativePath, node.NodeKind, owner, scope).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.NewRaw(`INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, claimID, f.guard.ExpectedPositionID, f.guard.CandidateGeneration).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	parent := intent.OutputRootParent
	directory := PublicationDirectoryIdentity{Platform: "darwin", Volume: "volume", FileID: "private-op"}
	base := PublicationPreparationRecord{
		Version: PublicationPreparationVersion, Kind: "publication_preparation", OperationID: f.guard.OperationID,
		ClaimUUID: f.guard.ClaimUUID, OwnerToken: intent.PrepOwner, PrivateDirRel: "private/op",
		InputRel: "private/op/input.flac", OutputRel: intent.StagedPath, ParentIdentity: parent,
	}
	for _, record := range []PublicationPreparationRecord{
		base,
		{Version: PublicationPreparationVersion, Kind: base.Kind, Step: "directory_created", OperationID: base.OperationID, ClaimUUID: base.ClaimUUID, OwnerToken: base.OwnerToken, PrivateDirRel: base.PrivateDirRel, InputRel: base.InputRel, OutputRel: base.OutputRel, ParentIdentity: parent, DirectoryIdentity: &directory},
	} {
		if record.Step == "" {
			record.Step = "intent"
		}
		if outcome, err := repo.RecordPublicationPreparation(ctx, f.guard, record); err != nil || outcome != PublicationPreparationRecorded {
			t.Fatalf("preparation %s = %q, %v", record.Step, outcome, err)
		}
	}
	size, mtime := int64(10), int64(20)
	copiedSize, copiedMTime := int64(30), int64(40)
	ready := base
	ready.Step, ready.DirectoryIdentity, ready.InputSize, ready.InputMTime = "input_ready", &directory, &size, &mtime
	ready.CopiedInput = &PublicationFileIdentity{SizeBytes: &copiedSize, MTime: &copiedMTime}
	if outcome, err := repo.RecordPublicationPreparation(ctx, f.guard, ready); err != nil || outcome != PublicationPreparationRecorded {
		t.Fatalf("preparation input_ready = %q, %v", outcome, err)
	}
	lossless, channels, bitDepth, sampleRate := true, 2, 16, 44100
	facts := PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Channels: &channels, BitDepth: &bitDepth, SampleRate: &sampleRate, Container: "flac", SupportedSourceContainer: true}
	actualAudio, err := json.Marshal(PublicationCurrentAudio{Version: 1, Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	observation := PublicationStageObservation{
		Version: 1, RelativePath: intent.StagedPath, Ownership: ArtifactOwnership{ExpectedSize: &size, ExpectedMTime: &mtime},
		PayloadSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ActualAudio:   actualAudio, ActualMetadata: json.RawMessage(`{"title":["Track"]}`),
	}
	for _, phase := range []string{"preparing", "staged"} {
		if ok, err := repo.AdvancePublicationStage(ctx, f.guard, phase, observation); err != nil || !ok {
			t.Fatalf("advance %s = %t, %v", phase, ok, err)
		}
	}
	return f, intent, observation
}

func TestPublicationExposureRecordsAndReplaysIdenticallyPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, intent, observation := readyPublicationExposureFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.BeginPublicationExposure(ctx, f.guard, intent, observation); err != nil || outcome != PublicationExposureRecorded {
		t.Fatalf("exposure = %q, %v", outcome, err)
	}
	before, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil || before.Phase != "exposing" {
		t.Fatalf("attempt phase = %q, %v", before.Phase, err)
	}
	frozen, err := DecodePublicationInputs(before.FrozenInputs)
	if err != nil || frozen.IntentKind != "audio" || frozen.Audio == nil {
		t.Fatalf("frozen publication inputs were not retained: intent=%q err=%v", frozen.IntentKind, err)
	}
	var journal []json.RawMessage
	if err := json.Unmarshal(before.MutationJournal, &journal); err != nil || len(journal) != 6 {
		t.Fatalf("journal entries = %d, %v", len(journal), err)
	}
	var state, stage string
	if err := f.db.NewRaw(`SELECT state,stage FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &state, &stage); err != nil || state != "running" || stage != "exposing" {
		t.Fatalf("operation state/stage = %q/%q, %v", state, stage, err)
	}
	var publicationCount int
	if err := f.db.NewRaw(`SELECT count(*) FROM track_publication WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx, &publicationCount); err != nil || publicationCount != 0 {
		t.Fatalf("current publication count = %d, %v", publicationCount, err)
	}
	if outcome, err := repo.BeginPublicationExposure(ctx, f.guard, intent, observation); err != nil || outcome != PublicationExposureAlreadyRecorded {
		t.Fatalf("replay = %q, %v", outcome, err)
	}
	after, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil || string(after.MutationJournal) != string(before.MutationJournal) {
		t.Fatalf("replay changed journal: %v", err)
	}
}

func TestPublicationExposureRejectsChangedRequestWithoutMutationPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, intent, observation := readyPublicationExposureFixture(t)
	repo := NewSetupManagerRepository(f.db)
	observation.PayloadSHA256 = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if outcome, err := repo.BeginPublicationExposure(ctx, f.guard, intent, observation); err != nil || outcome != PublicationExposureStale {
		t.Fatalf("changed exposure = %q, %v", outcome, err)
	}
	assertPublicationExposureUnchanged(t, ctx, f)
}

func TestPublicationExposureRejectsOperationStageMismatchPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, intent, observation := readyPublicationExposureFixture(t)
	if _, err := f.db.NewRaw(`UPDATE operation SET stage='preparing' WHERE id=?`, f.guard.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.BeginPublicationExposure(ctx, f.guard, intent, observation); err != nil || outcome != PublicationExposureStale {
		t.Fatalf("stage-mismatched exposure = %q, %v", outcome, err)
	}
	assertPublicationExposureUnchanged(t, ctx, f)
}

func TestPublicationExposureRequiresNamespaceReservationPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, intent, observation := readyPublicationExposureFixture(t)
	if _, err := f.db.NewRaw(`DELETE FROM publication_namespace_reservation r USING publication_namespace_claim c WHERE r.namespace_claim_id=c.id AND c.epoch=? AND c.canonical_key=? AND r.position_id=? AND r.generation=?`, f.guard.Epoch, intent.TargetCanonicalKey, f.guard.ExpectedPositionID, f.guard.CandidateGeneration).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.BeginPublicationExposure(ctx, f.guard, intent, observation); err != nil || outcome != PublicationExposureStale {
		t.Fatalf("unreserved exposure = %q, %v", outcome, err)
	}
	assertPublicationExposureUnchanged(t, ctx, f)
}

func assertPublicationExposureUnchanged(t *testing.T, ctx context.Context, f publicationExecutionFixture) {
	t.Helper()
	attempt, err := NewSetupManagerRepository(f.db).ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil || attempt.Phase != "validated" {
		t.Fatalf("attempt changed: phase=%q err=%v", attempt.Phase, err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil || len(entries) != 5 {
		t.Fatalf("journal changed: entries=%d err=%v", len(entries), err)
	}
}

func TestPublicationCandidateInstalledRecordsAndReplaysPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, intent, staged := readyPublicationExposureFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.BeginPublicationExposure(ctx, f.guard, intent, staged); err != nil || outcome != PublicationExposureRecorded {
		t.Fatalf("begin exposure = %q, %v", outcome, err)
	}
	size, mtime := int64(10), int64(20)
	installed := PublicationCandidateInstalledObservation{Version: 1, TargetRelativePath: intent.TargetRelativePath, Identity: PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: staged.PayloadSHA256}}
	if outcome, err := repo.RecordPublicationCandidateInstalled(ctx, f.guard, intent, staged, installed); err != nil || outcome != PublicationCandidateInstalledRecorded {
		t.Fatalf("record installed = %q, %v", outcome, err)
	}
	before, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil || before.Phase != "exposing" {
		t.Fatalf("attempt phase = %q, %v", before.Phase, err)
	}
	if outcome, err := repo.RecordPublicationCandidateInstalled(ctx, f.guard, intent, staged, installed); err != nil || outcome != PublicationCandidateInstalledAlreadyRecorded {
		t.Fatalf("replay installed = %q, %v", outcome, err)
	}
	changed := installed
	changed.Identity = clonePublicationFileIdentity(installed.Identity)
	otherDigest := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	changed.Identity.SHA256 = otherDigest
	if outcome, err := repo.RecordPublicationCandidateInstalled(ctx, f.guard, intent, staged, changed); err != nil || outcome != PublicationCandidateInstalledStale {
		t.Fatalf("changed installed observation = %q, %v", outcome, err)
	}
	changed = installed
	changed.TargetRelativePath = "root/other.flac"
	if outcome, err := repo.RecordPublicationCandidateInstalled(ctx, f.guard, intent, staged, changed); err != nil || outcome != PublicationCandidateInstalledStale {
		t.Fatalf("changed installed target = %q, %v", outcome, err)
	}
	staleGuard := f.guard
	staleGuard.DeliveryToken += "-stale"
	if outcome, err := repo.RecordPublicationCandidateInstalled(ctx, staleGuard, intent, staged, installed); err != nil || outcome != PublicationCandidateInstalledStale {
		t.Fatalf("stale guard = %q, %v", outcome, err)
	}
	after, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil || string(after.MutationJournal) != string(before.MutationJournal) {
		t.Fatalf("replay/conflict changed journal: %v", err)
	}
	var currentCount int
	if err := f.db.NewRaw(`SELECT count(*) FROM track_publication WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx, &currentCount); err != nil || currentCount != 0 {
		t.Fatalf("current publication count = %d, %v", currentCount, err)
	}
}

func TestPublicationCandidateInstalledRejectsMalformedExposureJournalPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func([]json.RawMessage) []json.RawMessage
	}{
		{name: "duplicate exposure", mutate: func(entries []json.RawMessage) []json.RawMessage { return append(entries, entries[len(entries)-1]) }},
		{name: "malformed unrelated exposure", mutate: func(entries []json.RawMessage) []json.RawMessage {
			var record map[string]any
			if err := json.Unmarshal(entries[len(entries)-1], &record); err != nil {
				panic(err)
			}
			record["intent"] = map[string]any{"version": 1}
			encoded, _ := json.Marshal(record)
			return append(entries, encoded)
		}},
		{name: "malformed guard", mutate: func(entries []json.RawMessage) []json.RawMessage {
			var record map[string]any
			if err := json.Unmarshal(entries[len(entries)-1], &record); err != nil {
				panic(err)
			}
			record["guard"] = map[string]any{}
			encoded, _ := json.Marshal(record)
			entries[len(entries)-1] = encoded
			return entries
		}},
		{name: "invalid phase transition", mutate: func(entries []json.RawMessage) []json.RawMessage {
			var record map[string]any
			if err := json.Unmarshal(entries[len(entries)-1], &record); err != nil {
				panic(err)
			}
			record["from"] = "staged"
			encoded, _ := json.Marshal(record)
			entries[len(entries)-1] = encoded
			return entries
		}},
		{name: "conflicting position tuple", mutate: func(entries []json.RawMessage) []json.RawMessage {
			var record map[string]any
			if err := json.Unmarshal(entries[len(entries)-1], &record); err != nil {
				panic(err)
			}
			record["position_id"] = uuid.NewString()
			encoded, _ := json.Marshal(record)
			entries[len(entries)-1] = encoded
			return entries
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, intent, staged := readyPublicationExposureFixture(t)
			repo := NewSetupManagerRepository(f.db)
			if outcome, err := repo.BeginPublicationExposure(ctx, f.guard, intent, staged); err != nil || outcome != PublicationExposureRecorded {
				t.Fatalf("begin exposure = %q, %v", outcome, err)
			}
			before, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			var entries []json.RawMessage
			if err := json.Unmarshal(before.MutationJournal, &entries); err != nil {
				t.Fatal(err)
			}
			journal, err := json.Marshal(tc.mutate(entries))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=?`, string(journal), f.guard.OperationID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			badBefore, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			size, mtime := int64(10), int64(20)
			installed := PublicationCandidateInstalledObservation{Version: 1, TargetRelativePath: intent.TargetRelativePath, Identity: PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: staged.PayloadSHA256}}
			if outcome, err := repo.RecordPublicationCandidateInstalled(ctx, f.guard, intent, staged, installed); outcome != PublicationCandidateInstalledStale {
				t.Fatalf("malformed exposure = %q, %v", outcome, err)
			}
			after, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
			if err != nil || after.Phase != "exposing" || string(after.MutationJournal) != string(badBefore.MutationJournal) {
				t.Fatalf("attempt mutated: phase=%q err=%v", after.Phase, err)
			}
			var state, stage string
			if err := f.db.NewRaw(`SELECT state,stage FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &state, &stage); err != nil || state != "running" || stage != "exposing" {
				t.Fatalf("operation state/stage changed: %q/%q err=%v", state, stage, err)
			}
			var claimState string
			if err := f.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &claimState); err != nil || claimState != "executing" {
				t.Fatalf("claim state changed: %q err=%v", claimState, err)
			}
		})
	}
}

func TestPublicationCandidateInstalledRejectsDuplicateInstalledJournalPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, intent, staged := readyPublicationExposureFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.BeginPublicationExposure(ctx, f.guard, intent, staged); err != nil || outcome != PublicationExposureRecorded {
		t.Fatalf("begin exposure = %q, %v", outcome, err)
	}
	size, mtime := int64(10), int64(20)
	installed := PublicationCandidateInstalledObservation{Version: 1, TargetRelativePath: intent.TargetRelativePath, Identity: PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: staged.PayloadSHA256}}
	if outcome, err := repo.RecordPublicationCandidateInstalled(ctx, f.guard, intent, staged, installed); err != nil || outcome != PublicationCandidateInstalledRecorded {
		t.Fatalf("record installed = %q, %v", outcome, err)
	}
	before, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(before.MutationJournal, &entries); err != nil {
		t.Fatal(err)
	}
	journal, err := json.Marshal(append(entries, entries[len(entries)-1]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=?`, string(journal), f.guard.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	badBefore, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := repo.RecordPublicationCandidateInstalled(ctx, f.guard, intent, staged, installed); outcome != PublicationCandidateInstalledStale {
		t.Fatalf("duplicate installed records = %q, %v", outcome, err)
	}
	after, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil || after.Phase != "exposing" || string(after.MutationJournal) != string(badBefore.MutationJournal) {
		t.Fatalf("attempt mutated: phase=%q err=%v", after.Phase, err)
	}
}
