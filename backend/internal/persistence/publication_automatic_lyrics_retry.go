package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/uptrace/bun"
)

// AutomaticLyricsRetryAdmission couples a source-free rewrite to the reviewed
// current generation and metadata revision. FrozenInputs is reconstructed from
// locked current publication and local metadata facts.
type AutomaticLyricsRetryAdmission struct {
	PublicationAdmission
	ExpectedReleaseID        uuid.UUID
	ExpectedMetadataRevision int64
}

const automaticLyricsRetryVersion = 1

const (
	automaticLyricsRetryAcquisitionKind = "publication_automatic_lyrics_retry_acquisition"
	automaticLyricsRetryOutcomeKind     = "publication_automatic_lyrics_retry_outcome"
)

type PublicationAutomaticLyricsRetryAcquisition struct {
	Version        int                                  `json:"version"`
	Kind           string                               `json:"kind"`
	Guard          PublicationDeliveryGuard             `json:"guard"`
	Authority      PublicationAutomaticLyricsRetryInput `json:"authority"`
	Metadata       lrclib.Metadata                      `json:"metadata"`
	Classification string                               `json:"classification"`
	Source         string                               `json:"source"`
	Lyrics         string                               `json:"lyrics"`
	SHA256         string                               `json:"sha256"`
	Warning        string                               `json:"warning"`
}

type PublicationAutomaticLyricsRetryOutcome struct {
	Version          int                          `json:"version"`
	Kind             string                       `json:"kind"`
	Guard            PublicationDeliveryGuard     `json:"guard"`
	Outcome          string                       `json:"outcome"`
	Preparation      PublicationPreparationRecord `json:"preparation"`
	InputIdentity    PublicationFileIdentity      `json:"input_identity"`
	OutputIdentity   PublicationFileIdentity      `json:"output_identity"`
	FinalObservation PublicationStageObservation  `json:"final_observation"`
	Warning          string                       `json:"warning"`
}

type automaticLyricsRetryJournalState struct {
	preparation *PublicationPreparationRecord
	acquisition *PublicationAutomaticLyricsRetryAcquisition
	outcome     *PublicationAutomaticLyricsRetryOutcome
	stageCount  int
	rebuilds    []PublicationCandidateRebuildRecord
	exposure    *publicationExposureJournalRecord
	replacement []PublicationReplacementEvidence
	commit      *replacementPublicationCommitWitness
	prefixEnd   int
}

