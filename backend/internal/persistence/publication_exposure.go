package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const PublicationExposureVersion = 1

type PublicationExposureIntent struct {
	Version            int                          `json:"version"`
	StagedPath         string                       `json:"staged_path"`
	TargetCanonicalKey string                       `json:"target_canonical_key"`
	TargetRelativePath string                       `json:"target_relative_path"`
	Expectation        string                       `json:"expectation"`
	PrepOwner          uuid.UUID                    `json:"prep_owner"`
	OutputRootParent   PublicationDirectoryIdentity `json:"output_root_parent"`
	TargetParent       PublicationDirectoryIdentity `json:"target_parent"`
}

type PublicationExposureOutcome string

const (
	PublicationExposureRecorded        PublicationExposureOutcome = "recorded"
	PublicationExposureAlreadyRecorded PublicationExposureOutcome = "already_recorded"
	PublicationExposureStale           PublicationExposureOutcome = "stale"
)

// BeginPublicationExposure records durable authority to begin exposure only. It
// performs no filesystem work and does not authorize replayed filesystem work.
func (repository *SetupManagerRepository) BeginPublicationExposure(ctx context.Context, guard PublicationDeliveryGuard, intent PublicationExposureIntent, staged PublicationStageObservation) (PublicationExposureOutcome, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return PublicationExposureStale, err
	}
	if !validExposureIntent(intent) || !validPublicationStageObservation(staged) {
		return PublicationExposureStale, errors.New("invalid publication exposure evidence")
	}
	result := PublicationExposureStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, _, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "begin publication exposure")
		if err != nil || !live {
			return err
		}
		// Manual-lyrics exposure has its own exact journal grammar. Do not
		// let the generic replay scanner grant even idempotent exposure authority.
		decoded, decodeErr := DecodePublicationInputs(attempt.FrozenInputs)
		if decodeErr != nil || !decoded.SupportsExecution() {
			return nil
		}
		initialLyrics := attempt.IntentKind == "audio" && attempt.ExpectedGeneration == nil
		if initialLyrics {
			entries, journalErr := decodeJournal(attempt.MutationJournal)
			if journalErr != nil {
				return journalErr
			}
			if _, evidenceErr := firstPublicationInitialLyricsEvidence(entries, guard, decoded); evidenceErr != nil {
				return nil
			}
		}
		if attempt.Phase == "exposing" {
			if operation.Stage != "exposing" {
				return nil
			}
			out, err := exposureReplay(attempt.MutationJournal, guard, attempt, intent, staged)
			result = out
			return err
		}
		if attempt.Phase != "validated" || (attempt.IntentKind != "audio" && attempt.IntentKind != "rewrite") {
			return nil
		}
		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil || !input.SupportsExecution() || input.IntentKind != attempt.IntentKind || operation.Stage != "validated" ||
			len(staged.ActualAssets) == 0 || staged.QualityPolicyVersion != input.QualityPolicyVersion ||
			!stageMatchesIntent(staged, intent) || !validatedStageJournalObservation(attempt.MutationJournal, staged) {
			return nil
		}
		if err := validateExposureNamespace(ctx, tx, attempt, intent); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if initialLyrics {
				entries, journalErr := decodeJournal(attempt.MutationJournal)
				if journalErr != nil {
					return journalErr
				}
				state, evidenceErr := firstPublicationInitialLyricsEvidence(entries, guard, input)
				if evidenceErr != nil || !samePublicationStageObservation(state.outcome.FinalObservation, staged) {
					return nil
				}
			}
			return err
		}
		current, currentOK := exposureExpectationSnapshot(ctx, tx, attempt, input, intent)
		if !currentOK {
			return nil
		}
		if !preparationOwnsIntent(attempt.MutationJournal, guard, intent, staged) {
			return nil
		}
		record := publicationExposureJournalRecord{Version: PublicationExposureVersion, Kind: "publication_exposure", From: "validated", To: "exposing", Guard: guard, PositionID: attempt.PositionID, Epoch: attempt.Epoch, CandidateGeneration: attempt.CandidateGeneration, ExpectedGeneration: attempt.ExpectedGeneration, Intent: intent, Current: current, Observation: staged}
		journal, err := appendPublicationExposureJournal(attempt.MutationJournal, record)
		if err != nil {
			return err
		}
		attemptResult, err := tx.NewRaw(`UPDATE track_publication_attempt SET phase='exposing', mutation_journal=?::jsonb WHERE operation_id=? AND phase='validated'`, string(journal), guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("record publication exposure: %w", err)
		}
		attemptRows, err := attemptResult.RowsAffected()
		if err != nil || attemptRows != 1 {
			return errors.New("record publication exposure: attempt changed concurrently")
		}
		operationResult, err := tx.NewRaw(`UPDATE operation SET stage='exposing', updated_at=now() WHERE id=? AND state='running' AND stage='validated'`, guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("record publication exposure: update operation: %w", err)
		}
		operationRows, err := operationResult.RowsAffected()
		if err != nil || operationRows != 1 {
			return errors.New("record publication exposure: operation changed concurrently")
		}
		result = PublicationExposureRecorded
		return nil
	})
	return result, err
}

