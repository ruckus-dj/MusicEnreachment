package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
)

func TestCommittedInitialLyricsRecordsRejectsMalformedEntryWithZeroRecords(t *testing.T) {
	acquisition, outcome, ok := committedInitialLyricsRecords([]json.RawMessage{json.RawMessage(`{`)})
	if ok || acquisition.Version != 0 || outcome.Version != 0 {
		t.Fatalf("malformed journal result = (%#v, %#v, %v), want zero records and false", acquisition, outcome, ok)
	}
}

func TestInitialLyricsAcquisitionRequiresExactValidTextAndDigest(t *testing.T) {
	guard := PublicationDeliveryGuard{OperationID: uuid.New(), ClaimUUID: uuid.New()}
	input := PublicationInputs{Version: PublicationInputsVersion, IntentKind: "audio", InitialLyrics: &PublicationInitialLyricsInput{ExpectedMetadataRevision: 4}}
	lyrics := "[00:01.00]words\n"
	digest := sha256.Sum256([]byte(lyrics))
	record := PublicationInitialLyricsAcquisitionRecord{
		Version: PublicationInitialLyricsVersion, Kind: PublicationInitialLyricsAcquisitionKind,
		Guard: guard, Authority: *input.InitialLyrics, Source: "lrclib", Lyrics: lyrics,
		SHA256: hex.EncodeToString(digest[:]),
	}
	if !validInitialLyricsAcquisition(record, guard, input) {
		t.Fatal("valid exact synced lyrics acquisition was rejected")
	}
	record.SHA256 = "wrong"
	if validInitialLyricsAcquisition(record, guard, input) {
		t.Fatal("acquisition with mismatched digest was accepted")
	}
	record.SHA256 = hex.EncodeToString(digest[:])
	record.Lyrics = "[00:01.00]words\n[00:01.00]words\n"
	if validInitialLyricsAcquisition(record, guard, input) {
		t.Fatal("acquisition with non-canonical LRC was accepted")
	}
}

func TestInitialLyricsManualAndExplicitEmptyAuthoritySuppressAcquisition(t *testing.T) {
	guard := PublicationDeliveryGuard{OperationID: uuid.New(), ClaimUUID: uuid.New()}
	for _, field := range []librarymetadata.FieldValue{
		librarymetadata.ManualFieldValue(librarymetadata.StateValue, "manual", true),
		librarymetadata.ManualFieldValue(librarymetadata.StateExplicitEmpty, nil, false),
	} {
		input := PublicationInputs{Version: PublicationInputsVersion, IntentKind: "audio", InitialLyrics: &PublicationInitialLyricsInput{ExpectedMetadataRevision: 1, Desired: &field}}
		record := PublicationInitialLyricsAcquisitionRecord{Version: PublicationInitialLyricsVersion, Kind: PublicationInitialLyricsAcquisitionKind, Guard: guard, Authority: *input.InitialLyrics, Source: "none"}
		if !validInitialLyricsAcquisition(record, guard, input) {
			t.Fatalf("suppressed acquisition rejected for authority state %q", field.State)
		}
		record.Source, record.Lyrics = "lrclib", "[00:01.00]not allowed\n"
		if validInitialLyricsAcquisition(record, guard, input) {
			t.Fatalf("automatic lyrics accepted for protected authority state %q", field.State)
		}
	}
}

func TestInitialLyricsManualAbsentAuthorityAllowsAutomaticAcquisition(t *testing.T) {
	guard := PublicationDeliveryGuard{OperationID: uuid.New(), ClaimUUID: uuid.New()}
	field := librarymetadata.ManualFieldValue(librarymetadata.StateAbsent, nil, false)
	input := PublicationInputs{Version: PublicationInputsVersion, IntentKind: "audio", InitialLyrics: &PublicationInitialLyricsInput{ExpectedMetadataRevision: 1, Desired: &field}}
	lyrics := "[00:01.00]words\n"
	digest := sha256.Sum256([]byte(lyrics))
	record := PublicationInitialLyricsAcquisitionRecord{Version: PublicationInitialLyricsVersion, Kind: PublicationInitialLyricsAcquisitionKind, Guard: guard, Authority: *input.InitialLyrics, Source: "lrclib", Lyrics: lyrics, SHA256: hex.EncodeToString(digest[:])}
	if !validInitialLyricsAcquisition(record, guard, input) {
		t.Fatal("manual absent authority incorrectly suppressed automatic acquisition")
	}
}

