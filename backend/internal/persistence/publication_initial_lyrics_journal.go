package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type initialLyricsJournalState struct {
	preparation *PublicationPreparationRecord
	acquisition *PublicationInitialLyricsAcquisitionRecord
	rebuilds    []PublicationCandidateRebuildRecord
	outcome     *PublicationInitialLyricsOutcomeRecord
	stageCount  int
	stagePhase  string
}

func sameInitialLyricsReadyPreparation(recorded, durable PublicationPreparationRecord, input PublicationFileIdentity) bool {
	return samePreparationRecord(recorded, durable) && recorded.Step == "input_ready" &&
		recorded.CopiedInput != nil && recorded.InputSize != nil && recorded.InputMTime != nil &&
		validSHA256(recorded.CopiedInput.SHA256) && sameFileIdentity(*recorded.CopiedInput, input) &&
		input.SizeBytes != nil && *recorded.InputSize == *input.SizeBytes
}

// parseInitialLyricsPrefix is intentionally fail-closed: every journal member
// must be a recognized, strictly decoded record in the publication prefix.
func parseInitialLyricsPrefix(entries []json.RawMessage, guard PublicationDeliveryGuard, input PublicationInputs) (initialLyricsJournalState, error) {
	var state initialLyricsJournalState
	preparations := make(map[string]PublicationPreparationRecord)
	phase := "preparation"
	stagePhase := "preparing"
	for index, raw := range entries {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &header) != nil || header.Kind == "" {
			return state, errors.New("initial lyrics mutation journal has malformed entry")
		}
		switch header.Kind {
		case "publication_preparation":
			if phase != "preparation" {
				return state, errors.New("preparation record appears after initial lyrics acquisition")
			}
			var record PublicationPreparationRecord
			if decodeStrictJSON(raw, &record) != nil || !validPreparationJournalRecord(record) || record.OperationID != guard.OperationID || record.ClaimUUID != guard.ClaimUUID {
				return state, errors.New("initial lyrics preparation journal record is invalid")
			}
			if state.preparation == nil {
				if record.Step != "intent" {
					return state, errors.New("initial lyrics preparation must begin with intent")
				}
				copy := record
				state.preparation = &copy
			} else if !samePreparationPlan(*state.preparation, record) {
				return state, errors.New("initial lyrics preparation plan changed")
			}
			if prior, ok := preparations[record.Step]; ok {
				if !samePreparationRecord(prior, record) {
					return state, errors.New("initial lyrics preparation step conflicts")
				}
				return state, errors.New("duplicate initial lyrics preparation step")
			}
			switch record.Step {
			case "intent":
				if len(preparations) != 0 {
					return state, errors.New("initial lyrics preparation intent is misplaced")
				}
			case "directory_created":
				if preparations["intent"].Step != "intent" || record.DirectoryIdentity == nil {
					return state, errors.New("initial lyrics directory creation lacks intent")
				}
			case "input_ready":
				directory := preparations["directory_created"]
				if directory.Step != "directory_created" || record.DirectoryIdentity == nil || directory.DirectoryIdentity == nil || *record.DirectoryIdentity != *directory.DirectoryIdentity || record.CopiedInput == nil || !validSHA256(record.CopiedInput.SHA256) {
					return state, errors.New("initial lyrics input readiness lacks matching directory")
				}
			default:
				return state, errors.New("unknown initial lyrics preparation step")
			}
			preparations[record.Step] = record
			copy := record
			state.preparation = &copy
		case PublicationInitialLyricsAcquisitionKind:
			if phase != "preparation" || state.preparation == nil || state.preparation.Step != "input_ready" || state.acquisition != nil {
				return state, errors.New("initial lyrics acquisition is out of order")
			}
			var record PublicationInitialLyricsAcquisitionRecord
			if decodeStrictJSON(raw, &record) != nil || !validInitialLyricsAcquisition(record, guard, input) {
				return state, errors.New("initial lyrics acquisition journal record is invalid")
			}
			state.acquisition = &record
			phase = "after_acquisition"
		case "publication_candidate_rebuild":
			if phase != "after_acquisition" || state.acquisition == nil || state.acquisition.Source == "none" || len(state.rebuilds) >= 2 {
				return state, errors.New("initial lyrics candidate rebuild is out of order")
			}
			var record PublicationCandidateRebuildRecord
			if decodeStrictJSON(raw, &record) != nil || !validCandidateRebuildRecord(record, guard) || state.preparation == nil || state.preparation.CopiedInput == nil || state.preparation.DirectoryIdentity == nil ||
				record.OwnerToken != state.preparation.OwnerToken || record.PrivateDirRel != state.preparation.PrivateDirRel || record.InputRel != state.preparation.InputRel || record.OutputRel != state.preparation.OutputRel ||
				record.DirectoryIdentity != *state.preparation.DirectoryIdentity || !sameFileIdentity(*record.Input, *state.preparation.CopiedInput) {
				return state, errors.New("initial lyrics candidate rebuild record is invalid")
			}
			if len(state.rebuilds) == 0 && record.Step != "intent" || len(state.rebuilds) == 1 && (record.Step != "complete" || !sameCandidateRebuildPlan(state.rebuilds[0], record)) {
				return state, errors.New("initial lyrics candidate rebuild pair is invalid")
			}
			state.rebuilds = append(state.rebuilds, record)
		case PublicationInitialLyricsOutcomeKind:
			if phase != "after_acquisition" || state.acquisition == nil || state.outcome != nil {
				return state, errors.New("initial lyrics outcome is out of order")
			}
			var record PublicationInitialLyricsOutcomeRecord
			if decodeStrictJSON(raw, &record) != nil || !validInitialLyricsOutcome(record, *state.acquisition, guard, input) || state.preparation == nil || !sameInitialLyricsReadyPreparation(record.Preparation, *state.preparation, record.InputIdentity) {
				return state, errors.New("initial lyrics outcome journal record is invalid")
			}
			if len(state.rebuilds) == 1 || record.Outcome == PublicationOptionalLyricsRebuiltWithoutLyrics && len(state.rebuilds) != 2 || record.Outcome == PublicationOptionalLyricsExactRoundTrip && len(state.rebuilds) != 0 {
				return state, errors.New("initial lyrics outcome does not match candidate rebuild evidence")
			}
			if record.Outcome == PublicationOptionalLyricsRebuiltWithoutLyrics && (state.acquisition.Source == "none" || !sameFileIdentity(*state.rebuilds[1].Candidate, record.OutputIdentity)) {
				return state, errors.New("initial lyrics rebuild candidate does not match final output identity")
			}
			state.outcome = &record
			phase = "after_outcome"
		case "stage_observation":
			if phase != "after_outcome" || state.outcome == nil {
				return state, errors.New("initial lyrics stage observation precedes outcome")
			}
			var record struct {
				Version     int                         `json:"version"`
				Kind        string                      `json:"kind"`
				From        string                      `json:"from"`
				To          string                      `json:"to"`
				Observation PublicationStageObservation `json:"observation"`
			}
			if decodeStrictJSON(raw, &record) != nil || record.Version != 1 || record.Kind != "stage_observation" || !validPublicationStageObservation(record.Observation) || record.From != stagePhase || record.To != nextPublicationStagePhase(stagePhase) {
				return state, errors.New("initial lyrics stage observation is invalid")
			}
			if !samePublicationStageObservation(record.Observation, state.outcome.FinalObservation) {
				return state, errors.New("initial lyrics stage observation differs from the durable outcome")
			}
			if record.From == "staged" && (record.Observation.QualityPolicyVersion != input.QualityPolicyVersion || len(record.Observation.ActualAssets) == 0) {
				return state, errors.New("initial lyrics validated stage lacks mandatory asset evidence")
			}
			stagePhase = record.To
			state.stagePhase = stagePhase
			state.stageCount++
		default:
			return state, fmt.Errorf("unknown initial lyrics mutation journal kind %q at %d", header.Kind, index)
		}
	}
	return state, nil
}

