package persistence

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// PublicationRecoverySnapshot is a caller-owned snapshot. It is deliberately
// not a command: assessment cannot authorize filesystem or database mutations.
type PublicationRecoverySnapshot struct {
	Attempt            TrackPublicationAttempt
	Operation          PublicationCommitOperationSnapshot
	Claim              PublicationExecutionClaim
	OperationID        uuid.UUID
	ApplicationAttempt int
	RiverJobID         int64
	CurrentEpoch       uuid.UUID
	CurrentKnown       bool
	Current            *TrackPublication
}

// PublicationRecoveryAssessment reports journal consistency and the additional
// observations a separate recovery protocol would need to collect.
type PublicationRecoveryAssessment struct {
	Classification       string   `json:"classification"`
	Phase                string   `json:"phase"`
	JournalFrontier      string   `json:"journal_frontier"`
	RequiredObservations []string `json:"required_observations"`
}

// PublicationRecoveryObservationPlan contains detached, already-recorded
// observations. It is descriptive evidence only and does not authorize work.
type PublicationRecoveryObservationPlan struct {
	Assessment     PublicationRecoveryAssessment `json:"assessment"`
	Preparation    *PublicationPreparationRecord `json:"preparation,omitempty"`
	ValidatedStage *PublicationStageObservation  `json:"validated_stage,omitempty"`
	Exposure       *PublicationExposureIntent    `json:"exposure,omitempty"`
}

// PublicationRecoveryObservationPlanFor extracts only the evidence present in
// the immutable journal, after the complete recovery assessment succeeds.
// Preparation.InputMTime remains the original-source timestamp recorded by the
// journal; it is not an expected mtime for the copied input file.
func PublicationRecoveryObservationPlanFor(snapshot PublicationRecoverySnapshot) (PublicationRecoveryObservationPlan, error) {
	assessment, err := AssessPublicationRecoveryEvidence(snapshot)
	if err != nil {
		return PublicationRecoveryObservationPlan{}, err
	}
	plan := PublicationRecoveryObservationPlan{Assessment: assessment}
	entries, err := decodeJournal(snapshot.Attempt.MutationJournal)
	if err != nil {
		return PublicationRecoveryObservationPlan{}, err
	}
	for _, raw := range entries {
		var header struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			return PublicationRecoveryObservationPlan{}, errors.New("malformed publication journal")
		}
		switch header.Kind {
		case "publication_preparation":
			var record PublicationPreparationRecord
			if err := decodeStrictJSON(raw, &record); err != nil {
				return PublicationRecoveryObservationPlan{}, err
			}
			copy := clonePublicationPreparationRecord(record)
			plan.Preparation = &copy
		case "stage_observation":
			var record struct {
				Version     int                         `json:"version"`
				Kind        string                      `json:"kind"`
				From        string                      `json:"from"`
				To          string                      `json:"to"`
				Observation PublicationStageObservation `json:"observation"`
			}
			if err := decodeStrictJSON(raw, &record); err != nil {
				return PublicationRecoveryObservationPlan{}, err
			}
			if record.From == "staged" && record.To == "validated" {
				copy := record.Observation.Clone()
				plan.ValidatedStage = &copy
			}
		case "publication_exposure":
			var record publicationExposureJournalRecord
			if err := decodeStrictJSON(raw, &record); err != nil {
				return PublicationRecoveryObservationPlan{}, err
			}
			copy := record.Intent
			plan.Exposure = &copy
		}
	}
	return plan, nil
}

func clonePublicationPreparationRecord(record PublicationPreparationRecord) PublicationPreparationRecord {
	if record.DirectoryIdentity != nil {
		identity := *record.DirectoryIdentity
		record.DirectoryIdentity = &identity
	}
	if record.InputSize != nil {
		size := *record.InputSize
		record.InputSize = &size
	}
	if record.InputMTime != nil {
		mtime := *record.InputMTime
		record.InputMTime = &mtime
	}
	if record.CopiedInput != nil {
		identity := clonePublicationFileIdentity(*record.CopiedInput)
		record.CopiedInput = &identity
	}
	return record
}