func TestInitialLyricsJournalRejectsUnknownAndPartialPrefixes(t *testing.T) {
	guard := PublicationDeliveryGuard{OperationID: uuid.New(), ClaimUUID: uuid.New()}
	input := PublicationInputs{Version: PublicationInputsVersion, IntentKind: "audio", InitialLyrics: &PublicationInitialLyricsInput{ExpectedMetadataRevision: 0}}
	for _, raw := range []string{`[{"version":1,"kind":"unknown"}]`, `[{"version":1,"kind":"publication_preparation","step":"directory_created"}]`} {
		var entries []json.RawMessage
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			t.Fatal(err)
		}
		if _, err := parseInitialLyricsPrefix(entries, guard, input); err == nil {
			t.Fatalf("accepted malformed or partial journal %s", raw)
		}
	}
}

func TestInitialLyricsJournalAcceptsStrictPreparationAcquisitionPrefix(t *testing.T) {
	guard := PublicationDeliveryGuard{OperationID: uuid.New(), ClaimUUID: uuid.New()}
	input := PublicationInputs{Version: PublicationInputsVersion, IntentKind: "audio", InitialLyrics: &PublicationInitialLyricsInput{ExpectedMetadataRevision: 0}}
	parent := PublicationDirectoryIdentity{Platform: "darwin", Volume: "volume", FileID: "parent"}
	directory := PublicationDirectoryIdentity{Platform: "darwin", Volume: "volume", FileID: "directory"}
	owner := uuid.New()
	size, mtime := int64(5), int64(10)
	prep := PublicationPreparationRecord{Version: PublicationPreparationVersion, Kind: "publication_preparation", OperationID: guard.OperationID, ClaimUUID: guard.ClaimUUID, OwnerToken: owner, PrivateDirRel: "private", InputRel: "private/input.flac", OutputRel: "private/output.flac", ParentIdentity: parent}
	intent := prep
	intent.Step = "intent"
	created := prep
	created.Step = "directory_created"
	created.DirectoryIdentity = &directory
	ready := prep
	ready.Step = "input_ready"
	ready.DirectoryIdentity = &directory
	ready.InputSize, ready.InputMTime = &size, &mtime
	ready.CopiedInput = &PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	acquisition := PublicationInitialLyricsAcquisitionRecord{Version: PublicationInitialLyricsVersion, Kind: PublicationInitialLyricsAcquisitionKind, Guard: guard, Authority: *input.InitialLyrics, Source: "none"}
	entries := make([]json.RawMessage, 0, 4)
	for _, value := range []any{intent, created, ready, acquisition} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, raw)
	}
	state, err := parseInitialLyricsPrefix(entries, guard, input)
	if err != nil {
		t.Fatalf("valid preparation/acquisition prefix rejected: %v", err)
	}
	if state.acquisition == nil || state.preparation == nil || state.preparation.Step != "input_ready" {
		t.Fatal("valid prefix did not preserve terminal preparation and acquisition")
	}
	last, _ := json.Marshal(map[string]any{"version": 1, "kind": PublicationInitialLyricsAcquisitionKind, "unexpected": true})
	withUnknown := append(append([]json.RawMessage(nil), entries[:3]...), last)
	if _, err := parseInitialLyricsPrefix(withUnknown, guard, input); err == nil {
		t.Fatal("strict record decoder accepted unknown acquisition field")
	}
}

