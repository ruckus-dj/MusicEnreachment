package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/uptrace/bun"
)

const PublicationInitialLyricsVersion = 1

const (
	PublicationInitialLyricsAcquisitionKind = "publication_initial_lyrics_acquisition"
	PublicationInitialLyricsOutcomeKind     = "publication_initial_lyrics_outcome"
	PublicationInitialLyricsNotRequested    = "not_requested"
)

// PublicationInitialLyricsProjection is the retained, source-free current-row
// summary. It deliberately excludes lyric text and source paths.
type PublicationInitialLyricsProjection struct {
	Version                    int       `json:"version"`
	Generation                 uuid.UUID `json:"generation"`
	Source                     string    `json:"source"`
	SHA256                     string    `json:"sha256"`
	Outcome                    string    `json:"outcome"`
	ResolutionWarningCode      string    `json:"resolution_warning_code"`
	OutcomeWarningCode         string    `json:"outcome_warning_code"`
	AuthorityMetadataRevision  int64     `json:"authority_metadata_revision"`
	AuthorityState             string    `json:"authority_state"`
	Authority                  string    `json:"authority"`
	AuthorityProvenance        string    `json:"authority_provenance"`
	AuthorityFingerprintSHA256 string    `json:"authority_fingerprint_sha256,omitempty"`
}

func initialLyricsProjection(acquisition PublicationInitialLyricsAcquisitionRecord, outcome PublicationInitialLyricsOutcomeRecord, generation uuid.UUID) (*PublicationInitialLyricsProjection, error) {
	if generation == uuid.Nil || acquisition.Authority.Desired == nil && acquisition.Authority.ExpectedMetadataRevision < 0 {
		return nil, errors.New("invalid initial lyrics projection authority")
	}
	projection := &PublicationInitialLyricsProjection{
		Version: PublicationInitialLyricsVersion, Generation: generation, Source: acquisition.Source,
		SHA256: acquisition.SHA256, Outcome: outcome.Outcome,
		ResolutionWarningCode: acquisition.WarningCode, OutcomeWarningCode: outcome.WarningCode,
		AuthorityMetadataRevision: acquisition.Authority.ExpectedMetadataRevision,
		AuthorityState:            "absent", Authority: "automatic", AuthorityProvenance: "source_metadata",
	}
	if desired := acquisition.Authority.Desired; desired != nil {
		projection.AuthorityState = string(desired.State)
		projection.Authority = string(desired.Authority)
		projection.AuthorityProvenance = string(desired.Provenance.Kind)
		fingerprint := sha256.Sum256(mustJSON(desired))
		projection.AuthorityFingerprintSHA256 = hex.EncodeToString(fingerprint[:])
	}
	if (projection.Source == "none" && projection.SHA256 != "") ||
		(projection.Source != "none" && !validSHA256(projection.SHA256)) ||
		projection.Outcome != PublicationInitialLyricsNotRequested && projection.Outcome != PublicationOptionalLyricsExactRoundTrip && projection.Outcome != PublicationOptionalLyricsRebuiltWithoutLyrics {
		return nil, errors.New("invalid initial lyrics projection evidence")
	}
	return projection, nil
}

func sameInitialLyricsProjectionJSON(raw json.RawMessage, expected *PublicationInitialLyricsProjection) bool {
	if expected == nil {
		return len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
	}
	var actual PublicationInitialLyricsProjection
	if decodeStrictJSON(raw, &actual) != nil || actual.Version != PublicationInitialLyricsVersion || actual.Generation != expected.Generation ||
		actual.Source != expected.Source || actual.SHA256 != expected.SHA256 || actual.Outcome != expected.Outcome ||
		actual.ResolutionWarningCode != expected.ResolutionWarningCode || actual.OutcomeWarningCode != expected.OutcomeWarningCode ||
		actual.AuthorityMetadataRevision != expected.AuthorityMetadataRevision || actual.AuthorityState != expected.AuthorityState ||
		actual.Authority != expected.Authority || actual.AuthorityProvenance != expected.AuthorityProvenance || actual.AuthorityFingerprintSHA256 != expected.AuthorityFingerprintSHA256 {
		return false
	}
	return true
}

func validPublicationInitialLyricsProjection(projection PublicationInitialLyricsProjection, generation uuid.UUID) bool {
	return projection.Version == PublicationInitialLyricsVersion && projection.Generation == generation &&
		(projection.Source == "none" && projection.SHA256 == "" || (projection.Source == "source" || projection.Source == "lrclib") && validSHA256(projection.SHA256)) &&
		(projection.Outcome == PublicationInitialLyricsNotRequested || projection.Outcome == PublicationOptionalLyricsExactRoundTrip || projection.Outcome == PublicationOptionalLyricsRebuiltWithoutLyrics) &&
		projection.AuthorityMetadataRevision >= 0 && validInitialLyricsWarning(projection.ResolutionWarningCode) &&
		(projection.OutcomeWarningCode == "" || projection.Outcome == PublicationOptionalLyricsRebuiltWithoutLyrics && projection.OutcomeWarningCode == "readback_mismatch")
}

