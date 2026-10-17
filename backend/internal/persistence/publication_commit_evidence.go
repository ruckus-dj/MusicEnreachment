package persistence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"

	"github.com/google/uuid"
)

// PublicationCommitOperationSnapshot is caller-supplied state, not a database read.
type PublicationCommitOperationSnapshot struct {
	State string
	Stage string
}

type PublicationCommitMetadata struct {
	Version int                 `json:"version"`
	Tags    map[string][]string `json:"tags"`
}

// FirstPublicationCommitProjection is consistency evidence only. It is not a
// filesystem proof or authorization to commit.
type FirstPublicationCommitProjection struct {
	Version                int                         `json:"version"`
	PositionID             uuid.UUID                   `json:"position_id"`
	Epoch                  uuid.UUID                   `json:"epoch"`
	CandidateGeneration    uuid.UUID                   `json:"candidate_generation"`
	TargetRelativePath     string                      `json:"target_relative_path"`
	CurrentAudio           PublicationCurrentAudio     `json:"current_audio"`
	Ownership              PublicationCurrentOwnership `json:"ownership"`
	Metadata               PublicationCommitMetadata   `json:"metadata"`
	NamespaceManifest      []PublicationNamespaceNode  `json:"namespace_manifest"`
	EvidenceClassification string                      `json:"evidence_classification"`
	MissingRequirements    []string                    `json:"missing_requirements"`
}