func TestInitialOutputCleanupEvidenceIsRetainedAndNotReplayable(t *testing.T) {
	guard := PublicationDeliveryGuard{OperationID: uuid.New(), ClaimUUID: uuid.New()}
	input := PublicationInputs{Version: PublicationInputsVersion, IntentKind: "audio", InitialLyrics: &PublicationInitialLyricsInput{ExpectedMetadataRevision: 0}}
	size, mtime, outSize, outMtime := int64(5), int64(10), int64(12), int64(20)
	parent := PublicationDirectoryIdentity{Platform: "darwin", Volume: "vol", FileID: "parent"}
	directory := PublicationDirectoryIdentity{Platform: "darwin", Volume: "vol", FileID: "directory"}
	prep := PublicationPreparationRecord{Version: PublicationPreparationVersion, Kind: "publication_preparation", Step: "input_ready", OperationID: guard.OperationID, ClaimUUID: guard.ClaimUUID, OwnerToken: uuid.New(), PrivateDirRel: "private", InputRel: "private/input.flac", OutputRel: "private/output.flac", ParentIdentity: parent, DirectoryIdentity: &directory, InputSize: &size, InputMTime: &mtime, CopiedInput: &PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	intent := prep
	intent.Step, intent.DirectoryIdentity, intent.InputSize, intent.InputMTime, intent.CopiedInput = "intent", nil, nil, nil, nil
	created := prep
	created.Step, created.InputSize, created.InputMTime, created.CopiedInput = "directory_created", nil, nil, nil
	acquisition := PublicationInitialLyricsAcquisitionRecord{Version: PublicationInitialLyricsVersion, Kind: PublicationInitialLyricsAcquisitionKind, Guard: guard, Authority: *input.InitialLyrics, Source: "none"}
	witness := PublicationInitialOutputCleanupWitness{Preparation: prep, Input: PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, Output: PublicationFileIdentity{SizeBytes: &outSize, MTime: &outMtime, SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, OutputRel: prep.OutputRel}
	entries := make([]json.RawMessage, 0, 6)
	for _, record := range []any{intent, created, prep, acquisition} {
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, raw)
	}
	if !validInitialOutputCleanupWitness(guard, witness) {
		t.Fatal("valid native cleanup witness rejected")
	}
	cleanupIntent := publicationInitialOutputCleanupRecord{Version: initialOutputCleanupVersion, Kind: "publication_initial_output_cleanup", Step: "intent", Guard: guard, Witness: witness}
	raw, _ := json.Marshal(cleanupIntent)
	withIntent := append(append([]json.RawMessage(nil), entries...), raw)
	if !exactInitialOutputCleanupPrefix(withIntent, guard, input, witness, 1) {
		t.Fatal("valid pending cleanup intent rejected")
	}
	if exactInitialOutputCleanupPrefix(withIntent, guard, input, witness, 2) {
		t.Fatal("retained cleanup intent was treated as settled")
	}
	if _, err := parseInitialLyricsPrefix(withIntent, guard, input); err == nil {
		t.Fatal("recovery parser accepted retained cleanup intent")
	}
	cleanupComplete := cleanupIntent
	cleanupComplete.Step, cleanupComplete.Result = "complete", "removed"
	raw, _ = json.Marshal(cleanupComplete)
	withComplete := append(withIntent, raw)
	if !exactInitialOutputCleanupPrefix(withComplete, guard, input, witness, 2) {
		t.Fatal("exact completed cleanup evidence rejected")
	}
	cleanupComplete.Witness.Output.SHA256 = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	raw, _ = json.Marshal(cleanupComplete)
	withComplete[5] = raw
	if exactInitialOutputCleanupPrefix(withComplete, guard, input, witness, 2) {
		t.Fatal("cleanup completion with changed output authority accepted")
	}
}

func TestPublicationRecoveryFrontierRetainsInitialOutputCleanupWitness(t *testing.T) {
	guard := PublicationDeliveryGuard{OperationID: uuid.New(), ApplicationAttempt: 2, RiverJobID: 9, RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "delivery", Epoch: uuid.New(), CandidateGeneration: uuid.New(), ExpectedPositionID: uuid.New()}
	input := publicationInputsFixture(t, "audio")
	inputRaw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	size, sourceMTime, copiedMTime, outputSize, outputMTime := int64(12), int64(21), int64(25), int64(30), int64(40)
	parent := PublicationDirectoryIdentity{Platform: "darwin", Volume: "disk", FileID: "parent"}
	directory := PublicationDirectoryIdentity{Platform: "darwin", Volume: "disk", FileID: "directory"}
	base := PublicationPreparationRecord{Version: PublicationPreparationVersion, Kind: "publication_preparation", OperationID: guard.OperationID, ClaimUUID: guard.ClaimUUID, OwnerToken: uuid.New(), PrivateDirRel: "private", InputRel: "private/input.flac", OutputRel: "private/output.flac", ParentIdentity: parent}
	intent := base
	intent.Step = "intent"
	created := base
	created.Step, created.DirectoryIdentity = "directory_created", &directory
	ready := created
	ready.Step, ready.InputSize, ready.InputMTime = "input_ready", &size, &sourceMTime
	ready.CopiedInput = &PublicationFileIdentity{SizeBytes: &size, MTime: &copiedMTime, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	acquisition := PublicationInitialLyricsAcquisitionRecord{Version: PublicationInitialLyricsVersion, Kind: PublicationInitialLyricsAcquisitionKind, Guard: guard, Authority: *input.InitialLyrics, Source: "none"}
	witness := PublicationInitialOutputCleanupWitness{Preparation: ready, Input: *ready.CopiedInput, Output: PublicationFileIdentity{SizeBytes: &outputSize, MTime: &outputMTime, SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, OutputRel: ready.OutputRel}
	intentRecord := publicationInitialOutputCleanupRecord{Version: initialOutputCleanupVersion, Kind: "publication_initial_output_cleanup", Step: "intent", Guard: guard, Witness: witness}
	completeRecord := intentRecord
	completeRecord.Step, completeRecord.Result = "complete", "removed"
	entries := make([]json.RawMessage, 0, 6)
	for _, record := range []any{intent, created, ready, acquisition, intentRecord, completeRecord} {
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, raw)
	}
	journal, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	attempt := TrackPublicationAttempt{OperationID: guard.OperationID, PositionID: guard.ExpectedPositionID, Epoch: guard.Epoch, CandidateGeneration: guard.CandidateGeneration, IntentKind: "audio", Phase: "preparing", FrozenInputs: inputRaw, MutationJournal: journal}
	claim := PublicationExecutionClaim{OperationID: guard.OperationID, ApplicationAttempt: guard.ApplicationAttempt, RiverJobID: guard.RiverJobID, RiverAttempt: guard.RiverAttempt, ClaimUUID: guard.ClaimUUID, BootUUID: guard.BootUUID, DeliveryToken: guard.DeliveryToken, Epoch: guard.Epoch, State: "recovery_required"}
	frontier, err := publicationRecoveryJournalFrontier(attempt, claim)
	if err != nil || frontier != "initial_output_cleanup_retained" {
		t.Fatalf("recovery frontier = %q, %v", frontier, err)
	}
	if _, err := parseInitialLyricsPrefix(entries, guard, input); err == nil {
		t.Fatal("initial lyrics parser permitted recovery to replay retained cleanup evidence")
	}
}

func TestInitialLyricsOutcomeRequiresExactDurablePreparedInput(t *testing.T) {
	makeReady := func() (PublicationPreparationRecord, PublicationFileIdentity) {
		guard := PublicationDeliveryGuard{OperationID: uuid.New(), ClaimUUID: uuid.New()}
		size, mtime := int64(5), int64(10)
		identity := PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
		directory := PublicationDirectoryIdentity{Platform: "darwin", Volume: "volume", FileID: "directory"}
		return PublicationPreparationRecord{
			Version: PublicationPreparationVersion, Kind: "publication_preparation", Step: "input_ready",
			OperationID: guard.OperationID, ClaimUUID: guard.ClaimUUID, OwnerToken: uuid.New(),
			PrivateDirRel: "private", InputRel: "private/input.flac", OutputRel: "private/output.flac",
			ParentIdentity:    PublicationDirectoryIdentity{Platform: "darwin", Volume: "volume", FileID: "parent"},
			DirectoryIdentity: &directory, InputSize: &size, InputMTime: &mtime, CopiedInput: &identity,
		}, identity
	}

	ready, inputIdentity := makeReady()
	if !sameInitialLyricsReadyPreparation(ready, ready, inputIdentity) {
		t.Fatal("identical durable input preparation was rejected")
	}
	// InputMTime is the original source fact; copied-input identity is the
	// durable native identity and can have an independently assigned mtime.
	sourceMTime := *ready.InputMTime + 1
	ready.InputMTime = &sourceMTime
	if !sameInitialLyricsReadyPreparation(ready, ready, inputIdentity) {
		t.Fatal("source mtime differing from copied-input mtime was rejected")
	}
	mutations := map[string]func(*PublicationPreparationRecord, *PublicationFileIdentity){
		"copied digest": func(prep *PublicationPreparationRecord, _ *PublicationFileIdentity) {
			prep.CopiedInput.SHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		},
		"directory file id": func(prep *PublicationPreparationRecord, _ *PublicationFileIdentity) {
			prep.DirectoryIdentity.FileID = "substituted-directory"
		},
		"input mtime": func(prep *PublicationPreparationRecord, _ *PublicationFileIdentity) {
			value := *prep.InputMTime + 1
			prep.InputMTime = &value
		},
		"copied identity mtime": func(prep *PublicationPreparationRecord, _ *PublicationFileIdentity) {
			value := *prep.CopiedInput.MTime + 1
			prep.CopiedInput.MTime = &value
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			durable, identity := makeReady()
			recorded := durable
			// Detach nested pointers before constructing a forged alternative receipt.
			directory := *durable.DirectoryIdentity
			recorded.DirectoryIdentity = &directory
			copied := clonePublicationFileIdentity(*durable.CopiedInput)
			recorded.CopiedInput = &copied
			mutate(&recorded, &identity)
			if sameInitialLyricsReadyPreparation(recorded, durable, identity) {
				t.Fatal("substituted prepared-input receipt was accepted")
			}
		})
	}
	if sameInitialLyricsReadyPreparation(ready, ready, PublicationFileIdentity{SizeBytes: ready.CopiedInput.SizeBytes, MTime: ready.CopiedInput.MTime, SHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}) {
		t.Fatal("outcome input identity with substituted digest was accepted")
	}
}
