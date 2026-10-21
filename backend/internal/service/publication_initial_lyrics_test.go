package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type initialLyricsJournalFixture struct {
	order             *[]string
	acquisitionRecord persistence.PublicationInitialLyricsAcquisitionRecord
	acquisition       persistence.PublicationOptionalLyricsOutcome
	rebuildIntent     persistence.PublicationOptionalLyricsOutcome
	rebuildDone       persistence.PublicationOptionalLyricsOutcome
	outcome           persistence.PublicationOptionalLyricsOutcome
	acquisitionErr    error
	rebuildErr        error
	outcomeErr        error
}

func (journal *initialLyricsJournalFixture) GuardedRecordPublicationInitialLyricsAcquisition(_ context.Context, _ persistence.PublicationDeliveryGuard, record persistence.PublicationInitialLyricsAcquisitionRecord) (persistence.PublicationOptionalLyricsOutcome, error) {
	*journal.order = append(*journal.order, "acquisition-ack")
	journal.acquisitionRecord = record
	return journal.acquisition, journal.acquisitionErr
}

func (journal *initialLyricsJournalFixture) GuardedRecordPublicationInitialLyricsRebuild(_ context.Context, _ persistence.PublicationDeliveryGuard, record persistence.PublicationCandidateRebuildRecord) (persistence.PublicationOptionalLyricsOutcome, error) {
	if record.Step == "intent" {
		*journal.order = append(*journal.order, "rebuild-intent-ack")
		return journal.rebuildIntent, nil
	}
	*journal.order = append(*journal.order, "rebuild-complete-ack")
	return journal.rebuildDone, journal.rebuildErr
}

func (journal *initialLyricsJournalFixture) GuardedRecordPublicationInitialLyricsOutcome(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationInitialLyricsOutcomeRecord) (persistence.PublicationOptionalLyricsOutcome, error) {
	*journal.order = append(*journal.order, "outcome-ack")
	return journal.outcome, journal.outcomeErr
}

func initialLyricsFlowFixture(status metadata.OptionalLyricsWriteStatus) (initialLyricsJournalFixture, publicationInitialLyricsCallbacks, *[]string) {
	order := []string{}
	orderRef := &order
	journal := initialLyricsJournalFixture{
		order:         orderRef,
		acquisition:   persistence.PublicationOptionalLyricsRecorded,
		rebuildIntent: persistence.PublicationOptionalLyricsRecorded,
		rebuildDone:   persistence.PublicationOptionalLyricsRecorded,
		outcome:       persistence.PublicationOptionalLyricsRecorded,
	}
	callbacks := publicationInitialLyricsCallbacks{
		CapturePreparedInput: func(context.Context) (metadata.Capture, persistence.PublicationPreparationRecord, persistence.PublicationFileIdentity, error) {
			*orderRef = append(*orderRef, "capture")
			return metadata.Capture{Tags: map[string][]string{"LYRICS": {"[00:01.00]source text"}}}, persistence.PublicationPreparationRecord{}, persistence.PublicationFileIdentity{}, nil
		},
		FrozenTransform: func(context.Context) error { *orderRef = append(*orderRef, "transform"); return nil },
		WriteOptional: func(_ context.Context, requested map[string][]string) (metadata.OptionalLyricsWriteResult, error) {
			*orderRef = append(*orderRef, "writer")
			if requested["TITLE"][0] != "Track" || requested["LYRICS"][0] != "[00:01.00]source text" {
				return metadata.OptionalLyricsWriteResult{}, errors.New("optional writer did not receive complete expected tags")
			}
			return metadata.OptionalLyricsWriteResult{Status: status}, nil
		},
		WriteMandatory: func(_ context.Context, tags map[string][]string) (metadata.WriteResult, error) {
			*orderRef = append(*orderRef, "write-mandatory")
			if tags["TITLE"][0] != "Track" {
				return metadata.WriteResult{}, errors.New("mandatory writer did not receive required tags")
			}
			if _, hasLyrics := tags["LYRICS"]; hasLyrics {
				return metadata.WriteResult{}, errors.New("mandatory writer received lyrics")
			}
			return metadata.WriteResult{Tags: tags}, nil
		},
		PrepareOwnedRebuild: func(context.Context) (persistence.PublicationCandidateRebuildRecord, error) {
			*orderRef = append(*orderRef, "prepare-rebuild")
			return persistence.PublicationCandidateRebuildRecord{Step: "intent"}, nil
		},
		ArmOwnedRebuildAfterAcknowledgedIntent: func(ctx context.Context, record persistence.PublicationCandidateRebuildRecord) (func(context.Context) error, error) {
			*orderRef = append(*orderRef, "rebuild-intent-arm")
			ack, err := journal.GuardedRecordPublicationInitialLyricsRebuild(ctx, record.Guard, record)
			if err != nil {
				return nil, err
			}
			if ack != persistence.PublicationOptionalLyricsRecorded {
				return nil, errors.New("intent was not newly acknowledged")
			}
			return func(context.Context) error {
				*orderRef = append(*orderRef, "owned-rebuild")
				return nil
			}, nil
		},
		CaptureRebuildComplete: func(context.Context) (persistence.PublicationCandidateRebuildRecord, error) {
			*orderRef = append(*orderRef, "capture-rebuild-complete")
			return persistence.PublicationCandidateRebuildRecord{Step: "complete"}, nil
		},
		ValidateFinal: func(_ context.Context, expected map[string][]string) (persistence.PublicationStageObservation, persistence.PublicationFileIdentity, error) {
			*orderRef = append(*orderRef, "validate")
			if expected["TITLE"][0] != "Track" {
				return persistence.PublicationStageObservation{}, persistence.PublicationFileIdentity{}, errors.New("validator did not receive expected tags")
			}
			return persistence.PublicationStageObservation{}, persistence.PublicationFileIdentity{}, nil
		},
		Quarantine:    func() { *orderRef = append(*orderRef, "quarantine") },
		CheckBoundary: func(context.Context) error { return nil },
	}
	return journal, callbacks, orderRef
}