// parseAutomaticLyricsRetryPrefix accepts only a strict, ordered current retry
// journal. Unknown records/fields and records from another delivery are rejected.
func parseAutomaticLyricsRetryPrefix(entries []json.RawMessage, guard PublicationDeliveryGuard, input PublicationInputs) (automaticLyricsRetryJournalState, error) {
	var state automaticLyricsRetryJournalState
	if input.AutomaticLyricsRetry == nil || input.IntentKind != "rewrite" || input.Rewrite == nil {
		return state, errors.New("automatic lyrics retry journal requires retry rewrite inputs")
	}
	prepByStep := map[string]PublicationPreparationRecord{}
	phase := "preparation"
	for _, raw := range entries {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &header) != nil || header.Kind == "" {
			return state, errors.New("automatic lyrics retry journal entry is malformed")
		}
		switch header.Kind {
		case "publication_preparation":
			if phase != "preparation" {
				return state, errors.New("retry preparation appears after acquisition")
			}
			var record PublicationPreparationRecord
			if decodeStrictJSON(raw, &record) != nil || !validPreparationJournalRecord(record) || record.OperationID != guard.OperationID || record.ClaimUUID != guard.ClaimUUID {
				return state, errors.New("automatic lyrics retry preparation is invalid")
			}
			if prior, ok := prepByStep[record.Step]; ok {
				if !samePreparationRecord(prior, record) {
					return state, errors.New("retry preparation step conflicts")
				}
				return state, errors.New("duplicate retry preparation step")
			}
			switch record.Step {
			case "intent":
				if len(prepByStep) != 0 {
					return state, errors.New("retry preparation intent is misplaced")
				}
			case "directory_created":
				if prepByStep["intent"].Step != "intent" || record.DirectoryIdentity == nil {
					return state, errors.New("retry directory creation lacks intent")
				}
			case "input_ready":
				directory := prepByStep["directory_created"]
				if directory.Step != "directory_created" || directory.DirectoryIdentity == nil || record.DirectoryIdentity == nil || *directory.DirectoryIdentity != *record.DirectoryIdentity || record.CopiedInput == nil || !validSHA256(record.CopiedInput.SHA256) {
					return state, errors.New("retry input readiness lacks matching directory")
				}
			default:
				return state, errors.New("unknown retry preparation step")
			}
			prepByStep[record.Step] = record
			copy := record
			state.preparation = &copy
		case automaticLyricsRetryAcquisitionKind:
			if phase != "preparation" || state.preparation == nil || state.preparation.Step != "input_ready" {
				return state, errors.New("automatic lyrics retry acquisition is out of order")
			}
			var record PublicationAutomaticLyricsRetryAcquisition
			if decodeStrictJSON(raw, &record) != nil || !validAutomaticLyricsRetryAcquisition(record, guard, input) {
				return state, errors.New("automatic lyrics retry acquisition is invalid")
			}
			state.acquisition = &record
			phase = "acquired"
		case automaticLyricsRetryOutcomeKind:
			if phase != "acquired" || state.acquisition == nil || state.outcome != nil || len(state.rebuilds) == 1 {
				return state, errors.New("automatic lyrics retry outcome is out of order")
			}
			var record PublicationAutomaticLyricsRetryOutcome
			if decodeStrictJSON(raw, &record) != nil || !validAutomaticLyricsRetryOutcome(record, *state.acquisition, guard, input) || !samePreparationRecord(record.Preparation, *state.preparation) {
				return state, errors.New("automatic lyrics retry outcome is invalid")
			}
			if record.Outcome == PublicationOptionalLyricsRebuiltWithoutLyrics && len(state.rebuilds) != 2 || record.Outcome != PublicationOptionalLyricsRebuiltWithoutLyrics && len(state.rebuilds) != 0 {
				return state, errors.New("automatic lyrics retry outcome does not match rebuild evidence")
			}
			if len(state.rebuilds) == 2 && !sameFileIdentity(*state.rebuilds[1].Candidate, record.OutputIdentity) {
				return state, errors.New("automatic lyrics retry outcome differs from the acknowledged rebuilt candidate")
			}
			state.outcome = &record
			phase = "outcome"
		case "publication_candidate_rebuild":
			if phase != "acquired" || state.acquisition == nil || state.acquisition.Source != "lrclib" || len(state.rebuilds) >= 2 {
				return state, errors.New("automatic lyrics retry rebuild record is out of order")
			}
			var record PublicationCandidateRebuildRecord
			if decodeStrictJSON(raw, &record) != nil || !validCandidateRebuildRecord(record, guard) || state.preparation == nil || state.preparation.DirectoryIdentity == nil || state.preparation.CopiedInput == nil || record.OwnerToken != state.preparation.OwnerToken || record.DirectoryIdentity != *state.preparation.DirectoryIdentity || record.PrivateDirRel != state.preparation.PrivateDirRel || record.InputRel != state.preparation.InputRel || record.OutputRel != state.preparation.OutputRel || !sameFileIdentity(*record.Input, *state.preparation.CopiedInput) {
				return state, errors.New("automatic lyrics retry rebuild evidence is invalid")
			}
			if len(state.rebuilds) == 0 {
				if record.Step != "intent" {
					return state, errors.New("automatic lyrics retry rebuild completion lacks an intent")
				}
			} else if record.Step != "complete" || !sameCandidateRebuildPlan(state.rebuilds[0], record) {
				return state, errors.New("automatic lyrics retry rebuild completion does not match intent")
			}
			state.rebuilds = append(state.rebuilds, record)
		case "stage_observation":
			if phase != "outcome" || state.outcome == nil || state.stageCount >= 2 {
				return state, errors.New("retry stage observation is out of order")
			}
			var record struct {
				Version     int                         `json:"version"`
				Kind        string                      `json:"kind"`
				From        string                      `json:"from"`
				To          string                      `json:"to"`
				Observation PublicationStageObservation `json:"observation"`
			}
			if decodeStrictJSON(raw, &record) != nil || record.Version != 1 || record.Kind != "stage_observation" || record.From != []string{"preparing", "staged"}[state.stageCount] || record.To != []string{"staged", "validated"}[state.stageCount] || !validPublicationStageObservation(record.Observation) || !samePublicationStageObservation(record.Observation, state.outcome.FinalObservation) {
				return state, errors.New("automatic lyrics retry stage observation is invalid")
			}
			state.stageCount++
		case "publication_exposure":
			if phase != "outcome" || state.outcome == nil || state.stageCount != 2 || state.exposure != nil {
				return state, errors.New("automatic lyrics retry exposure is out of order")
			}
			var record publicationExposureJournalRecord
			if decodeStrictJSON(raw, &record) != nil || record.Version != PublicationExposureVersion || record.Kind != "publication_exposure" || record.From != "validated" || record.To != "exposing" || record.Guard != guard || record.CandidateGeneration != guard.CandidateGeneration || record.ExpectedGeneration == nil || *record.ExpectedGeneration != input.Rewrite.CurrentGeneration || !validExposureIntent(record.Intent) || record.Intent.Expectation != "expected_current" || !samePublicationStageObservation(record.Observation, state.outcome.FinalObservation) {
				return state, errors.New("automatic lyrics retry exposure is invalid")
			}
			copy := record
			state.exposure = &copy
			phase = "exposing"
		case "publication_replacement":
			if phase != "exposing" || state.exposure == nil || len(state.replacement) >= 4 {
				return state, errors.New("automatic lyrics retry replacement is out of order")
			}
			var record PublicationReplacementEvidence
			if decodeStrictJSON(raw, &record) != nil || !validPublicationReplacementEvidence(record) {
				return state, errors.New("automatic lyrics retry replacement is invalid")
			}
			idx := len(state.replacement)
			fromTo := [][2]string{{"exposing", "backup_intent"}, {"backup_intent", "backup_ready"}, {"backup_ready", "replacement_intent"}, {"replacement_intent", "installed"}}
			if record.From != fromTo[idx][0] || record.To != fromTo[idx][1] || record.Guard != guard || record.PositionID != guard.ExpectedPositionID || record.Epoch != guard.Epoch || record.CandidateGeneration != guard.CandidateGeneration || record.ExpectedGeneration == nil || *record.ExpectedGeneration != input.Rewrite.CurrentGeneration || record.Intent != state.exposure.Intent || !samePublicationStageObservation(record.Staged, state.exposure.Observation) {
				return state, errors.New("automatic lyrics retry replacement does not match exposure")
			}
			if idx > 0 && !validReplacementSuccessor(&state.replacement[idx-1], &record) {
				return state, errors.New("automatic lyrics retry replacement chain is broken")
			}
			state.replacement = append(state.replacement, record)
		case "publication_replacement_commit":
			if phase != "exposing" || state.exposure == nil || len(state.replacement) != 4 || state.commit != nil {
				return state, errors.New("automatic lyrics retry commit is out of order")
			}
			var record replacementPublicationCommitWitness
			if decodeStrictJSON(raw, &record) != nil || !validAutomaticLyricsRetryCommit(record, state, guard, input) {
				return state, errors.New("automatic lyrics retry commit witness is invalid")
			}
			state.commit = &record
			state.prefixEnd = len(entries)
			phase = "committed"
		default:
			return state, fmt.Errorf("unknown automatic lyrics retry journal kind %q", header.Kind)
		}
	}
	return state, nil
}