// AcquisitionRecord is durable evidence of the bounded automatic resolver, not
// authority to override the frozen local-metadata value.
type PublicationInitialLyricsAcquisitionRecord struct {
	Version     int                           `json:"version"`
	Kind        string                        `json:"kind"`
	Guard       PublicationDeliveryGuard      `json:"guard"`
	Authority   PublicationInitialLyricsInput `json:"authority"`
	Source      string                        `json:"source"`
	Lyrics      string                        `json:"lyrics"`
	SHA256      string                        `json:"sha256"`
	WarningCode string                        `json:"warning_code"`
}

// OutcomeRecord is independent readback evidence for the final private output.
type PublicationInitialLyricsOutcomeRecord struct {
	Version          int                          `json:"version"`
	Kind             string                       `json:"kind"`
	Guard            PublicationDeliveryGuard     `json:"guard"`
	Outcome          string                       `json:"outcome"`
	Preparation      PublicationPreparationRecord `json:"preparation"`
	InputIdentity    PublicationFileIdentity      `json:"input_identity"`
	OutputIdentity   PublicationFileIdentity      `json:"output_identity"`
	FinalObservation PublicationStageObservation  `json:"final_observation"`
	WarningCode      string                       `json:"warning_code"`
}

// GuardedRecordPublicationInitialLyricsAcquisition records an acquisition
// before a candidate rebuild or final output observation. Replays are identity
// results only and never authorize another filesystem mutation.
func (repository *SetupManagerRepository) GuardedRecordPublicationInitialLyricsAcquisition(ctx context.Context, guard PublicationDeliveryGuard, record PublicationInitialLyricsAcquisitionRecord) (PublicationOptionalLyricsOutcome, error) {
	return repository.recordInitialLyricsJournal(ctx, guard, "record initial lyrics acquisition", func(attempt *TrackPublicationAttempt, input PublicationInputs) (json.RawMessage, bool, error) {
		if !validInitialLyricsAcquisition(record, guard, input) {
			return nil, false, errors.New("invalid initial lyrics acquisition record")
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return nil, false, err
		}
		state, err := parseInitialLyricsPrefix(entries, guard, input)
		if err != nil {
			return nil, false, err
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return nil, false, err
		}
		if state.acquisition != nil {
			if !sameJSONValue(initialLyricsJSON(state.acquisition), encoded) {
				return nil, false, errors.New("conflicting initial lyrics acquisition")
			}
			return nil, false, nil
		}
		if state.preparation == nil || state.preparation.Step != "input_ready" || state.outcome != nil {
			return nil, false, errors.New("initial lyrics acquisition is out of order")
		}
		journal, err := appendJournalEntry(attempt.MutationJournal, record)
		return journal, err == nil, err
	})
}

// GuardedRecordPublicationInitialLyricsRebuild adds the existing rebuild record
// only after initial lyrics acquisition has been durably recorded.
func (repository *SetupManagerRepository) GuardedRecordPublicationInitialLyricsRebuild(ctx context.Context, guard PublicationDeliveryGuard, record PublicationCandidateRebuildRecord) (PublicationOptionalLyricsOutcome, error) {
	return repository.recordInitialLyricsJournal(ctx, guard, "record initial lyrics rebuild", func(attempt *TrackPublicationAttempt, input PublicationInputs) (json.RawMessage, bool, error) {
		if !validCandidateRebuildRecord(record, guard) {
			return nil, false, errors.New("invalid initial lyrics candidate rebuild record")
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return nil, false, err
		}
		state, err := parseInitialLyricsPrefix(entries, guard, input)
		if err != nil {
			return nil, false, err
		}
		if state.acquisition == nil || state.outcome != nil {
			return nil, false, errors.New("initial lyrics rebuild is out of order")
		}
		for _, prior := range state.rebuilds {
			if prior.Step == record.Step {
				if sameJSONValue(initialLyricsJSON(prior), initialLyricsJSON(record)) {
					return nil, false, nil
				}
				return nil, false, errors.New("conflicting initial lyrics rebuild record")
			}
		}
		if len(state.rebuilds) == 0 && record.Step != "intent" || len(state.rebuilds) == 1 && (record.Step != "complete" || !sameCandidateRebuildPlan(state.rebuilds[0], record)) {
			return nil, false, errors.New("initial lyrics rebuild record is out of order")
		}
		journal, err := appendJournalEntry(attempt.MutationJournal, record)
		return journal, err == nil, err
	})
}