func initialLyricsInputs(authority *librarymetadata.FieldValue) persistence.PublicationInputs {
	return persistence.PublicationInputs{
		IntentKind:    "audio",
		InitialLyrics: &persistence.PublicationInitialLyricsInput{Desired: authority},
		DesiredTags:   map[string][]string{"TITLE": {"Track"}},
	}
}

func TestExecuteInitialPublicationLyricsOrdersOptionalRoundTrip(t *testing.T) {
	journal, callbacks, order := initialLyricsFlowFixture(metadata.OptionalLyricsVerified)
	settings := &initialLyricsSettings{}
	guard := persistence.PublicationDeliveryGuard{ApplicationAttempt: 7}
	result, err := executeInitialPublicationLyrics(context.Background(), guard, initialLyricsInputs(nil), NewPublicationLyricsResolver(settings, nil), lrclib.Metadata{Duration: 10}, &journal, callbacks)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"capture", "acquisition-ack", "transform", "writer", "validate", "outcome-ack"}
	if !reflect.DeepEqual(*order, want) {
		t.Fatalf("unexpected callback order: got %v, want %v", *order, want)
	}
	if result.Outcome.Outcome != persistence.PublicationOptionalLyricsExactRoundTrip {
		t.Fatalf("unexpected outcome: %+v", result.Outcome)
	}
	if journal.acquisitionRecord.Guard != guard {
		t.Fatalf("acquisition ACK was not bound to delivery guard: got %+v, want %+v", journal.acquisitionRecord.Guard, guard)
	}
}

func TestExecuteInitialPublicationLyricsFallbackRerunsFrozenTransform(t *testing.T) {
	for _, status := range []metadata.OptionalLyricsWriteStatus{metadata.OptionalLyricsAbsent, metadata.OptionalLyricsMismatch} {
		journal, callbacks, order := initialLyricsFlowFixture(status)
		_, err := executeInitialPublicationLyrics(context.Background(), persistence.PublicationDeliveryGuard{}, initialLyricsInputs(nil), NewPublicationLyricsResolver(&initialLyricsSettings{}, nil), lrclib.Metadata{Duration: 10}, &journal, callbacks)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"capture", "acquisition-ack", "transform", "writer", "prepare-rebuild", "rebuild-intent-arm", "rebuild-intent-ack", "owned-rebuild", "transform", "write-mandatory", "capture-rebuild-complete", "rebuild-complete-ack", "validate", "outcome-ack"}
		if !reflect.DeepEqual(*order, want) {
			t.Fatalf("status %d: got order %v, want %v", status, *order, want)
		}
	}
}

