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