// GuardedRecordPublicationInitialLyricsOutcome records validated exact readback.
func (repository *SetupManagerRepository) GuardedRecordPublicationInitialLyricsOutcome(ctx context.Context, guard PublicationDeliveryGuard, record PublicationInitialLyricsOutcomeRecord) (PublicationOptionalLyricsOutcome, error) {
	return repository.recordInitialLyricsJournal(ctx, guard, "record initial lyrics outcome", func(attempt *TrackPublicationAttempt, input PublicationInputs) (json.RawMessage, bool, error) {
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return nil, false, err
		}
		state, err := parseInitialLyricsPrefix(entries, guard, input)
		if err != nil {
			return nil, false, err
		}
		if state.acquisition == nil || state.preparation == nil || state.preparation.Step != "input_ready" {
			return nil, false, errors.New("initial lyrics outcome lacks completed acquisition and preparation")
		}
		if !validInitialLyricsOutcome(record, *state.acquisition, guard, input) {
			return nil, false, errors.New("invalid initial lyrics outcome record")
		}
		if state.outcome != nil {
			if sameJSONValue(initialLyricsJSON(state.outcome), initialLyricsJSON(record)) {
				return nil, false, nil
			}
			return nil, false, errors.New("conflicting initial lyrics outcome")
		}
		journal, err := appendJournalEntry(attempt.MutationJournal, record)
		return journal, err == nil, err
	})
}

type initialLyricsJournalAppender func(*TrackPublicationAttempt, PublicationInputs) (json.RawMessage, bool, error)