func firstPublicationInitialLyricsEvidence(entries []json.RawMessage, guard PublicationDeliveryGuard, input PublicationInputs) (initialLyricsJournalState, error) {
	end := len(entries)
	for index, raw := range entries {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &header) != nil || header.Kind == "" {
			return initialLyricsJournalState{}, errors.New("malformed publication journal record")
		}
		switch header.Kind {
		case "publication_preparation", PublicationInitialLyricsAcquisitionKind, "publication_candidate_rebuild", PublicationInitialLyricsOutcomeKind, "stage_observation":
		default:
			end = index
		}
		if end != len(entries) {
			break
		}
	}
	state, err := parseInitialLyricsPrefix(entries[:end], guard, input)
	if err != nil {
		return state, err
	}
	if state.preparation == nil || state.preparation.Step != "input_ready" || state.acquisition == nil || state.outcome == nil || state.stageCount != 2 || state.stagePhase != "validated" {
		return state, errors.New("first publication lacks complete initial lyrics evidence and stage chain")
	}
	// A detached evidence prefix may be followed only by records in the
	// established first-publication grammar. Do not silently accept unknown or
	// malformed suffix entries as if they were outside the evidence prefix.
	for _, raw := range entries[end:] {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &header) != nil {
			return state, errors.New("malformed publication journal suffix")
		}
		switch header.Kind {
		case "publication_exposure":
			var record publicationExposureJournalRecord
			if decodeStrictJSON(raw, &record) != nil || record.Version != PublicationExposureVersion || record.Kind != header.Kind {
				return state, errors.New("invalid publication exposure suffix")
			}
		case "publication_candidate_installed":
			var record publicationCandidateInstalledJournalRecord
			if decodeStrictJSON(raw, &record) != nil || !validPublicationCandidateInstalledRecord(record) {
				return state, errors.New("invalid installed-candidate suffix")
			}
		case "publication_commit":
			var record firstPublicationCommitWitness
			if decodeStrictJSON(raw, &record) != nil || record.Version != firstPublicationCommitVersion || record.Kind != header.Kind {
				return state, errors.New("invalid publication commit suffix")
			}
		case "publication_preparation_cleanup":
			var record publicationPreparationCleanupRecord
			if decodeStrictJSON(raw, &record) != nil || record.Version != publicationPreparationCleanupVersion || record.Kind != header.Kind {
				return state, errors.New("invalid preparation cleanup suffix")
			}
		case "publication_first_absent_cleanup":
			var record publicationFirstAbsentCleanupRecord
			if decodeStrictJSON(raw, &record) != nil || record.Version != publicationFirstAbsentCleanupVersion || record.Kind != header.Kind {
				return state, errors.New("invalid first-absent cleanup suffix")
			}
		default:
			return state, fmt.Errorf("unsupported publication journal suffix kind %q", header.Kind)
		}
	}
	return state, nil
}