func validatedStageJournalObservation(raw json.RawMessage, staged PublicationStageObservation) bool {
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || entries == nil {
		return false
	}
	type stageRecord struct {
		Version     int                         `json:"version"`
		Kind        string                      `json:"kind"`
		From        string                      `json:"from"`
		To          string                      `json:"to"`
		Observation PublicationStageObservation `json:"observation"`
	}
	var stagedIndex = -1
	var validatedCount int
	var first PublicationStageObservation
	for index, entry := range entries {
		var header struct {
			Kind string `json:"kind"`
			From string `json:"from"`
			To   string `json:"to"`
		}
		if json.Unmarshal(entry, &header) != nil || header.Kind != "stage_observation" {
			continue
		}
		if header.From == "preparing" && header.To == "staged" {
			var record stageRecord
			if decodeStrictJSON(entry, &record) != nil || record.Version != 1 || record.Kind != "stage_observation" || !validPublicationStageObservation(record.Observation) || stagedIndex >= 0 {
				return false
			}
			stagedIndex = index
			first = record.Observation
			continue
		}
		if header.From == "staged" && header.To == "validated" {
			validatedCount++
			var record stageRecord
			if decodeStrictJSON(entry, &record) != nil || record.Version != 1 || record.Kind != "stage_observation" || !validPublicationStageObservation(record.Observation) || !samePublicationStageObservation(record.Observation, staged) {
				return false
			}
			if stagedIndex < 0 || index <= stagedIndex || record.Observation.RelativePath != first.RelativePath {
				return false
			}
		}
	}
	return stagedIndex >= 0 && validatedCount == 1
}

func decodeStrictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func samePublicationStageObservation(a, b PublicationStageObservation) bool {
	assetsEqual := (len(a.ActualAssets) == 0) == (len(b.ActualAssets) == 0) && (len(a.ActualAssets) == 0 || sameJSONValue(a.ActualAssets, b.ActualAssets))
	return a.Version == b.Version && a.RelativePath == b.RelativePath && samePublicationExposureJSON(a.Ownership, b.Ownership) && a.PayloadSHA256 == b.PayloadSHA256 && sameJSONValue(a.ActualAudio, b.ActualAudio) && sameJSONValue(a.ActualMetadata, b.ActualMetadata) && assetsEqual && a.QualityPolicyVersion == b.QualityPolicyVersion
}

func sameJSONValue(a, b json.RawMessage) bool {
	var left, right any
	decode := func(raw json.RawMessage, value *any) bool {
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		return decoder.Decode(value) == nil && decoder.Decode(new(any)) == io.EOF
	}
	if !decode(a, &left) || !decode(b, &right) {
		return false
	}
	return samePublicationExposureJSON(left, right)
}

func validExposureIntent(i PublicationExposureIntent) bool {
	return i.Version == PublicationExposureVersion && portablePreparationPath(i.StagedPath) && portableRelativePath(i.TargetCanonicalKey) && portableRelativePath(i.TargetRelativePath) && i.PrepOwner != uuid.Nil && validPreparationPlatformIdentity(i.OutputRootParent) && validPreparationPlatformIdentity(i.TargetParent) && (i.Expectation == "absent" || i.Expectation == "expected_current")
}

