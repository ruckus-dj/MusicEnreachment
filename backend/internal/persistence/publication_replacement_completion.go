package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/uptrace/bun"
)

const publicationReplacementCleanupVersion = 1

// PublicationReplacementCleanupWitness binds cleanup to the same live filesystem
// observations used by replacement installation. Persistence treats these as
// evidence, never as filesystem proof.
type PublicationReplacementCleanupWitness struct {
	PrivateDirRel string                       `json:"private_dir_rel"`
	BackupRel     string                       `json:"backup_rel"`
	Directory     PublicationDirectoryIdentity `json:"directory_identity"`
	Parent        PublicationDirectoryIdentity `json:"parent_identity"`
	Backup        PublicationFileIdentity      `json:"backup_identity"`
}

type PublicationReplacementCleanupOutcome string

const (
	PublicationReplacementCleanupRecorded        PublicationReplacementCleanupOutcome = "recorded"
	PublicationReplacementCleanupAlreadyRecorded PublicationReplacementCleanupOutcome = "already_recorded"
	PublicationReplacementCleanupStale           PublicationReplacementCleanupOutcome = "stale"
)

type publicationReplacementCleanupRecord struct {
	Version int                                  `json:"version"`
	Kind    string                               `json:"kind"`
	Guard   PublicationDeliveryGuard             `json:"guard"`
	Witness PublicationReplacementCleanupWitness `json:"witness"`
	Step    string                               `json:"step"`
	Name    string                               `json:"name"`
	Result  string                               `json:"result"`
}

func validPublicationReplacementCleanupWitness(w PublicationReplacementCleanupWitness) bool {
	return portablePreparationPath(w.PrivateDirRel) && portablePreparationPath(w.BackupRel) && strings.HasPrefix(w.BackupRel, w.PrivateDirRel+"/") && validPreparationPlatformIdentity(w.Directory) && validPreparationPlatformIdentity(w.Parent) && w.Backup.SizeBytes != nil && *w.Backup.SizeBytes >= 0 && w.Backup.MTime != nil && validSHA256(w.Backup.SHA256)
}

func samePublicationReplacementCleanupWitness(a, b PublicationReplacementCleanupWitness) bool {
	return a.PrivateDirRel == b.PrivateDirRel && a.BackupRel == b.BackupRel && a.Directory == b.Directory && a.Parent == b.Parent && sameOptionalInt64(a.Backup.SizeBytes, b.Backup.SizeBytes) && sameOptionalInt64(a.Backup.MTime, b.Backup.MTime) && a.Backup.SHA256 == b.Backup.SHA256
}

func (repository *SetupManagerRepository) BeginPublicationReplacementCleanup(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationReplacementCleanupWitness) (PublicationReplacementCleanupOutcome, error) {
	return repository.beginPublicationReplacementCleanup(ctx, guard, witness, "backup_intent", witness.BackupRel)
}

func (repository *SetupManagerRepository) BeginPublicationReplacementDirectoryCleanup(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationReplacementCleanupWitness) (PublicationReplacementCleanupOutcome, error) {
	return repository.beginPublicationReplacementCleanup(ctx, guard, witness, "directory_intent", witness.PrivateDirRel)
}