func validAutomaticLyricsRetryCommit(record replacementPublicationCommitWitness, state automaticLyricsRetryJournalState, guard PublicationDeliveryGuard, input PublicationInputs) bool {
	commitAttempt := &TrackPublicationAttempt{PositionID: guard.ExpectedPositionID, Epoch: guard.Epoch, CandidateGeneration: guard.CandidateGeneration}
	if record.Version != replacementPublicationCommitVersion || record.Kind != "publication_replacement_commit" || record.ManualLyricsReceipt != nil || record.Guard != guard || record.PositionID != guard.ExpectedPositionID || record.Epoch != guard.Epoch || record.ExpectedGeneration != input.Rewrite.CurrentGeneration || record.CandidateGeneration != guard.CandidateGeneration || !currentIsCandidate(record.Projection, commitAttempt) {
		return false
	}
	last := state.replacement[3]
	var ownership PublicationCurrentOwnership
	if decodePublicationCurrentContract(record.Projection.Ownership, &ownership) != nil || ownership.Version != 1 || !samePublicationExposureJSON(ownership.Identity, last.Identity) || !sameJSONValue(record.Projection.ActualMetadata, state.exposure.Observation.ActualMetadata) || !sameJSONValue(record.Projection.ActualAssets, state.exposure.Observation.ActualAssets) || record.Projection.RelativePath != state.exposure.Intent.TargetRelativePath || record.Projection.QualityPolicyVersion != input.QualityPolicyVersion {
		return false
	}
	var old TrackPublication
	if json.Unmarshal(state.exposure.Current, &old) != nil || old.Generation != input.Rewrite.CurrentGeneration || !sameJSONValue(record.Projection.ActualAudio, old.ActualAudio) {
		return false
	}
	var oldMetadata PublicationCommitMetadata
	var newMetadata PublicationCommitMetadata
	if strictMetadata(old.ActualMetadata, &oldMetadata) != nil || strictMetadata(record.Projection.ActualMetadata, &newMetadata) != nil || !sameAutomaticLyricsRetryMandatoryTags(oldMetadata.Tags, newMetadata.Tags) {
		return false
	}
	projection, err := automaticLyricsRetryProjection(*state.acquisition, *state.outcome, guard.CandidateGeneration)
	return err == nil && sameInitialLyricsProjectionJSON(encodePublicationCommitJSON(record.Projection.InitialLyrics), projection)
}