func (repository *SetupManagerRepository) recordInitialLyricsJournal(ctx context.Context, guard PublicationDeliveryGuard, action string, appendRecord initialLyricsJournalAppender) (PublicationOptionalLyricsOutcome, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return PublicationOptionalLyricsStale, err
	}
	outcome := PublicationOptionalLyricsStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, _, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, action)
		if err != nil || !live || attempt.Phase != "preparing" || operation.Stage != "preparing" || attempt.IntentKind != "audio" || attempt.ExpectedGeneration != nil {
			return err
		}
		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil {
			return fmt.Errorf("%s: decode frozen inputs: %w", action, err)
		}
		if input.IntentKind != "audio" || input.InitialLyrics == nil || !publicationInitialLyricsGuardMatches(guard, input, attempt) {
			return errors.New("initial lyrics record does not match frozen first-publication authority")
		}
		journal, append, err := appendRecord(attempt, input)
		if err != nil {
			return fmt.Errorf("%s: %w", action, err)
		}
		if !append {
			outcome = PublicationOptionalLyricsAlreadyRecorded
			return nil
		}
		entries, err := decodeJournal(journal)
		if err != nil {
			return fmt.Errorf("%s: decode proposed journal: %w", action, err)
		}
		if _, err := parseInitialLyricsPrefix(entries, guard, input); err != nil {
			return fmt.Errorf("%s: proposed journal violates initial lyrics prefix: %w", action, err)
		}
		result, err := tx.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=? AND phase='preparing'`, string(journal), guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("%s: update attempt: %w", action, err)
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return fmt.Errorf("%s: attempt changed concurrently", action)
		}
		outcome = PublicationOptionalLyricsRecorded
		return nil
	})
	return outcome, err
}

func publicationInitialLyricsGuardMatches(guard PublicationDeliveryGuard, input PublicationInputs, attempt *TrackPublicationAttempt) bool {
	return input.Version == PublicationInputsVersion && input.InitialLyrics != nil && input.IntentKind == "audio" &&
		attempt.OperationID == guard.OperationID && attempt.Epoch == guard.Epoch &&
		attempt.CandidateGeneration == guard.CandidateGeneration && attempt.PositionID == guard.ExpectedPositionID && attempt.ExpectedGeneration == nil
}

func validInitialLyricsAcquisition(record PublicationInitialLyricsAcquisitionRecord, guard PublicationDeliveryGuard, input PublicationInputs) bool {
	if record.Version != PublicationInitialLyricsVersion || record.Kind != PublicationInitialLyricsAcquisitionKind || record.Guard != guard ||
		input.InitialLyrics == nil || !sameInitialLyricsAuthority(record.Authority, *input.InitialLyrics) {
		return false
	}
	// Manual authority and explicit clearing suppress automatic acquisition.
	if field := record.Authority.Desired; field != nil && field.State != "absent" && (field.Authority == "manual" || field.State == "explicit_empty") {
		return record.Source == "none" && record.Lyrics == "" && record.SHA256 == "" && validInitialLyricsWarning(record.WarningCode)
	}
	if record.Source == "none" {
		return record.Lyrics == "" && record.SHA256 == "" && validInitialLyricsWarning(record.WarningCode)
	}
	if record.WarningCode != "" || record.Source != "source" && record.Source != "lrclib" || record.Lyrics == "" {
		return false
	}
	lyrics, err := lrclib.ValidateSyncedLyrics(record.Lyrics)
	if err != nil || lyrics != record.Lyrics {
		return false
	}
	digest := sha256.Sum256([]byte(record.Lyrics))
	return record.SHA256 == hex.EncodeToString(digest[:])
}

func validInitialLyricsWarning(code string) bool {
	switch code {
	case "", "lyrics_source_selection_required", "lyrics_settings_unavailable", "lyrics_lookup_metadata_incomplete", "lyrics_provider_unavailable", "lyrics_lookup_failed", "lyrics_lookup_invalid_synced_text":
		return true
	default:
		return false
	}
}

func sameInitialLyricsAuthority(a, b PublicationInitialLyricsInput) bool {
	return a.ExpectedMetadataRevision == b.ExpectedMetadataRevision && sameJSONValue(initialLyricsJSON(a.Desired), initialLyricsJSON(b.Desired))
}

func validInitialLyricsOutcome(record PublicationInitialLyricsOutcomeRecord, acquisition PublicationInitialLyricsAcquisitionRecord, guard PublicationDeliveryGuard, input PublicationInputs) bool {
	if record.Version != PublicationInitialLyricsVersion || record.Kind != PublicationInitialLyricsOutcomeKind || record.Guard != guard ||
		!validPublicationPreparationRecord(record.Preparation, guard) || record.Preparation.Step != "input_ready" ||
		record.Preparation.CopiedInput == nil || record.Preparation.DirectoryIdentity == nil ||
		!validCopiedInputIdentity(&record.InputIdentity) || !validCopiedInputIdentity(&record.OutputIdentity) || !validSHA256(record.InputIdentity.SHA256) || !validSHA256(record.OutputIdentity.SHA256) ||
		!validPublicationStageObservation(record.FinalObservation) || record.FinalObservation.RelativePath != record.Preparation.OutputRel ||
		record.FinalObservation.PayloadSHA256 != record.OutputIdentity.SHA256 || record.FinalObservation.Ownership.ExpectedSize == nil || *record.FinalObservation.Ownership.ExpectedSize != *record.OutputIdentity.SizeBytes ||
		record.FinalObservation.Ownership.ExpectedMTime == nil || *record.FinalObservation.Ownership.ExpectedMTime != *record.OutputIdentity.MTime ||
		record.FinalObservation.QualityPolicyVersion != input.QualityPolicyVersion || len(record.FinalObservation.ActualAssets) == 0 {
		return false
	}
	prep := record.Preparation
	if prep.OperationID != guard.OperationID || prep.ClaimUUID != guard.ClaimUUID || prep.InputRel == prep.OutputRel ||
		!sameFileIdentity(*prep.CopiedInput, record.InputIdentity) || prep.InputSize == nil || *prep.InputSize != *record.InputIdentity.SizeBytes ||
		prep.InputMTime == nil {
		return false
	}
	var metadata PublicationCommitMetadata
	if strictMetadata(record.FinalObservation.ActualMetadata, &metadata) != nil {
		return false
	}
	assets, err := DecodePublicationAssetOutcomes(record.FinalObservation.ActualAssets)
	if err != nil || assets.ValidateAgainstMetadata(metadata.Tags, nil) != nil {
		return false
	}
	for name, values := range input.DesiredTags {
		if !sameJSONValue(initialLyricsJSON(metadata.Tags[name]), initialLyricsJSON(values)) {
			return false
		}
	}
	_, hasTag := metadata.Tags["LYRICS"]
	if acquisition.Source == "none" {
		return record.Outcome == PublicationInitialLyricsNotRequested && acquisition.Lyrics == "" && !hasTag && assets.Lyrics.State == PublicationAssetAbsent && record.WarningCode == ""
	}
	if record.Outcome == PublicationOptionalLyricsExactRoundTrip {
		digest := sha256.Sum256([]byte(acquisition.Lyrics))
		return record.WarningCode == "" && hasTag && len(metadata.Tags["LYRICS"]) == 1 && metadata.Tags["LYRICS"][0] == acquisition.Lyrics && assets.Lyrics.State == PublicationAssetEmbedded && assets.Lyrics.Format == "lrc" && assets.Lyrics.SHA256 == hex.EncodeToString(digest[:])
	}
	return record.Outcome == PublicationOptionalLyricsRebuiltWithoutLyrics && record.WarningCode == "readback_mismatch" && !hasTag && assets.Lyrics.State == PublicationAssetAbsent
}

func sameFileIdentity(a, b PublicationFileIdentity) bool {
	return sameJSONValue(initialLyricsJSON(a), initialLyricsJSON(b))
}

func initialLyricsJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}