func stageMatchesIntent(o PublicationStageObservation, i PublicationExposureIntent) bool {
	if o.RelativePath != i.StagedPath || o.Ownership.ExpectedSize == nil || o.Ownership.ExpectedMTime == nil || len(o.PayloadSHA256) != 64 || strings.ToLower(o.PayloadSHA256) != o.PayloadSHA256 {
		return false
	}
	digest, err := hex.DecodeString(o.PayloadSHA256)
	if err != nil || len(digest) != sha256.Size {
		return false
	}
	var audio PublicationCurrentAudio
	decoder := json.NewDecoder(strings.NewReader(string(o.ActualAudio)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&audio) != nil || audio.Version != 1 || strings.TrimSpace(audio.Facts.Codec) == "" || audio.Facts.StreamIndex < 0 || audio.Facts.Lossless == nil {
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		return false
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(o.ActualMetadata, &metadata) != nil || metadata == nil {
		return false
	}
	for key := range metadata {
		if strings.Contains(strings.ToLower(key), "pending") {
			return false
		}
	}
	return true
}

func preparationOwnsIntent(raw json.RawMessage, guard PublicationDeliveryGuard, intent PublicationExposureIntent, staged PublicationStageObservation) bool {
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return false
	}
	steps := map[string]PublicationPreparationRecord{}
	for _, entry := range entries {
		var r PublicationPreparationRecord
		if json.Unmarshal(entry, &r) == nil && validPreparationJournalRecord(r) && r.Kind == "publication_preparation" && r.OperationID == guard.OperationID && r.ClaimUUID == guard.ClaimUUID && r.OwnerToken == intent.PrepOwner {
			if _, exists := steps[r.Step]; exists {
				return false
			}
			steps[r.Step] = r
		}
	}
	ready, created, plan := steps["input_ready"], steps["directory_created"], steps["intent"]
	return ready.Step != "" && created.Step != "" && plan.Step != "" && samePreparationPlan(plan, created) && samePreparationPlan(plan, ready) && ready.DirectoryIdentity != nil && created.DirectoryIdentity != nil && *ready.DirectoryIdentity == *created.DirectoryIdentity && plan.OutputRel == intent.StagedPath && (intent.StagedPath != ready.InputRel) && ready.InputSize != nil && ready.InputMTime != nil && plan.ParentIdentity == intent.OutputRootParent && ready.ParentIdentity == intent.OutputRootParent && created.ParentIdentity == intent.OutputRootParent && (intent.TargetParent == intent.OutputRootParent || intent.TargetParent.Platform == intent.OutputRootParent.Platform)
}

func validateExposureNamespace(ctx context.Context, tx bun.Tx, attempt *TrackPublicationAttempt, i PublicationExposureIntent) error {
	return validatePublicationTargetNamespace(ctx, tx, attempt, i.TargetCanonicalKey, i.TargetRelativePath)
}

func validatePublicationRewriteNamespace(ctx context.Context, tx bun.Tx, attempt *TrackPublicationAttempt, relativePath string) error {
	var manifest struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}
	if decodeStrictJSON(attempt.NamespaceManifest, &manifest) != nil || manifest.Version != 1 || validatePublicationManifest(manifest.Nodes) != nil {
		return sql.ErrNoRows
	}
	var target *PublicationNamespaceNode
	for _, node := range manifest.Nodes {
		if node.NodeKind == "file" && node.RelativePath == relativePath {
			if target != nil {
				return sql.ErrNoRows
			}
			copy := node
			target = &copy
		}
	}
	if target == nil {
		return sql.ErrNoRows
	}
	return validatePublicationTargetNamespace(ctx, tx, attempt, target.CanonicalKey, relativePath)
}