// AssessPublicationRecoveryEvidence performs bounded, pure classification.
// Quiescence is always an observation requirement, including terminal claims.
func AssessPublicationRecoveryEvidence(snapshot PublicationRecoverySnapshot) (PublicationRecoveryAssessment, error) {
	a := snapshot.Attempt
	c := snapshot.Claim
	if a.IntentKind != "audio" || a.ExpectedGeneration != nil || a.OperationID == uuid.Nil || a.PositionID == uuid.Nil || a.Epoch == uuid.Nil || a.CandidateGeneration == uuid.Nil {
		return PublicationRecoveryAssessment{}, errors.New("publication recovery requires a valid first-audio attempt")
	}
	if c.OperationID != a.OperationID || c.Epoch != a.Epoch || c.ClaimUUID == uuid.Nil || c.BootUUID == uuid.Nil || c.DeliveryToken == "" || c.ApplicationAttempt <= 0 || c.RiverJobID <= 0 || c.RiverAttempt <= 0 {
		return PublicationRecoveryAssessment{}, errors.New("publication recovery claim does not match attempt")
	}
	if snapshot.OperationID != c.OperationID || snapshot.ApplicationAttempt != c.ApplicationAttempt || snapshot.RiverJobID != c.RiverJobID {
		return PublicationRecoveryAssessment{}, errors.New("publication recovery operation identity does not match claim")
	}
	if !validPublicationRecoveryClaimState(c.State) || !validPublicationRecoveryOperationState(snapshot.Operation.State) {
		return PublicationRecoveryAssessment{}, errors.New("publication recovery state is unsupported")
	}
	if !snapshot.CurrentKnown {
		return PublicationRecoveryAssessment{}, errors.New("current publication knowledge must be explicit")
	}
	if snapshot.CurrentEpoch != a.Epoch {
		return PublicationRecoveryAssessment{}, errors.New("current publication epoch does not match attempt")
	}
	if snapshot.Current != nil && snapshot.Current.PositionID != a.PositionID {
		return PublicationRecoveryAssessment{}, errors.New("current publication position does not match attempt")
	}
	frontier, err := publicationRecoveryJournalFrontier(a, c)
	if err != nil {
		return PublicationRecoveryAssessment{}, err
	}
	phase := a.Phase
	guard := PublicationDeliveryGuard{OperationID: c.OperationID, ApplicationAttempt: c.ApplicationAttempt, RiverJobID: c.RiverJobID, RiverAttempt: c.RiverAttempt, ClaimUUID: c.ClaimUUID, BootUUID: c.BootUUID, DeliveryToken: c.DeliveryToken, Epoch: c.Epoch, CandidateGeneration: a.CandidateGeneration, ExpectedPositionID: a.PositionID}
	if phase != "committed" && (phase == "preparing" && frontier == "prep_3" || phase == "staged" || phase == "validated" || phase == "exposing") {
		input, inputErr := DecodePublicationInputs(a.FrozenInputs)
		if inputErr != nil || input.IntentKind != "audio" || input.InitialLyrics == nil {
			return PublicationRecoveryAssessment{}, errors.New("initial publication recovery requires frozen lyrics authority")
		}
		entries, journalErr := decodeJournal(a.MutationJournal)
		if journalErr != nil {
			return PublicationRecoveryAssessment{}, journalErr
		}
		if phase == "preparing" || phase == "staged" {
			lyricsEvidence, evidenceErr := parseInitialLyricsPrefix(entries, guard, input)
			if evidenceErr != nil {
				return PublicationRecoveryAssessment{}, fmt.Errorf("initial publication recovery evidence is incomplete: %w", evidenceErr)
			}
			if phase == "preparing" && lyricsEvidence.stageCount != 0 || phase == "staged" && (lyricsEvidence.stageCount != 1 || lyricsEvidence.stagePhase != "staged") {
				return PublicationRecoveryAssessment{}, errors.New("initial publication recovery stage evidence does not match phase")
			}
		} else if _, evidenceErr := firstPublicationInitialLyricsEvidence(entries, guard, input); evidenceErr != nil {
			return PublicationRecoveryAssessment{}, fmt.Errorf("initial publication recovery evidence is incomplete: %w", evidenceErr)
		}
	}
	if snapshot.Operation.Stage != phase || !publicationRecoveryPhaseMatches(phase, frontier) {
		return PublicationRecoveryAssessment{}, errors.New("publication phase does not match journal frontier")
	}
	if phase == "committed" {
		if !snapshot.CurrentKnown || snapshot.Current == nil || len(a.FrozenInputs) != 0 {
			return PublicationRecoveryAssessment{}, errors.New("committed attempt requires a current publication row")
		}
		if !committedWitnessMatches(a.MutationJournal, guard, &a, *snapshot.Current) {
			return PublicationRecoveryAssessment{}, errors.New("committed attempt does not match its unique current witness")
		}
	}
	if phase != "committed" && snapshot.Current != nil {
		return PublicationRecoveryAssessment{}, errors.New("first publication has a current row before committed witness")
	}
	return PublicationRecoveryAssessment{
		Classification: "evidence_consistency_only", Phase: phase, JournalFrontier: frontier,
		RequiredObservations: []string{"old_execution_quiescence", "anchored_parent_identity", "private_directory_ownership", "input_identity", "staged_file_identity", "target_namespace_identity", "cleanup_ownership"},
	}, nil
}