func TestExecuteInitialPublicationLyricsAcquisitionAckFailurePreventsMutation(t *testing.T) {
	journal, callbacks, order := initialLyricsFlowFixture(metadata.OptionalLyricsVerified)
	journal.acquisitionErr = errors.New("ack failed")
	_, err := executeInitialPublicationLyrics(context.Background(), persistence.PublicationDeliveryGuard{}, initialLyricsInputs(nil), NewPublicationLyricsResolver(&initialLyricsSettings{}, nil), lrclib.Metadata{Duration: 10}, &journal, callbacks)
	if err == nil {
		t.Fatal("expected acquisition acknowledgement failure")
	}
	if want := []string{"capture", "acquisition-ack", "quarantine"}; !reflect.DeepEqual(*order, want) {
		t.Fatalf("mutation continued after failed acquisition acknowledgement: %v", *order)
	}
}

func TestExecuteInitialPublicationLyricsOpaqueWriterFailureDoesNotRebuild(t *testing.T) {
	journal, callbacks, order := initialLyricsFlowFixture(metadata.OptionalLyricsMismatch)
	callbacks.WriteOptional = func(context.Context, map[string][]string) (metadata.OptionalLyricsWriteResult, error) {
		*order = append(*order, "writer")
		return metadata.OptionalLyricsWriteResult{}, errors.New("opaque failure")
	}
	_, err := executeInitialPublicationLyrics(context.Background(), persistence.PublicationDeliveryGuard{}, initialLyricsInputs(nil), NewPublicationLyricsResolver(&initialLyricsSettings{}, nil), lrclib.Metadata{Duration: 10}, &journal, callbacks)
	if err == nil {
		t.Fatal("expected opaque writer failure")
	}
	if want := []string{"capture", "acquisition-ack", "transform", "writer", "quarantine"}; !reflect.DeepEqual(*order, want) {
		t.Fatalf("opaque writer failure was downgraded: %v", *order)
	}
}

func TestExecuteInitialPublicationLyricsRebuildIntentAckFailureStopsRebuild(t *testing.T) {
	journal, callbacks, order := initialLyricsFlowFixture(metadata.OptionalLyricsAbsent)
	ackErr := errors.New("intent acknowledgement failed")
	callbacks.ArmOwnedRebuildAfterAcknowledgedIntent = func(context.Context, persistence.PublicationCandidateRebuildRecord) (func(context.Context) error, error) {
		*order = append(*order, "rebuild-intent-arm")
		return nil, ackErr
	}
	_, err := executeInitialPublicationLyrics(context.Background(), persistence.PublicationDeliveryGuard{}, initialLyricsInputs(nil), NewPublicationLyricsResolver(&initialLyricsSettings{}, nil), lrclib.Metadata{Duration: 10}, &journal, callbacks)
	if !errors.Is(err, ackErr) {
		t.Fatalf("expected rebuild intent acknowledgement failure, got %v", err)
	}
	for _, event := range *order {
		if event == "owned-rebuild" || event == "validate" || event == "outcome-ack" {
			t.Fatalf("rebuild continued without intent acknowledgement: %v", *order)
		}
	}
}

func TestExecuteInitialPublicationLyricsAlreadyRecordedAcquisitionDoesNotAuthorizeWork(t *testing.T) {
	journal, callbacks, order := initialLyricsFlowFixture(metadata.OptionalLyricsVerified)
	journal.acquisition = persistence.PublicationOptionalLyricsAlreadyRecorded
	_, err := executeInitialPublicationLyrics(context.Background(), persistence.PublicationDeliveryGuard{}, initialLyricsInputs(nil), NewPublicationLyricsResolver(&initialLyricsSettings{}, nil), lrclib.Metadata{Duration: 10}, &journal, callbacks)
	if err == nil {
		t.Fatal("already-recorded acquisition must not authorize forward execution")
	}
	if want := []string{"capture", "acquisition-ack", "quarantine"}; !reflect.DeepEqual(*order, want) {
		t.Fatalf("execution continued from a replayed acquisition ACK: %v", *order)
	}
}

func TestExecuteInitialPublicationLyricsCanceledBeforePreparedCapture(t *testing.T) {
	journal, callbacks, order := initialLyricsFlowFixture(metadata.OptionalLyricsVerified)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := executeInitialPublicationLyrics(ctx, persistence.PublicationDeliveryGuard{}, initialLyricsInputs(nil), NewPublicationLyricsResolver(&initialLyricsSettings{}, nil), lrclib.Metadata{Duration: 10}, &journal, callbacks)
	if err == nil {
		t.Fatal("expected canceled execution")
	}
	if want := []string{"quarantine"}; !reflect.DeepEqual(*order, want) {
		t.Fatalf("canceled execution reached callbacks: %v", *order)
	}
}