func validatePublicationTargetNamespace(ctx context.Context, tx bun.Tx, attempt *TrackPublicationAttempt, targetCanonicalKey, targetRelativePath string) error {
	var manifest struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(attempt.NamespaceManifest)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF || manifest.Version != 1 || validatePublicationManifest(manifest.Nodes) != nil {
		return sql.ErrNoRows
	}
	var target *PublicationNamespaceNode
	for n := range manifest.Nodes {
		if manifest.Nodes[n].CanonicalKey == targetCanonicalKey && manifest.Nodes[n].RelativePath == targetRelativePath && manifest.Nodes[n].NodeKind == "file" {
			target = &manifest.Nodes[n]
		}
	}
	if target == nil {
		return sql.ErrNoRows
	}
	keys := []string{targetCanonicalKey}
	for parent := parentPath(targetCanonicalKey); parent != ""; parent = parentPath(parent) {
		keys = append(keys, parent)
	}
	for _, key := range keys {
		var node *PublicationNamespaceNode
		for _, n := range manifest.Nodes {
			if n.CanonicalKey == key {
				copy := n
				node = &copy
			}
		}
		if node == nil || (key != targetCanonicalKey && node.NodeKind != "dir") || (key == targetCanonicalKey && node.NodeKind != "file") {
			return sql.ErrNoRows
		}
		digest := sha256.Sum256([]byte(key))
		var claim PublicationNamespaceClaim
		err := tx.NewSelect().Model(&claim).Where("epoch=? AND key_digest=?", attempt.Epoch, digest[:]).For("UPDATE").Scan(ctx)
		if err != nil {
			return err
		}
		if claim.CanonicalKey != key || claim.RelativePath != node.RelativePath || claim.NodeKind != node.NodeKind ||
			(node.NodeKind == "file" && (claim.OwnerPositionID == nil || *claim.OwnerPositionID != attempt.PositionID)) ||
			(node.NodeKind == "dir" && claim.OwnerPositionID != nil) {
			return sql.ErrNoRows
		}
		if node.NodeKind == "dir" && (claim.DirectoryScopeKey == nil || *claim.DirectoryScopeKey != node.DirectoryScope) {
			return sql.ErrNoRows
		}
		if node.NodeKind == "file" && claim.DirectoryScopeKey != nil {
			return sql.ErrNoRows
		}
		var reserved bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM publication_namespace_reservation WHERE namespace_claim_id=? AND position_id=? AND generation=?)`, claim.ID, attempt.PositionID, attempt.CandidateGeneration).Scan(ctx, &reserved); err != nil {
			return err
		}
		if !reserved {
			return sql.ErrNoRows
		}
	}
	return nil
}

func exposureExpectationSnapshot(ctx context.Context, tx bun.Tx, attempt *TrackPublicationAttempt, input PublicationInputs, intent PublicationExposureIntent) (json.RawMessage, bool) {
	var current TrackPublication
	err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return json.RawMessage(`null`), intent.Expectation == "absent" && attempt.ExpectedGeneration == nil
	}
	if err != nil {
		return nil, false
	}
	if attempt.ExpectedGeneration == nil || current.Generation != *attempt.ExpectedGeneration || current.RelativePath != intent.TargetRelativePath {
		return nil, false
	}
	if intent.Expectation != "expected_current" || input.Rewrite == nil || input.Rewrite.CurrentGeneration != current.Generation || input.Rewrite.RelativePath != current.RelativePath {
		return nil, false
	}
	var ownership PublicationCurrentOwnership
	d := json.NewDecoder(strings.NewReader(string(current.Ownership)))
	d.DisallowUnknownFields()
	if d.Decode(&ownership) != nil || d.Decode(new(any)) != io.EOF || ownership.Version != 1 || ownership.Identity.SizeBytes == nil || ownership.Identity.MTime == nil || ownership.Identity.SHA256 == "" || *ownership.Identity.SizeBytes < 0 || !validSHA256(ownership.Identity.SHA256) {
		return nil, false
	}
	if !samePublicationExposureJSON(input.Rewrite.Ownership, ownership.Identity) {
		return nil, false
	}
	encoded, err := json.Marshal(current)
	return encoded, err == nil
}

func exposureReplay(raw json.RawMessage, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt, intent PublicationExposureIntent, staged PublicationStageObservation) (PublicationExposureOutcome, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return PublicationExposureStale, errors.New("publication journal is malformed")
	}
	for _, entry := range entries {
		var record publicationExposureJournalRecord
		decoder := json.NewDecoder(strings.NewReader(string(entry)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&record) == nil && decoder.Decode(new(any)) == io.EOF && record.Version == PublicationExposureVersion && record.Kind == "publication_exposure" && record.From == "validated" && record.To == "exposing" {
			if record.Current != nil && record.Guard == guard && record.PositionID == attempt.PositionID && record.Epoch == attempt.Epoch && record.CandidateGeneration == attempt.CandidateGeneration && sameOptionalUUID(record.ExpectedGeneration, attempt.ExpectedGeneration) && record.Intent == intent && samePublicationStageObservation(record.Observation, staged) {
				return PublicationExposureAlreadyRecorded, nil
			}
		}
	}
	return PublicationExposureStale, nil
}

type publicationExposureJournalRecord struct {
	Version             int                         `json:"version"`
	Kind                string                      `json:"kind"`
	From                string                      `json:"from"`
	To                  string                      `json:"to"`
	Guard               PublicationDeliveryGuard    `json:"guard"`
	PositionID          uuid.UUID                   `json:"position_id"`
	Epoch               uuid.UUID                   `json:"epoch"`
	CandidateGeneration uuid.UUID                   `json:"candidate_generation"`
	ExpectedGeneration  *uuid.UUID                  `json:"expected_generation"`
	Intent              PublicationExposureIntent   `json:"intent"`
	Current             json.RawMessage             `json:"current_snapshot"`
	Observation         PublicationStageObservation `json:"observation"`
}

func appendPublicationExposureJournal(raw json.RawMessage, record publicationExposureJournalRecord) (json.RawMessage, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil || entries == nil {
		return nil, errors.New("journal is not an array")
	}
	entry, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	entries = append(entries, entry)
	return json.Marshal(entries)
}

func validSHA256(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == sha256.Size && strings.ToLower(value) == value
}

func samePublicationExposureJSON(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && string(left) == string(right)
}