func publicationRecoveryJournalFrontier(attempt TrackPublicationAttempt, claim PublicationExecutionClaim) (string, error) {
	entries, err := decodeJournal(attempt.MutationJournal)
	if err != nil {
		return "", err
	}
	frontier := "empty"
	prep := []string{}
	var prior PublicationPreparationRecord
	var validated PublicationStageObservation
	var exposure publicationExposureJournalRecord
	var installed publicationCandidateInstalledJournalRecord
	var commitCount int
	var cleanupIntent *publicationPreparationCleanupRecord
	var firstAbsentCleanup *PublicationFirstAbsentCleanupWitness
	firstAbsentCleanupCount := 0
	wantGuard := PublicationDeliveryGuard{OperationID: claim.OperationID, ApplicationAttempt: claim.ApplicationAttempt, RiverJobID: claim.RiverJobID, RiverAttempt: claim.RiverAttempt, ClaimUUID: claim.ClaimUUID, BootUUID: claim.BootUUID, DeliveryToken: claim.DeliveryToken, Epoch: claim.Epoch, CandidateGeneration: attempt.CandidateGeneration, ExpectedPositionID: attempt.PositionID}
	for _, raw := range entries {
		var h struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &h) != nil || h.Kind == "" {
			return "", errors.New("malformed publication journal")
		}
		switch h.Kind {
		case PublicationInitialLyricsAcquisitionKind:
			var record PublicationInitialLyricsAcquisitionRecord
			if frontier != "prep_3" || decodeStrictJSON(raw, &record) != nil || record.Guard != wantGuard {
				return "", errors.New("invalid initial lyrics acquisition journal")
			}
		case "publication_candidate_rebuild":
			var record PublicationCandidateRebuildRecord
			if frontier != "prep_3" || decodeStrictJSON(raw, &record) != nil || !validCandidateRebuildRecord(record, wantGuard) {
				return "", errors.New("invalid initial lyrics rebuild journal")
			}
		case PublicationInitialLyricsOutcomeKind:
			var record PublicationInitialLyricsOutcomeRecord
			if frontier != "prep_3" || decodeStrictJSON(raw, &record) != nil || record.Guard != wantGuard {
				return "", errors.New("invalid initial lyrics outcome journal")
			}
		case "publication_preparation":
			var r PublicationPreparationRecord
			steps := [...]string{"intent", "directory_created", "input_ready"}
			if frontier != "empty" && frontier != "prep_1" && frontier != "prep_2" || len(prep) >= len(steps) || decodeStrictJSON(raw, &r) != nil || !validPreparationJournalRecord(r) || r.OperationID != attempt.OperationID || r.ClaimUUID != claim.ClaimUUID || r.Step != steps[len(prep)] || len(prep) > 0 && !samePreparationPlan(r, prior) {
				return "", errors.New("invalid or reordered publication preparation journal")
			}
			prep = append(prep, r.Step)
			if r.Step == "input_ready" && (prior.DirectoryIdentity == nil || r.DirectoryIdentity == nil || *r.DirectoryIdentity != *prior.DirectoryIdentity) {
				return "", errors.New("input-ready preparation does not match created directory")
			}
			prior = r
			frontier = fmt.Sprintf("prep_%d", len(prep))
		case "stage_observation":
			var r struct {
				Version     int                         `json:"version"`
				Kind        string                      `json:"kind"`
				From        string                      `json:"from"`
				To          string                      `json:"to"`
				Observation PublicationStageObservation `json:"observation"`
			}
			if len(prep) != 3 || frontier != "prep_3" && frontier != "staged" || decodeStrictJSON(raw, &r) != nil || r.Version != 1 || r.Kind != "stage_observation" || !validPublicationStageObservation(r.Observation) {
				return "", errors.New("invalid or reordered stage journal")
			}
			if r.From == "preparing" && r.To == "staged" && frontier == "prep_3" && r.Observation.RelativePath == prior.OutputRel {
				frontier = "staged"
			} else if r.From == "staged" && r.To == "validated" && frontier == "staged" && r.Observation.RelativePath == prior.OutputRel {
				validated = r.Observation
				frontier = "validated"
			} else {
				return "", errors.New("invalid stage transition")
			}
		case "publication_preparation_cleanup":
			var r publicationPreparationCleanupRecord
			if (frontier != "prep_2" && frontier != "preparation_cleanup_retained") || decodeStrictJSON(raw, &r) != nil || r.Version != publicationPreparationCleanupVersion || r.Kind != "publication_preparation_cleanup" || r.Guard != wantGuard || !validPublicationPreparationCleanupWitness(wantGuard, r.Witness) || !samePreparationPlan(r.Witness.Preparation, prior) || r.Witness.Preparation.DirectoryIdentity == nil || prior.DirectoryIdentity == nil || *r.Witness.Preparation.DirectoryIdentity != *prior.DirectoryIdentity {
				return "", errors.New("invalid publication preparation cleanup journal")
			}
			if cleanupIntent == nil {
				if r.Step != "intent" || r.Result != "" {
					return "", errors.New("publication preparation cleanup result has no intent")
				}
				copy := r
				cleanupIntent = &copy
			} else {
				if r.Step != "result" || r.Result != "removed" || !reflect.DeepEqual(r.Witness, cleanupIntent.Witness) {
					return "", errors.New("invalid publication preparation cleanup result")
				}
			}
			frontier = "preparation_cleanup_retained"
		case "publication_exposure":
			if frontier != "validated" || decodeStrictJSON(raw, &exposure) != nil || exposure.Version != PublicationExposureVersion || exposure.Kind != "publication_exposure" || exposure.From != "validated" || exposure.To != "exposing" || validatePublicationDeliveryGuard(exposure.Guard) != nil || exposure.Guard != wantGuard || exposure.PositionID != attempt.PositionID || exposure.Epoch != attempt.Epoch || exposure.CandidateGeneration != attempt.CandidateGeneration || exposure.ExpectedGeneration != nil || exposure.Intent.Expectation != "absent" || exposure.Current == nil || string(exposure.Current) != "null" || !validExposureIntent(exposure.Intent) || !validPublicationStageObservation(exposure.Observation) || !samePublicationStageObservation(exposure.Observation, validated) || !stageMatchesIntent(validated, exposure.Intent) || !preparationOwnsIntent(attempt.MutationJournal, exposure.Guard, exposure.Intent, validated) {
				return "", errors.New("invalid publication exposure journal")
			}
			frontier = "exposing"
		case "publication_candidate_installed":
			if frontier != "exposing" || decodeStrictJSON(raw, &installed) != nil || !validPublicationCandidateInstalledRecord(installed) || installed.ExpectedGeneration != nil || installed.Guard != exposure.Guard || installed.PositionID != exposure.PositionID || installed.Epoch != exposure.Epoch || installed.CandidateGeneration != exposure.CandidateGeneration || installed.Intent != exposure.Intent || installed.Observation.TargetRelativePath != exposure.Intent.TargetRelativePath || validated.Ownership.ExpectedSize == nil || validated.Ownership.ExpectedMTime == nil || !publicationRecoveryIdentityMatchesStage(installed.Observation.Identity, validated) {
				return "", errors.New("invalid installed-candidate journal")
			}
			frontier = "candidate_installed"
		case "publication_commit":
			var witness firstPublicationCommitWitness
			commitCount++
			guard := exposure.Guard
			if frontier != "candidate_installed" || commitCount != 1 || decodeStrictJSON(raw, &witness) != nil {
				return "", errors.New("invalid commit witness journal")
			}
			assetsRaw := encodePublicationCommitJSON(witness.Assets)
			_, assetErr := DecodePublicationAssetOutcomes(assetsRaw)
			if witness.Version != firstPublicationCommitVersion || witness.Kind != "publication_commit" || witness.Guard != guard || witness.PositionID != attempt.PositionID || witness.Epoch != attempt.Epoch || witness.CandidateGeneration != attempt.CandidateGeneration || witness.RelativePath != installed.Observation.TargetRelativePath || witness.Ownership.Version != PublicationCurrentOwnershipVersion || witness.Ownership.Identity.SizeBytes == nil || witness.Ownership.Identity.MTime == nil || !publicationRecoveryIdentityMatchesInstalled(witness.Ownership.Identity, installed.Observation.Identity) || !sameJSONValue(encodePublicationCommitJSON(witness.Audio), validated.ActualAudio) || !sameJSONValue(encodePublicationCommitJSON(witness.Metadata), validated.ActualMetadata) || len(validated.ActualAssets) == 0 || assetErr != nil || !sameJSONValue(assetsRaw, validated.ActualAssets) || witness.QualityPolicyVersion == 0 || witness.QualityPolicyVersion != validated.QualityPolicyVersion || witness.QualityPolicyVersion != publication.QualityPolicyVersion {
				return "", errors.New("invalid commit witness journal")
			}
			frontier = "committed"
		case "publication_first_absent_cleanup":
			var record publicationFirstAbsentCleanupRecord
			if frontier != "committed" || commitCount != 1 || decodeStrictJSON(raw, &record) != nil || record.Version != publicationFirstAbsentCleanupVersion || record.Kind != h.Kind || record.Guard != wantGuard || !validFirstAbsentCleanupWitness(record.Witness) || !validFirstAbsentCleanupRecordResult(record) {
				return "", errors.New("invalid first-absent cleanup journal")
			}
			if firstAbsentCleanup == nil {
				witness := record.Witness
				firstAbsentCleanup = &witness
			}
			firstAbsentCleanupCount++
		default:
			return "", fmt.Errorf("unsupported journal record kind %q", h.Kind)
		}
	}
	if firstAbsentCleanupCount > 0 {
		if firstAbsentCleanup == nil || !exactCleanupJournal(entries, wantGuard, *firstAbsentCleanup, firstAbsentCleanupCount) {
			return "", errors.New("first-absent cleanup journal is incomplete")
		}
	}
	return frontier, nil
}