func TestExecuteInitialPublicationLyricsManualAuthorityDoesNotLookupOrWrite(t *testing.T) {
	journal, callbacks, order := initialLyricsFlowFixture(metadata.OptionalLyricsVerified)
	settings := &initialLyricsSettings{enabled: true}
	lookup := &initialLyricsLookup{result: lrclib.LookupResult{Found: true, Lyrics: "[00:01.00]provider text"}}
	manual := librarymetadata.ManualFieldValue(librarymetadata.StateValue, "[00:01.00]manual", true)
	result, err := executeInitialPublicationLyrics(context.Background(), persistence.PublicationDeliveryGuard{}, initialLyricsInputs(&manual), NewPublicationLyricsResolver(settings, lookup), lrclib.Metadata{Duration: 10}, &journal, callbacks)
	if err != nil {
		t.Fatal(err)
	}
	if settings.calls != 0 || lookup.calls != 0 {
		t.Fatalf("manual authority performed lookup: settings=%d lookup=%d", settings.calls, lookup.calls)
	}
	if result.Outcome.Outcome != persistence.PublicationInitialLyricsNotRequested {
		t.Fatalf("manual authority unexpectedly requested lyrics: %+v", result.Outcome)
	}
	if !reflect.DeepEqual(*order, []string{"capture", "acquisition-ack", "transform", "write-mandatory", "validate", "outcome-ack"}) {
		t.Fatalf("manual authority did not take mandatory-only path: %v", *order)
	}
	for _, event := range *order {
		if event == "writer" || event == "prepare-rebuild" || event == "owned-rebuild" {
			t.Fatalf("manual authority unexpectedly mutated lyrics: %v", *order)
		}
	}
}

type initialLyricsSettings struct {
	enabled bool
	calls   int
}

func (settings *initialLyricsSettings) GetLRCLIBEnabled(context.Context) (bool, error) {
	settings.calls++
	return settings.enabled, nil
}

type initialLyricsLookup struct {
	result lrclib.LookupResult
	calls  int
}

func (lookup *initialLyricsLookup) Lookup(context.Context, lrclib.Metadata) (lrclib.LookupResult, error) {
	lookup.calls++
	return lookup.result, nil
}

func TestResolveInitialPublicationLyricsSuppressesManualAndExplicitEmpty(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field librarymetadata.FieldValue
	}{
		{name: "manual value", field: librarymetadata.ManualFieldValue(librarymetadata.StateValue, "[00:01.00]held text", true)},
		{name: "explicit empty", field: librarymetadata.FieldValue{State: librarymetadata.StateExplicitEmpty, Authority: librarymetadata.AuthorityAutomatic}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := &initialLyricsSettings{enabled: true}
			lookup := &initialLyricsLookup{result: lrclib.LookupResult{Found: true, Lyrics: "[00:01.00]provider text"}}
			record, err := resolveInitialPublicationLyrics(context.Background(), NewPublicationLyricsResolver(settings, lookup), persistence.PublicationInitialLyricsInput{Desired: &tc.field}, map[string][]string{"LYRICS": {"[00:01.00]source text"}}, lrclib.Metadata{Duration: 10})
			if err != nil {
				t.Fatal(err)
			}
			if record.Source != "none" || record.Lyrics != "" || record.SHA256 != "" || settings.calls != 0 || lookup.calls != 0 {
				t.Fatalf("manual/empty authority was not suppressive: record=%+v settings=%d lookup=%d", record, settings.calls, lookup.calls)
			}
		})
	}
}

func TestResolveInitialPublicationLyricsUsesPreparedSourceBeforeProvider(t *testing.T) {
	settings := &initialLyricsSettings{enabled: true}
	lookup := &initialLyricsLookup{result: lrclib.LookupResult{Found: true, Lyrics: "[00:01.00]provider text"}}
	tags := map[string][]string{"LYRICS": {"[00:01.00]source text"}}
	record, err := resolveInitialPublicationLyrics(context.Background(), NewPublicationLyricsResolver(settings, lookup), persistence.PublicationInitialLyricsInput{}, tags, lrclib.Metadata{Duration: 10})
	if err != nil {
		t.Fatal(err)
	}
	if record.Source != "source" || record.Lyrics != "[00:01.00]source text" || record.SHA256 == "" || settings.calls != 0 || lookup.calls != 0 {
		t.Fatalf("unexpected source resolution: record=%+v settings=%d lookup=%d", record, settings.calls, lookup.calls)
	}
	if tags["LYRICS"][0] != "[00:01.00]source text" {
		t.Fatal("source tags were mutated")
	}
}