func automaticLyricsRetryProjection(acquisition PublicationAutomaticLyricsRetryAcquisition, outcome PublicationAutomaticLyricsRetryOutcome, generation uuid.UUID) (*PublicationInitialLyricsProjection, error) {
	if generation == uuid.Nil || acquisition.Authority.ExpectedMetadataRevision < 0 {
		return nil, errors.New("invalid automatic lyrics retry projection authority")
	}
	if acquisition.Source == "none" && outcome.Outcome == PublicationAutomaticLyricsRetryNoResult {
		// Keep no-result distinct from an initial publication that never requested lyrics.
	} else if acquisition.Source != "lrclib" || outcome.Outcome != PublicationOptionalLyricsExactRoundTrip && outcome.Outcome != PublicationOptionalLyricsRebuiltWithoutLyrics {
		return nil, errors.New("automatic lyrics retry projection outcome does not match acquisition")
	}
	projection := &PublicationInitialLyricsProjection{Version: PublicationInitialLyricsVersion, Generation: generation, Source: acquisition.Source, SHA256: acquisition.SHA256, Outcome: outcome.Outcome, ResolutionWarningCode: acquisition.Warning, OutcomeWarningCode: outcome.Warning, AuthorityMetadataRevision: acquisition.Authority.ExpectedMetadataRevision, AuthorityState: "absent", Authority: "automatic", AuthorityProvenance: "source_metadata"}
	if desired := acquisition.Authority.Desired; desired != nil {
		projection.AuthorityState = string(desired.State)
		projection.Authority = string(desired.Authority)
		projection.AuthorityProvenance = string(desired.Provenance.Kind)
		fingerprint := sha256.Sum256(mustJSON(desired))
		projection.AuthorityFingerprintSHA256 = hex.EncodeToString(fingerprint[:])
	}
	if !validPublicationInitialLyricsProjection(*projection, generation) {
		return nil, errors.New("invalid automatic lyrics retry projection evidence")
	}
	return projection, nil
}

func validAutomaticLyricsRetryAcquisition(record PublicationAutomaticLyricsRetryAcquisition, guard PublicationDeliveryGuard, input PublicationInputs) bool {
	if record.Version != automaticLyricsRetryVersion || record.Kind != automaticLyricsRetryAcquisitionKind || record.Guard != guard || !sameJSONValue(json.RawMessage(mustJSON(record.Authority)), json.RawMessage(mustJSON(*input.AutomaticLyricsRetry))) || math.IsNaN(record.Metadata.Duration) || math.IsInf(record.Metadata.Duration, 0) || record.Metadata.Duration < 0 {
		return false
	}
	if record.Source == "none" {
		validClass := record.Classification == "no_result" && record.Warning == "" || record.Classification == "disabled" && record.Warning == "" || record.Classification == "metadata_incomplete" && record.Warning == "lyrics_lookup_metadata_incomplete" || record.Classification == "lookup_failed" && record.Warning == "lyrics_lookup_failed" || record.Classification == "invalid_synced_text" && record.Warning == "lyrics_lookup_invalid_synced_text"
		return validClass && record.Lyrics == "" && record.SHA256 == ""
	}
	if record.Source != "lrclib" || record.Classification != "found" || record.Warning != "" || record.Lyrics == "" || record.Metadata.Duration <= 0 {
		return false
	}
	lyrics, err := lrclib.ValidateSyncedLyrics(record.Lyrics)
	digest := sha256.Sum256([]byte(record.Lyrics))
	return err == nil && lyrics == record.Lyrics && record.SHA256 == hex.EncodeToString(digest[:])
}