func (repository *SetupManagerRepository) beginPublicationReplacementCleanup(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationReplacementCleanupWitness, step, name string) (PublicationReplacementCleanupOutcome, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return PublicationReplacementCleanupStale, err
	}
	if !validPublicationReplacementCleanupWitness(witness) {
		return PublicationReplacementCleanupStale, errors.New("invalid replacement cleanup witness")
	}
	out := PublicationReplacementCleanupStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, claim, live, err := lockPublicationDeliveryIdentity(ctx, tx, guard, "begin replacement cleanup")
		if err != nil || !live {
			return err
		}
		if !replacementCommittedState(attempt, operation, claim) {
			return nil
		}
		prefix, ok := validateCanonicalReplacementPrefix(attempt.MutationJournal, guard, attempt)
		boundary := 11
		var receipt *manualLyricsCommitReceipt
		if !ok {
			prefix, boundary, receipt, ok = canonicalReplacementCompletionPrefix(attempt.MutationJournal, guard, attempt)
		}
		if !ok {
			return nil
		}
		if receipt != nil {
			matches, err := manualLyricsReceiptMatchesTx(ctx, tx, receipt)
			if err != nil {
				return err
			}
			if !matches {
				return nil
			}
		}
		if err := validateExposureNamespace(ctx, tx, attempt, prefix.exposure.Intent); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		var current TrackPublication
		if err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return err
		}
		if !replacementCleanupPrefixMatches(entries, guard, attempt, current, witness, boundary) {
			return nil
		}
		if replacementCleanupIntentExists(entries, step, name, witness) {
			out = PublicationReplacementCleanupAlreadyRecorded
			return nil
		}
		if step == "directory_intent" && !replacementCleanupResultExists(entries, "backup", witness.BackupRel) {
			return nil
		}
		record := publicationReplacementCleanupRecord{Version: publicationReplacementCleanupVersion, Kind: "publication_replacement_cleanup", Guard: guard, Witness: witness, Step: step, Name: name}
		journal, err := appendJournalEntry(attempt.MutationJournal, record)
		if err != nil {
			return err
		}
		if err := updateFirstAbsentJournal(ctx, tx, guard, journal, "begin replacement cleanup"); err != nil {
			return err
		}
		out = PublicationReplacementCleanupRecorded
		return nil
	})
	return out, err
}

func (repository *SetupManagerRepository) RecordPublicationReplacementCleanupResult(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationReplacementCleanupWitness, step, result string) (PublicationReplacementCleanupOutcome, error) {
	name := witness.BackupRel
	if step == "directory" {
		name = witness.PrivateDirRel
	}
	if step != "backup" && step != "directory" || result != "removed" && result != "already_absent" {
		return PublicationReplacementCleanupStale, errors.New("invalid replacement cleanup result")
	}
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return PublicationReplacementCleanupStale, err
	}
	if !validPublicationReplacementCleanupWitness(witness) {
		return PublicationReplacementCleanupStale, errors.New("invalid replacement cleanup witness")
	}
	out := PublicationReplacementCleanupStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, claim, live, err := lockPublicationDeliveryIdentity(ctx, tx, guard, "record replacement cleanup")
		if err != nil || !live {
			return err
		}
		if !replacementCommittedState(attempt, operation, claim) {
			return nil
		}
		prefix, ok := validateCanonicalReplacementPrefix(attempt.MutationJournal, guard, attempt)
		boundary := 11
		var receipt *manualLyricsCommitReceipt
		if !ok {
			prefix, boundary, receipt, ok = canonicalReplacementCompletionPrefix(attempt.MutationJournal, guard, attempt)
		}
		if !ok {
			return nil
		}
		if receipt != nil {
			matches, err := manualLyricsReceiptMatchesTx(ctx, tx, receipt)
			if err != nil {
				return err
			}
			if !matches {
				return nil
			}
		}
		if err := validateExposureNamespace(ctx, tx, attempt, prefix.exposure.Intent); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		var current TrackPublication
		if err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return err
		}
		if !replacementCleanupPrefixMatches(entries, guard, attempt, current, witness, boundary) {
			return nil
		}
		if replacementCleanupResultExists(entries, step, name) {
			out = PublicationReplacementCleanupAlreadyRecorded
			return nil
		}
		intent := "backup_intent"
		if step == "directory" {
			intent = "directory_intent"
			if !replacementCleanupResultExists(entries, "backup", witness.BackupRel) {
				return nil
			}
		}
		if !replacementCleanupIntentExists(entries, intent, name, witness) {
			return nil
		}
		record := publicationReplacementCleanupRecord{Version: publicationReplacementCleanupVersion, Kind: "publication_replacement_cleanup", Guard: guard, Witness: witness, Step: step + "_result", Name: name, Result: result}
		journal, err := appendJournalEntry(attempt.MutationJournal, record)
		if err != nil {
			return err
		}
		if err := updateFirstAbsentJournal(ctx, tx, guard, journal, "record replacement cleanup"); err != nil {
			return err
		}
		out = PublicationReplacementCleanupRecorded
		return nil
	})
	return out, err
}