// AssessFirstPublicationCommit is a pure classifier over supplied snapshots.
// It performs no I/O and returns partial evidence, never commit authorization.
func AssessFirstPublicationCommit(attempt TrackPublicationAttempt, operation PublicationCommitOperationSnapshot, current *TrackPublication, guard PublicationDeliveryGuard) (FirstPublicationCommitProjection, error) {
	if attempt.IntentKind != "audio" || attempt.Phase != "exposing" || operation.State != "running" || operation.Stage != "exposing" {
		return FirstPublicationCommitProjection{}, errors.New("publication attempt is not a running first-audio exposure")
	}
	if current != nil || attempt.ExpectedGeneration != nil {
		return FirstPublicationCommitProjection{}, errors.New("first publication requires no current publication and no expected generation")
	}
	if validatePublicationDeliveryGuard(guard) != nil || attempt.OperationID != guard.OperationID || attempt.PositionID != guard.ExpectedPositionID || attempt.Epoch != guard.Epoch || attempt.CandidateGeneration != guard.CandidateGeneration {
		return FirstPublicationCommitProjection{}, errors.New("publication delivery guard does not match attempt")
	}
	input, err := DecodePublicationInputs(attempt.FrozenInputs)
	if err != nil || input.IntentKind != "audio" || input.Audio == nil {
		return FirstPublicationCommitProjection{}, errors.New("valid frozen audio inputs are required")
	}
	var manifestEnvelope struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}
	if err := decodeStrictJSON(attempt.NamespaceManifest, &manifestEnvelope); err != nil || manifestEnvelope.Version != 1 || validatePublicationManifest(manifestEnvelope.Nodes) != nil {
		return FirstPublicationCommitProjection{}, errors.New("invalid namespace manifest")
	}
	manifest := manifestEnvelope.Nodes
	entries, err := decodeJournal(attempt.MutationJournal)
	if err != nil {
		return FirstPublicationCommitProjection{}, err
	}
	var staged, validated PublicationStageObservation
	var intent PublicationExposureIntent
	var installed PublicationCandidateInstalledObservation
	prepSteps := []string{}
	preparationRecords := []PublicationPreparationRecord{}
	stageSteps := []string{}
	phase := 0
	for _, raw := range entries {
		var h struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &h); err != nil || h.Kind == "" {
			return FirstPublicationCommitProjection{}, errors.New("malformed journal record")
		}
		switch h.Kind {
		case "publication_preparation":
			var r PublicationPreparationRecord
			if phase != 0 || len(prepSteps) >= 3 || decodeStrictJSON(raw, &r) != nil || !validPreparationJournalRecord(r) || r.OperationID != attempt.OperationID || r.ClaimUUID != guard.ClaimUUID {
				return FirstPublicationCommitProjection{}, errors.New("invalid preparation journal record")
			}
			want := [...]string{"intent", "directory_created", "input_ready"}
			if r.Step != want[len(prepSteps)] || len(prepSteps) > 0 && !samePreparationPlan(r, preparationRecords[len(prepSteps)-1]) {
				return FirstPublicationCommitProjection{}, errors.New("preparation records are not one ordered immutable plan")
			}
			prepSteps = append(prepSteps, r.Step)
			preparationRecords = append(preparationRecords, r)
		case "stage_observation":
			var r struct {
				Version     int                         `json:"version"`
				Kind        string                      `json:"kind"`
				From        string                      `json:"from"`
				To          string                      `json:"to"`
				Observation PublicationStageObservation `json:"observation"`
			}
			if len(prepSteps) != 3 || decodeStrictJSON(raw, &r) != nil || r.Version != 1 || !validPublicationStageObservation(r.Observation) {
				return FirstPublicationCommitProjection{}, errors.New("invalid stage journal record")
			}
			if phase == 0 && r.From == "preparing" && r.To == "staged" {
				staged = r.Observation
				phase = 1
			} else if phase == 1 && r.From == "staged" && r.To == "validated" {
				validated = r.Observation
				phase = 2
			} else {
				return FirstPublicationCommitProjection{}, errors.New("stage journal records are duplicated or out of order")
			}
			stageSteps = append(stageSteps, r.From+":"+r.To)
		case "publication_exposure":
			var r publicationExposureJournalRecord
			if phase != 2 || decodeStrictJSON(raw, &r) != nil || r.Version != PublicationExposureVersion || r.Kind != h.Kind || r.From != "validated" || r.To != "exposing" || r.Guard != guard || r.PositionID != attempt.PositionID || r.Epoch != attempt.Epoch || r.CandidateGeneration != attempt.CandidateGeneration || r.ExpectedGeneration != nil || !validExposureIntent(r.Intent) || r.Intent.Expectation != "absent" || string(bytes.TrimSpace(r.Current)) != "null" {
				return FirstPublicationCommitProjection{}, errors.New("invalid exposure journal record")
			}
			intent = r.Intent
			if !samePublicationStageObservation(validated, r.Observation) {
				return FirstPublicationCommitProjection{}, errors.New("exposure does not match validated stage")
			}
			phase = 3
		case "publication_candidate_installed":
			var r publicationCandidateInstalledJournalRecord
			if phase != 3 || decodeStrictJSON(raw, &r) != nil || !validPublicationCandidateInstalledRecord(r) || r.Guard != guard || r.PositionID != attempt.PositionID || r.Epoch != attempt.Epoch || r.CandidateGeneration != attempt.CandidateGeneration || r.ExpectedGeneration != nil || r.Intent != intent {
				return FirstPublicationCommitProjection{}, errors.New("invalid installed-candidate journal record")
			}
			installed = r.Observation
			phase = 4
		default:
			return FirstPublicationCommitProjection{}, fmt.Errorf("unsupported journal record kind %q", h.Kind)
		}
	}
	if len(prepSteps) != 3 || phase != 4 || len(stageSteps) != 2 || !stageMatchesIntent(validated, intent) || staged.RelativePath != validated.RelativePath || validated.RelativePath != preparationRecords[2].OutputRel || path.Dir(preparationRecords[0].InputRel) != path.Dir(preparationRecords[0].OutputRel) || !preparationOwnsIntent(attempt.MutationJournal, guard, intent, validated) {
		return FirstPublicationCommitProjection{}, errors.New("journal does not contain the ordered preparation-to-install chain")
	}
	var metadata PublicationCommitMetadata
	if err := strictMetadata(validated.ActualMetadata, &metadata); err != nil {
		return FirstPublicationCommitProjection{}, err
	}
	if metadata.Version != 1 || metadata.Tags == nil {
		return FirstPublicationCommitProjection{}, errors.New("publication metadata must be version 1 with tags")
	}
	var audio PublicationCurrentAudio
	if err := decodeStrictJSON(validated.ActualAudio, &audio); err != nil || audio.Version != 1 || audio.Facts.Validate() != nil {
		return FirstPublicationCommitProjection{}, errors.New("invalid verified current audio facts")
	}
	matching := false
	var targetRelativePath string
	for _, node := range manifest {
		if node.CanonicalKey == intent.TargetCanonicalKey && node.NodeKind == "file" {
			if node.RelativePath != intent.TargetRelativePath {
				return FirstPublicationCommitProjection{}, errors.New("target canonical key and relative path disagree")
			}
			targetRelativePath = node.RelativePath
			matching = true
		}
	}
	if !matching || installed.TargetRelativePath != targetRelativePath || installed.Identity.SizeBytes == nil || validated.Ownership.ExpectedSize == nil || *installed.Identity.SizeBytes != *validated.Ownership.ExpectedSize || installed.Identity.MTime == nil || validated.Ownership.ExpectedMTime == nil || *installed.Identity.MTime != *validated.Ownership.ExpectedMTime || installed.Identity.SHA256 != validated.PayloadSHA256 {
		return FirstPublicationCommitProjection{}, errors.New("target is not represented by the namespace manifest and installed identity")
	}
	return FirstPublicationCommitProjection{Version: 1, PositionID: attempt.PositionID, Epoch: attempt.Epoch, CandidateGeneration: attempt.CandidateGeneration, TargetRelativePath: targetRelativePath, CurrentAudio: audio, Ownership: PublicationCurrentOwnership{Version: 1, Identity: installed.Identity}, Metadata: metadata, NamespaceManifest: append([]PublicationNamespaceNode(nil), manifest...), EvidenceClassification: "evidence_consistency_only_namespace_not_revalidated", MissingRequirements: []string{"asset_outcomes", "quality_policy_provenance", "namespace_revalidation", "live_delivery_fence"}}, nil
}

func strictMetadata(raw json.RawMessage, target *PublicationCommitMetadata) error {
	if target == nil || len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("decode publication metadata: object is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode publication metadata: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("decode publication metadata: trailing JSON")
	}
	if target.Tags == nil {
		return errors.New("decode publication metadata: tags must be an object")
	}
	for _, values := range target.Tags {
		if values == nil {
			return errors.New("decode publication metadata: tag values must be arrays")
		}
	}
	return nil
}