func validAutomaticLyricsRetryOutcome(record PublicationAutomaticLyricsRetryOutcome, acquisition PublicationAutomaticLyricsRetryAcquisition, guard PublicationDeliveryGuard, input PublicationInputs) bool {
	if record.Version != automaticLyricsRetryVersion || record.Kind != automaticLyricsRetryOutcomeKind || record.Guard != guard || !validPublicationPreparationRecord(record.Preparation, guard) || record.Preparation.Step != "input_ready" || record.Preparation.CopiedInput == nil || !validCopiedInputIdentity(&record.InputIdentity) || !validCopiedInputIdentity(&record.OutputIdentity) || !validPublicationStageObservation(record.FinalObservation) || record.FinalObservation.RelativePath != record.Preparation.OutputRel || record.FinalObservation.PayloadSHA256 != record.OutputIdentity.SHA256 || !sameFileIdentity(*record.Preparation.CopiedInput, record.InputIdentity) || record.FinalObservation.QualityPolicyVersion != input.QualityPolicyVersion {
		return false
	}
	var metadata PublicationCommitMetadata
	if strictMetadata(record.FinalObservation.ActualMetadata, &metadata) != nil {
		return false
	}
	assets, err := DecodePublicationAssetOutcomes(record.FinalObservation.ActualAssets)
	if err != nil || !sameJSONValue(json.RawMessage(mustJSON(assets.Cover)), json.RawMessage(mustJSON(input.AutomaticLyricsRetry.CurrentAssets.Cover))) {
		return false
	}
	if !sameAutomaticLyricsRetryMandatoryTags(input.DesiredTags, metadata.Tags) {
		return false
	}
	lyricsValues, hasLyrics := automaticLyricsRetryTagValues(metadata.Tags, "LYRICS")
	if acquisition.Source == "lrclib" && record.Outcome == PublicationOptionalLyricsExactRoundTrip {
		return record.Warning == "" && hasLyrics && len(lyricsValues) == 1 && lyricsValues[0] == acquisition.Lyrics && assets.Lyrics.State == PublicationAssetEmbedded && assets.Lyrics.SHA256 == acquisition.SHA256
	}
	if acquisition.Source == "lrclib" && record.Outcome == PublicationOptionalLyricsRebuiltWithoutLyrics {
		return record.Warning == "readback_mismatch" && !hasLyrics && assets.Lyrics.State == PublicationAssetAbsent
	}
	return acquisition.Source == "none" && record.Outcome == PublicationAutomaticLyricsRetryNoResult && !hasLyrics && assets.Lyrics.State == PublicationAssetAbsent && record.Warning == ""
}

func sameAutomaticLyricsRetryMandatoryTags(expected, actual map[string][]string) bool {
	canonical := func(tags map[string][]string) (map[string][]string, bool) {
		result := make(map[string][]string, len(tags))
		for name, values := range tags {
			key := strings.ToUpper(strings.TrimSpace(name))
			if key == "LYRICS" {
				continue
			}
			if key == "" {
				return nil, false
			}
			if _, duplicate := result[key]; duplicate {
				return nil, false
			}
			result[key] = append([]string(nil), values...)
		}
		return result, true
	}
	want, wantOK := canonical(expected)
	got, gotOK := canonical(actual)
	return wantOK && gotOK && sameJSONValue(json.RawMessage(mustJSON(want)), json.RawMessage(mustJSON(got)))
}

func automaticLyricsRetryTagValues(tags map[string][]string, target string) ([]string, bool) {
	var values []string
	found := false
	for name, current := range tags {
		if strings.EqualFold(strings.TrimSpace(name), target) {
			if found {
				return nil, false
			}
			found = true
			values = current
		}
	}
	return values, found
}