func (repository *SetupManagerRepository) SettleReplacementPublication(ctx context.Context, guard PublicationDeliveryGuard, witness PublicationReplacementCleanupWitness, originalScopeAcknowledged bool) (bool, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return false, err
	}
	if !originalScopeAcknowledged || !validPublicationReplacementCleanupWitness(witness) {
		return false, nil
	}
	settled := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, claim, live, err := lockPublicationDeliveryIdentity(ctx, tx, guard, "settle replacement publication")
		if err != nil || !live || !replacementCommittedState(attempt, operation, claim) {
			return err
		}
		prefix, ok := validateCanonicalReplacementPrefix(attempt.MutationJournal, guard, attempt)
		boundary := 11
		var receipt *manualLyricsCommitReceipt
		if !ok {
			prefix, boundary, receipt, ok = canonicalReplacementCompletionPrefix(attempt.MutationJournal, guard, attempt)
		}
		if !ok {
			return nil
		}
		if receipt != nil {
			matches, err := manualLyricsReceiptMatchesTx(ctx, tx, receipt)
			if err != nil {
				return err
			}
			if !matches {
				return nil
			}
		}
		if err := validateExposureNamespace(ctx, tx, attempt, prefix.exposure.Intent); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		var current TrackPublication
		if err := tx.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return err
		}
		if !replacementCleanupPrefixMatches(entries, guard, attempt, current, witness, boundary) || !replacementCleanupResultExists(entries, "backup", witness.BackupRel) || !replacementCleanupResultExists(entries, "directory", witness.PrivateDirRel) {
			return nil
		}
		var hold PublicationSourceHold
		err = tx.NewSelect().Model(&hold).Where("operation_id=?", guard.OperationID).For("UPDATE").Scan(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("settle replacement publication: read source hold: %w", err)
		}
		if err == nil {
			if hold.ApplicationAttempt != guard.ApplicationAttempt || hold.RiverJobID != guard.RiverJobID || hold.Epoch != guard.Epoch {
				return nil
			}
			if _, err := tx.NewRaw(`DELETE FROM publication_source_hold WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND epoch=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, guard.Epoch).Exec(ctx); err != nil {
				return err
			}
		}
		if _, err := tx.NewRaw(`DELETE FROM operation_tool_read_hold WHERE operation_id=?`, guard.OperationID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE operation SET tools_read_required=false WHERE id=? AND attempt=? AND river_job_id=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx); err != nil {
			return err
		}
		res, err := tx.NewRaw(`UPDATE operation SET state='succeeded',stage='finished',safe_error=NULL,finished_at=now(),updated_at=now() WHERE id=? AND state='running' AND attempt=? AND river_job_id=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n != 1 {
			return errors.New("settle replacement publication: operation changed concurrently")
		}
		if _, err := tx.NewRaw(`DELETE FROM publication_execution_claim WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND claim_uuid=? AND boot_uuid=? AND delivery_token=?`, guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, guard.ClaimUUID, guard.BootUUID, guard.DeliveryToken).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM track_publication_attempt WHERE operation_id=?`, guard.OperationID).Exec(ctx); err != nil {
			return err
		}
		settled = true
		return nil
	})
	return settled, err
}

func replacementCommittedState(a *TrackPublicationAttempt, o *Operation, c *PublicationExecutionClaim) bool {
	return a != nil && o != nil && c != nil && a.IntentKind == "rewrite" && a.ExpectedGeneration != nil && a.Phase == "committed" && a.FrozenInputs == nil && o.State == "running" && o.Stage == "committed" && c.State == "executing"
}

func replacementCleanupPrefixMatches(entries []json.RawMessage, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt, current TrackPublication, w PublicationReplacementCleanupWitness, boundary int) bool {
	prefix, ok := validateCanonicalReplacementPrefixRaw(entries, guard, attempt)
	if !ok {
		if retry, end, valid := canonicalAutomaticLyricsRetryCompletionPrefix(entries, guard, attempt); valid && end == boundary {
			prefix, ok = retry, true
		}
	}
	if !ok {
		manual, valid := parseCanonicalManualLyricsCommitPrefix(entries, guard, attempt)
		if !valid || manual.PrefixEnd != boundary {
			return false
		}
		prefix = canonicalReplacementPrefix{preparation: manual.Preparation, exposure: manual.Exposure, replacement: manual.Replacement, commit: manual.Commit}
		ok = true
	}
	if !ok || !currentPublicationMatchesReplacementWitness(current, prefix.commit.Projection) || !currentIsCandidate(current, attempt) {
		return false
	}
	if !sameCleanupWitnessForPrefix(w, prefix) {
		return false
	}
	steps := []struct{ step, name string }{{"backup_intent", w.BackupRel}, {"backup_result", w.BackupRel}, {"directory_intent", w.PrivateDirRel}, {"directory_result", w.PrivateDirRel}}
	if boundary < 0 || boundary > len(entries) {
		return false
	}
	for i, raw := range entries[boundary:] {
		var rec publicationReplacementCleanupRecord
		if i >= len(steps) || decodeStrictJSON(raw, &rec) != nil || rec.Version != publicationReplacementCleanupVersion || rec.Kind != "publication_replacement_cleanup" || rec.Guard != guard || !samePublicationReplacementCleanupWitness(rec.Witness, w) || rec.Step != steps[i].step || rec.Name != steps[i].name || (strings.HasSuffix(rec.Step, "_intent") && rec.Result != "") || (strings.HasSuffix(rec.Step, "_result") && rec.Result != "removed" && rec.Result != "already_absent") {
			return false
		}
	}
	// The immutable prefix's strict witness is recorded only after all four
	// preparation, stage, exposure and replacement observations were validated.
	return true
}

// canonicalReplacementCompletionPrefix adapts completion-only V3 evidence to
// the shared V2 replacement projection. It never changes the V2 parser.
func canonicalReplacementCompletionPrefix(raw json.RawMessage, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt) (canonicalReplacementPrefix, int, *manualLyricsCommitReceipt, bool) {
	entries, err := decodeJournal(raw)
	if err != nil {
		return canonicalReplacementPrefix{}, 0, nil, false
	}
	if prefix, boundary, ok := canonicalAutomaticLyricsRetryCompletionPrefix(entries, guard, attempt); ok {
		return prefix, boundary, nil, true
	}
	p, ok := parseCanonicalManualLyricsCommitPrefix(entries, guard, attempt)
	if !ok {
		return canonicalReplacementPrefix{}, 0, nil, false
	}
	return canonicalReplacementPrefix{preparation: p.Preparation, exposure: p.Exposure, replacement: p.Replacement, commit: p.Commit}, p.PrefixEnd, p.Commit.ManualLyricsReceipt, true
}

func canonicalAutomaticLyricsRetryCompletionPrefix(entries []json.RawMessage, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt) (canonicalReplacementPrefix, int, bool) {
	if attempt == nil || attempt.ExpectedGeneration == nil || len(entries) < 13 {
		return canonicalReplacementPrefix{}, 0, false
	}
	var acquisition PublicationAutomaticLyricsRetryAcquisition
	if decodeStrictJSON(entries[3], &acquisition) != nil || acquisition.Kind != automaticLyricsRetryAcquisitionKind {
		return canonicalReplacementPrefix{}, 0, false
	}
	index := 4
	for index < len(entries) {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(entries[index], &header) != nil || header.Kind != "publication_candidate_rebuild" {
			break
		}
		index++
	}
	if index != 4 && index != 6 {
		return canonicalReplacementPrefix{}, 0, false
	}
	outcomeIndex := index
	stageIndex := outcomeIndex + 1
	exposureIndex := stageIndex + 2
	boundary := exposureIndex + 1 + 4 + 1
	if len(entries) < boundary || len(entries) > boundary+4 {
		return canonicalReplacementPrefix{}, 0, false
	}
	var outcome PublicationAutomaticLyricsRetryOutcome
	var exposure publicationExposureJournalRecord
	if decodeStrictJSON(entries[outcomeIndex], &outcome) != nil || decodeStrictJSON(entries[exposureIndex], &exposure) != nil {
		return canonicalReplacementPrefix{}, 0, false
	}
	var old TrackPublication
	var oldMetadata PublicationCommitMetadata
	if json.Unmarshal(exposure.Current, &old) != nil || strictMetadata(old.ActualMetadata, &oldMetadata) != nil {
		return canonicalReplacementPrefix{}, 0, false
	}
	input := PublicationInputs{Version: PublicationInputsVersion, QualityPolicyVersion: outcome.FinalObservation.QualityPolicyVersion, IntentKind: "rewrite", Rewrite: &PublicationRewriteInput{CurrentGeneration: *attempt.ExpectedGeneration}, AutomaticLyricsRetry: &acquisition.Authority, DesiredTags: withoutAutomaticLyricsTag(oldMetadata.Tags)}
	state, err := parseAutomaticLyricsRetryPrefix(entries[:boundary], guard, input)
	if err != nil || state.commit == nil || state.prefixEnd != boundary {
		return canonicalReplacementPrefix{}, 0, false
	}
	base := make([]json.RawMessage, 0, 10)
	base = append(base, entries[:3]...)
	base = append(base, entries[stageIndex:stageIndex+2]...)
	base = append(base, entries[exposureIndex])
	base = append(base, entries[exposureIndex+1:exposureIndex+5]...)
	prefix, ok := validateCanonicalReplacementPrefixSharedRaw(base, guard, attempt)
	if !ok {
		return canonicalReplacementPrefix{}, 0, false
	}
	prefix.commit = *state.commit
	return prefix, boundary, true
}

func withoutAutomaticLyricsTag(tags map[string][]string) map[string][]string {
	result := make(map[string][]string, len(tags))
	for name, values := range tags {
		if !strings.EqualFold(strings.TrimSpace(name), "LYRICS") {
			result[name] = append([]string(nil), values...)
		}
	}
	return result
}

type canonicalReplacementPrefix struct {
	preparation [3]PublicationPreparationRecord
	exposure    publicationExposureJournalRecord
	replacement [4]PublicationReplacementEvidence
	commit      replacementPublicationCommitWitness
}

// validateCanonicalReplacementPrefix accepts only the exact immutable journal
// prefix emitted by the replacement flow; cleanup records are validated separately.
func validateCanonicalReplacementPrefix(raw json.RawMessage, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt) (canonicalReplacementPrefix, bool) {
	entries, err := decodeJournal(raw)
	if err != nil {
		return canonicalReplacementPrefix{}, false
	}
	return validateCanonicalReplacementPrefixRaw(entries, guard, attempt)
}

func validateCanonicalReplacementPrefixRaw(entries []json.RawMessage, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt) (canonicalReplacementPrefix, bool) {
	if len(entries) < 11 {
		return canonicalReplacementPrefix{}, false
	}
	p, ok := validateCanonicalReplacementPrefixSharedRaw(entries[:10], guard, attempt)
	if !ok || decodeStrictJSON(entries[10], &p.commit) != nil || p.commit.Version != replacementPublicationCommitVersion || p.commit.ManualLyricsReceipt != nil || p.commit.Kind != "publication_replacement_commit" || p.commit.Guard != guard || p.commit.PositionID != attempt.PositionID || p.commit.Epoch != attempt.Epoch || p.commit.ExpectedGeneration != *attempt.ExpectedGeneration || p.commit.CandidateGeneration != attempt.CandidateGeneration || !currentIsCandidate(p.commit.Projection, attempt) {
		return canonicalReplacementPrefix{}, false
	}
	return p, true
}

// validateCanonicalReplacementPrefixSharedRaw validates the semantic replacement
// prefix shared by ordinary and manual-lyrics commits, without interpreting the
// contract-specific terminal commit witness.
func validateCanonicalReplacementPrefixSharedRaw(entries []json.RawMessage, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt) (canonicalReplacementPrefix, bool) {
	var p canonicalReplacementPrefix
	if len(entries) != 10 || attempt == nil || attempt.ExpectedGeneration == nil {
		return p, false
	}
	want := []string{"publication_preparation", "publication_preparation", "publication_preparation", "stage_observation", "stage_observation", "publication_exposure", "publication_replacement", "publication_replacement", "publication_replacement", "publication_replacement"}
	for i, raw := range entries {
		var h struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &h) != nil || h.Kind != want[i] {
			return p, false
		}
	}
	for i := range p.preparation {
		if decodeStrictJSON(entries[i], &p.preparation[i]) != nil || !validPreparationJournalRecord(p.preparation[i]) || p.preparation[i].Step != [3]string{"intent", "directory_created", "input_ready"}[i] {
			return p, false
		}
	}
	for i := 1; i < 3; i++ {
		if !samePreparationPlan(p.preparation[0], p.preparation[i]) || p.preparation[i].OwnerToken != p.preparation[0].OwnerToken || p.preparation[i].ParentIdentity != p.preparation[0].ParentIdentity || p.preparation[i].PrivateDirRel != p.preparation[0].PrivateDirRel || p.preparation[i].InputRel != p.preparation[0].InputRel || p.preparation[i].OutputRel != p.preparation[0].OutputRel {
			return p, false
		}
	}
	if p.preparation[2].CopiedInput == nil || p.preparation[0].OperationID != guard.OperationID || p.preparation[0].ClaimUUID != guard.ClaimUUID || p.preparation[0].PrivateDirRel == "" || !strings.HasPrefix(p.preparation[0].InputRel, p.preparation[0].PrivateDirRel+"/") || decodeStrictJSON(entries[5], &p.exposure) != nil {
		return p, false
	}
	if p.exposure.Version != PublicationExposureVersion || p.exposure.Kind != "publication_exposure" || p.exposure.From != "validated" || p.exposure.To != "exposing" || p.exposure.Guard != guard || p.exposure.PositionID != attempt.PositionID || p.exposure.Epoch != attempt.Epoch || p.exposure.CandidateGeneration != attempt.CandidateGeneration || p.exposure.ExpectedGeneration == nil || *p.exposure.ExpectedGeneration != *attempt.ExpectedGeneration || p.exposure.Intent.Expectation != "expected_current" || !validExposureIntent(p.exposure.Intent) || !validPublicationStageObservation(p.exposure.Observation) || p.preparation[0].OwnerToken != p.exposure.Intent.PrepOwner || p.preparation[0].OutputRel != p.exposure.Intent.StagedPath || p.preparation[0].ParentIdentity != p.exposure.Intent.OutputRootParent || p.exposure.Intent.TargetParent.Platform != p.preparation[0].ParentIdentity.Platform {
		return p, false
	}
	var stages [2]struct {
		Version     int                         `json:"version"`
		Kind        string                      `json:"kind"`
		From        string                      `json:"from"`
		To          string                      `json:"to"`
		Observation PublicationStageObservation `json:"observation"`
	}
	for i := range stages {
		if decodeStrictJSON(entries[3+i], &stages[i]) != nil || stages[i].Version != 1 || stages[i].Kind != "stage_observation" || !validPublicationStageObservation(stages[i].Observation) || ![]bool{stages[i].From == "preparing" && stages[i].To == "staged", stages[i].From == "staged" && stages[i].To == "validated"}[i] {
			return p, false
		}
	}
	if !samePublicationStageObservation(stages[1].Observation, p.exposure.Observation) {
		return p, false
	}
	for i := range p.replacement {
		e := &p.replacement[i]
		if decodeStrictJSON(entries[6+i], e) != nil || !validPublicationReplacementEvidence(*e) || e.Guard != guard || e.PositionID != attempt.PositionID || e.Epoch != attempt.Epoch || e.CandidateGeneration != attempt.CandidateGeneration || e.ExpectedGeneration == nil || *e.ExpectedGeneration != *attempt.ExpectedGeneration || e.Intent != p.exposure.Intent || !samePublicationStageObservation(e.Staged, p.exposure.Observation) || e.BackupPath != p.preparation[0].InputRel {
			return p, false
		}
		if i > 0 && !validReplacementSuccessor(&p.replacement[i-1], e) {
			return p, false
		}
	}
	if p.replacement[0].From != "exposing" || p.replacement[0].To != "backup_intent" || p.replacement[1].To != "backup_ready" || p.replacement[2].To != "replacement_intent" || p.replacement[3].To != "installed" {
		return p, false
	}
	return p, true
}

func sameCleanupWitnessForPrefix(w PublicationReplacementCleanupWitness, p canonicalReplacementPrefix) bool {
	prep, installed := p.preparation[2], p.replacement[3]
	return w.PrivateDirRel == prep.PrivateDirRel && w.BackupRel == prep.InputRel && prep.DirectoryIdentity != nil && w.Directory == *prep.DirectoryIdentity && w.Parent == prep.ParentIdentity && samePublicationExposureJSON(w.Backup, installed.BackupIdentity) && sameCopiedBackupIdentity(w.Backup, *prep.CopiedInput, p.replacement[0].Identity)
}

// replacementCommitReplayMatches validates the immutable replacement journal
// and its only permitted cleanup suffix. Cleanup records are intentionally not
// searched for a commit marker: the full canonical prefix must start at entry 0.
func replacementCommitReplayMatches(entries []json.RawMessage, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt, current TrackPublication) bool {
	prefix, ok := validateCanonicalReplacementPrefixRaw(entries, guard, attempt)
	if !ok || len(entries) > 15 || !currentIsCandidate(current, attempt) || !currentPublicationMatchesReplacementWitness(current, prefix.commit.Projection) {
		return false
	}
	if len(entries) == 11 {
		return true
	}
	prep := prefix.preparation[2]
	if prep.DirectoryIdentity == nil {
		return false
	}
	witness := PublicationReplacementCleanupWitness{
		PrivateDirRel: prep.PrivateDirRel,
		BackupRel:     prep.InputRel,
		Directory:     *prep.DirectoryIdentity,
		Parent:        prep.ParentIdentity,
		Backup:        prefix.replacement[3].BackupIdentity,
	}
	steps := []struct{ step, name string }{
		{"backup_intent", witness.BackupRel},
		{"backup_result", witness.BackupRel},
		{"directory_intent", witness.PrivateDirRel},
		{"directory_result", witness.PrivateDirRel},
	}
	if !sameCleanupWitnessForPrefix(witness, prefix) {
		return false
	}
	for i, raw := range entries[11:] {
		var rec publicationReplacementCleanupRecord
		if decodeStrictJSON(raw, &rec) != nil || rec.Version != publicationReplacementCleanupVersion || rec.Kind != "publication_replacement_cleanup" || rec.Guard != guard || !samePublicationReplacementCleanupWitness(rec.Witness, witness) || rec.Step != steps[i].step || rec.Name != steps[i].name || (strings.HasSuffix(rec.Step, "_intent") && rec.Result != "") || (strings.HasSuffix(rec.Step, "_result") && rec.Result != "removed" && rec.Result != "already_absent") {
			return false
		}
	}
	return true
}

func replacementCleanupIntentExists(entries []json.RawMessage, step, name string, w PublicationReplacementCleanupWitness) bool {
	for _, raw := range entries {
		var r publicationReplacementCleanupRecord
		if decodeStrictJSON(raw, &r) == nil && r.Kind == "publication_replacement_cleanup" && r.Step == step && r.Name == name && samePublicationReplacementCleanupWitness(r.Witness, w) {
			return true
		}
	}
	return false
}
func replacementCleanupResultExists(entries []json.RawMessage, step, name string) bool {
	for _, raw := range entries {
		var r publicationReplacementCleanupRecord
		if decodeStrictJSON(raw, &r) == nil && r.Kind == "publication_replacement_cleanup" && r.Step == step+"_result" && r.Name == name && (r.Result == "removed" || r.Result == "already_absent") {
			return true
		}
	}
	return false
}