func initialLyricsEvidenceDigest(acquisition PublicationInitialLyricsAcquisitionRecord, outcome PublicationInitialLyricsOutcomeRecord) string {
	raw, _ := json.Marshal(struct {
		Acquisition PublicationInitialLyricsAcquisitionRecord `json:"acquisition"`
		Outcome     PublicationInitialLyricsOutcomeRecord     `json:"outcome"`
	}{acquisition, outcome})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func committedInitialLyricsRecords(entries []json.RawMessage) (PublicationInitialLyricsAcquisitionRecord, PublicationInitialLyricsOutcomeRecord, bool) {
	var acquisition *PublicationInitialLyricsAcquisitionRecord
	var outcome *PublicationInitialLyricsOutcomeRecord
	for _, raw := range entries {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &header) != nil {
			return PublicationInitialLyricsAcquisitionRecord{}, PublicationInitialLyricsOutcomeRecord{}, false
		}
		switch header.Kind {
		case PublicationInitialLyricsAcquisitionKind:
			var record PublicationInitialLyricsAcquisitionRecord
			if acquisition != nil || decodeStrictJSON(raw, &record) != nil {
				return PublicationInitialLyricsAcquisitionRecord{}, PublicationInitialLyricsOutcomeRecord{}, false
			}
			acquisition = &record
		case PublicationInitialLyricsOutcomeKind:
			var record PublicationInitialLyricsOutcomeRecord
			if outcome != nil || decodeStrictJSON(raw, &record) != nil {
				return PublicationInitialLyricsAcquisitionRecord{}, PublicationInitialLyricsOutcomeRecord{}, false
			}
			outcome = &record
		}
	}
	if acquisition == nil || outcome == nil || acquisition.Guard != outcome.Guard || outcome.Guard.OperationID == uuid.Nil {
		return PublicationInitialLyricsAcquisitionRecord{}, PublicationInitialLyricsOutcomeRecord{}, false
	}
	return *acquisition, *outcome, true
}

func committedInitialLyricsCommitDigest(entries []json.RawMessage, witness firstPublicationCommitWitness) string {
	acquisition, outcome, ok := committedInitialLyricsRecords(entries)
	if !ok {
		return ""
	}
	canonical, err := json.Marshal(struct {
		Acquisition          PublicationInitialLyricsAcquisitionRecord `json:"acquisition"`
		Outcome              PublicationInitialLyricsOutcomeRecord     `json:"outcome"`
		Projection           *PublicationInitialLyricsProjection       `json:"projection"`
		PositionID           uuid.UUID                                 `json:"position_id"`
		Epoch                uuid.UUID                                 `json:"epoch"`
		Generation           uuid.UUID                                 `json:"generation"`
		RelativePath         string                                    `json:"relative_path"`
		Ownership            PublicationCurrentOwnership               `json:"ownership"`
		Audio                PublicationCurrentAudio                   `json:"audio"`
		Metadata             PublicationCommitMetadata                 `json:"metadata"`
		Assets               PublicationAssetOutcomes                  `json:"assets"`
		QualityPolicyVersion int                                       `json:"quality_policy_version"`
	}{acquisition, outcome, witness.InitialLyrics, witness.PositionID, witness.Epoch, witness.CandidateGeneration, witness.RelativePath, witness.Ownership, witness.Audio, witness.Metadata, witness.Assets, witness.QualityPolicyVersion})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:])
}

func nextPublicationStagePhase(from string) string {
	switch from {
	case "preparing":
		return "staged"
	case "staged":
		return "validated"
	default:
		return ""
	}
}