func (repository *SetupManagerRepository) recordAutomaticLyricsRetry(ctx context.Context, guard PublicationDeliveryGuard, action string, appendRecord func(*TrackPublicationAttempt, PublicationInputs, []json.RawMessage) (json.RawMessage, bool, error)) (PublicationOptionalLyricsOutcome, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return PublicationOptionalLyricsStale, err
	}
	outcome := PublicationOptionalLyricsStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, _, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, action)
		if err != nil || !live || attempt.Phase != "preparing" || operation.Stage != "preparing" || attempt.IntentKind != "rewrite" {
			return err
		}
		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil {
			return fmt.Errorf("%s: decode frozen inputs: %w", action, err)
		}
		if input.AutomaticLyricsRetry == nil || input.ManualLyrics != nil || input.InitialLyrics != nil || attempt.ExpectedGeneration == nil || !publicationRewriteMatchesRetryGuard(guard, input, attempt) {
			return errors.New("automatic lyrics retry journal does not match frozen attempt")
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return err
		}
		journal, append, err := appendRecord(attempt, input, entries)
		if err != nil {
			return fmt.Errorf("%s: %w", action, err)
		}
		if !append {
			outcome = PublicationOptionalLyricsAlreadyRecorded
			return nil
		}
		proposed, err := decodeJournal(journal)
		if err != nil {
			return err
		}
		if _, err := parseAutomaticLyricsRetryPrefix(proposed, guard, input); err != nil {
			return fmt.Errorf("%s: proposed journal is invalid: %w", action, err)
		}
		result, err := tx.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=? AND phase='preparing'`, string(journal), guard.OperationID).Exec(ctx)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return errors.New("automatic lyrics retry journal attempt changed concurrently")
		}
		outcome = PublicationOptionalLyricsRecorded
		return nil
	})
	return outcome, err
}

func publicationRewriteMatchesRetryGuard(guard PublicationDeliveryGuard, input PublicationInputs, attempt *TrackPublicationAttempt) bool {
	return input.Version == PublicationInputsVersion && input.Rewrite != nil && input.AutomaticLyricsRetry != nil && input.IntentKind == "rewrite" && attempt.OperationID == guard.OperationID && attempt.Epoch == guard.Epoch && attempt.CandidateGeneration == guard.CandidateGeneration && attempt.PositionID == guard.ExpectedPositionID && attempt.ExpectedGeneration != nil && *attempt.ExpectedGeneration == input.Rewrite.CurrentGeneration
}

func (repository *SetupManagerRepository) GuardedRecordAutomaticLyricsRetryAcquisition(ctx context.Context, guard PublicationDeliveryGuard, record PublicationAutomaticLyricsRetryAcquisition) (PublicationOptionalLyricsOutcome, error) {
	return repository.recordAutomaticLyricsRetry(ctx, guard, "record automatic lyrics retry acquisition", func(_ *TrackPublicationAttempt, input PublicationInputs, entries []json.RawMessage) (json.RawMessage, bool, error) {
		if !validAutomaticLyricsRetryAcquisition(record, guard, input) {
			return nil, false, errors.New("invalid automatic lyrics retry acquisition")
		}
		state, err := parseAutomaticLyricsRetryPrefix(entries, guard, input)
		if err != nil {
			return nil, false, err
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return nil, false, err
		}
		if state.acquisition != nil {
			if sameJSONValue(json.RawMessage(mustJSON(*state.acquisition)), json.RawMessage(mustJSON(record))) {
				return nil, false, nil
			}
			return nil, false, errors.New("conflicting automatic lyrics retry acquisition")
		}
		journal, err := appendJournalEntry(json.RawMessage(mustJSON(entries)), json.RawMessage(encoded))
		return journal, err == nil, err
	})
}

func (repository *SetupManagerRepository) GuardedRecordAutomaticLyricsRetryOutcome(ctx context.Context, guard PublicationDeliveryGuard, record PublicationAutomaticLyricsRetryOutcome) (PublicationOptionalLyricsOutcome, error) {
	return repository.recordAutomaticLyricsRetry(ctx, guard, "record automatic lyrics retry outcome", func(_ *TrackPublicationAttempt, input PublicationInputs, entries []json.RawMessage) (json.RawMessage, bool, error) {
		state, err := parseAutomaticLyricsRetryPrefix(entries, guard, input)
		if err != nil {
			return nil, false, err
		}
		if state.acquisition == nil || state.preparation == nil || !validAutomaticLyricsRetryOutcome(record, *state.acquisition, guard, input) || !samePreparationRecord(record.Preparation, *state.preparation) {
			return nil, false, errors.New("automatic lyrics retry outcome lacks matching acquisition/preparation")
		}
		if state.outcome != nil {
			if sameJSONValue(json.RawMessage(mustJSON(*state.outcome)), json.RawMessage(mustJSON(record))) {
				return nil, false, nil
			}
			return nil, false, errors.New("conflicting automatic lyrics retry outcome")
		}
		journal, err := appendJournalEntry(json.RawMessage(mustJSON(entries)), record)
		return journal, err == nil, err
	})
}

// GuardedRecordAutomaticLyricsRetryRebuild accepts only a new exact rebuild
// record. An already-present intent is evidence for recovery, never replay authority.
func (repository *SetupManagerRepository) GuardedRecordAutomaticLyricsRetryRebuild(ctx context.Context, guard PublicationDeliveryGuard, record PublicationCandidateRebuildRecord) (PublicationOptionalLyricsOutcome, error) {
	return repository.recordAutomaticLyricsRetry(ctx, guard, "record automatic lyrics retry rebuild", func(_ *TrackPublicationAttempt, input PublicationInputs, entries []json.RawMessage) (json.RawMessage, bool, error) {
		state, err := parseAutomaticLyricsRetryPrefix(entries, guard, input)
		if err != nil {
			return nil, false, err
		}
		if state.acquisition == nil || state.acquisition.Source != "lrclib" || state.outcome != nil || !validCandidateRebuildRecord(record, guard) || state.preparation == nil || state.preparation.Step != "input_ready" || state.preparation.DirectoryIdentity == nil || state.preparation.CopiedInput == nil || record.OwnerToken != state.preparation.OwnerToken || record.DirectoryIdentity != *state.preparation.DirectoryIdentity || record.PrivateDirRel != state.preparation.PrivateDirRel || record.InputRel != state.preparation.InputRel || record.OutputRel != state.preparation.OutputRel || !sameFileIdentity(*record.Input, *state.preparation.CopiedInput) {
			return nil, false, errors.New("automatic lyrics retry rebuild does not match prepared owner")
		}
		if len(state.rebuilds) == 0 && record.Step != "intent" || len(state.rebuilds) == 1 && (record.Step != "complete" || !sameCandidateRebuildPlan(state.rebuilds[0], record)) || len(state.rebuilds) > 1 {
			return nil, false, errors.New("automatic lyrics retry rebuild record is out of order")
		}
		journal, err := appendJournalEntry(json.RawMessage(mustJSON(entries)), record)
		return journal, err == nil, err
	})
}

func validateAutomaticLyricsRetryCurrentAuthority(ctx context.Context, tx bun.Tx, positionID uuid.UUID, authority PublicationAutomaticLyricsRetryInput) (bool, error) {
	var raw []byte
	var revision int64
	if err := tx.NewRaw(`SELECT metadata,metadata_revision FROM release_track WHERE id=? FOR UPDATE`, positionID).Scan(ctx, &raw, &revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("lock current automatic lyrics retry authority: %w", err)
	}
	if revision != authority.ExpectedMetadataRevision {
		return false, nil
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil || root == nil {
		return false, nil
	}
	var envelope librarymetadata.Envelope
	if local := root["local_metadata"]; len(local) == 0 {
		envelope = librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{}}
	} else {
		var err error
		envelope, err = librarymetadata.DecodeEnvelope(local)
		if err != nil {
			return false, nil
		}
	}
	if envelope.Scope != librarymetadata.ScopePosition {
		return false, nil
	}
	var current *librarymetadata.FieldValue
	if field, exists := envelope.Desired["synced_lyrics"]; exists {
		copy := field
		copy.Value = clonePublicationJSONValue(field.Value)
		current = &copy
	}
	return sameJSONValue(json.RawMessage(mustJSON(current)), json.RawMessage(mustJSON(authority.Desired))), nil
}

// AdmitAutomaticLyricsRetry is the sole admission path for an automatic lyrics
// retry. It atomically inserts the operation, attempt and River delivery.
func (repository *SetupManagerRepository) AdmitAutomaticLyricsRetry(_ context.Context, request AutomaticLyricsRetryAdmission, _ RiverInserter) (uuid.UUID, error) {
	input, err := DecodePublicationInputs(request.FrozenInputs)
	if err != nil {
		return uuid.Nil, fmt.Errorf("admit automatic lyrics retry: %w", err)
	}
	if input.Version != PublicationInputsVersion || input.IntentKind != "rewrite" || input.AutomaticLyricsRetry == nil || request.IntentKind != "rewrite" || request.Source != nil || request.ExpectedReleaseID == uuid.Nil {
		return uuid.Nil, errors.New("admit automatic lyrics retry: source-free retry rewrite input is required")
	}
	if input.AutomaticLyricsRetry.ExpectedMetadataRevision != request.ExpectedMetadataRevision || input.AutomaticLyricsRetry.ConfirmedCurrentGeneration != input.Rewrite.CurrentGeneration {
		return uuid.Nil, fmt.Errorf("admit automatic lyrics retry: request conflicts with frozen authority: %w", ErrPublicationAdmissionConflict)
	}
	// Keep this capability dormant until executor, replacement commit and
	// recovery parsers all understand its journal grammar. No queued operation may
	// be created that the existing generic worker could misinterpret.
	return uuid.Nil, errors.New("admit automatic lyrics retry: execution is not yet available")
}

func validateAutomaticLyricsRetryAdmission(ctx context.Context, tx bun.Tx, request PublicationAdmission, input *PublicationInputs, current TrackPublication) error {
	retry := input.AutomaticLyricsRetry
	if retry == nil || retry.ExpectedMetadataRevision != request.metadataRevision || retry.ConfirmedCurrentGeneration != current.Generation {
		return fmt.Errorf("admit automatic lyrics retry: metadata revision or generation changed: %w", ErrPublicationAdmissionConflict)
	}
	var raw []byte
	var revision int64
	if err := tx.NewRaw(`SELECT metadata,metadata_revision FROM release_track WHERE id=? FOR UPDATE`, request.PositionID).Scan(ctx, &raw, &revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("admit automatic lyrics retry: position metadata not found: %w", ErrPublicationAdmissionConflict)
		}
		return fmt.Errorf("admit automatic lyrics retry: lock position metadata: %w", err)
	}
	if revision != request.metadataRevision {
		return fmt.Errorf("admit automatic lyrics retry: metadata revision changed: %w", ErrPublicationAdmissionConflict)
	}
	var currentMetadata publicationCurrentMetadata
	if err := decodePublicationCurrentContract(current.ActualMetadata, &currentMetadata); err != nil || currentMetadata.Version != 1 || currentMetadata.Tags == nil {
		return errors.New("admit automatic lyrics retry: current publication metadata is invalid")
	}
	mandatory := make(map[string][]string, len(currentMetadata.Tags))
	for name, values := range currentMetadata.Tags {
		if !strings.EqualFold(strings.TrimSpace(name), "LYRICS") {
			mandatory[name] = append(make([]string, 0, len(values)), values...)
		}
	}
	assets, err := DecodePublicationAssetOutcomes(current.ActualAssets)
	if err != nil {
		return fmt.Errorf("admit automatic lyrics retry: current publication assets are invalid: %w", err)
	}
	var metadataRoot map[string]json.RawMessage
	if err := json.Unmarshal(raw, &metadataRoot); err != nil || metadataRoot == nil {
		return errors.New("admit automatic lyrics retry: current metadata is invalid")
	}
	var envelope librarymetadata.Envelope
	if local := metadataRoot["local_metadata"]; len(local) != 0 {
		envelope, err = librarymetadata.DecodeEnvelope(local)
		if err != nil || envelope.Scope != librarymetadata.ScopePosition {
			return errors.New("admit automatic lyrics retry: current lyrics authority is invalid")
		}
	} else {
		envelope = librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{}}
	}
	var desired *librarymetadata.FieldValue
	if field, exists := envelope.Desired["synced_lyrics"]; exists {
		copy := field
		copy.Value = clonePublicationJSONValue(field.Value)
		desired = &copy
	}
	input.DesiredTags = mandatory
	retry.ExpectedMetadataRevision = revision
	retry.ConfirmedCurrentGeneration = current.Generation
	retry.Desired = desired
	retry.CurrentAssets = assets
	if err := input.Validate(); err != nil {
		return fmt.Errorf("admit automatic lyrics retry: current frozen contract is invalid: %w", err)
	}
	return nil
}