func validPublicationRecoveryClaimState(state string) bool {
	switch state {
	case "executing", "recovery_required", "quiescent":
		return true
	default:
		return false
	}
}

func validPublicationRecoveryOperationState(state string) bool {
	switch state {
	case "queued", "running", "succeeded", "failed", "canceled":
		return true
	default:
		return false
	}
}

func publicationRecoveryIdentityMatchesStage(identity PublicationFileIdentity, stage PublicationStageObservation) bool {
	return stage.Ownership.ExpectedSize != nil && stage.Ownership.ExpectedMTime != nil && identity.SizeBytes != nil && identity.MTime != nil &&
		*identity.SizeBytes == *stage.Ownership.ExpectedSize && *identity.MTime == *stage.Ownership.ExpectedMTime && identity.SHA256 == stage.PayloadSHA256
}

func publicationRecoveryIdentityMatchesInstalled(a, b PublicationFileIdentity) bool {
	return a.SizeBytes != nil && b.SizeBytes != nil && *a.SizeBytes == *b.SizeBytes && a.MTime != nil && b.MTime != nil && *a.MTime == *b.MTime && a.SHA256 == b.SHA256
}

func publicationRecoveryPhaseMatches(phase, frontier string) bool {
	switch phase {
	case "preparing":
		return frontier == "empty" || strings.HasPrefix(frontier, "prep_") || frontier == "preparation_cleanup_retained"
	case "staged":
		return frontier == "staged"
	case "validated":
		return frontier == "validated"
	case "exposing":
		return frontier == "exposing" || frontier == "candidate_installed"
	case "committed":
		return frontier == "committed"
	default:
		return false
	}
}
