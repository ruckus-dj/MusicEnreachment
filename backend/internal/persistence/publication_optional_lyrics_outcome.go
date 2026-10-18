package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/uptrace/bun"
)

const PublicationOptionalLyricsOutcomeVersion = 1

const (
	PublicationOptionalLyricsExactRoundTrip       = "exact_roundtrip"
	PublicationOptionalLyricsRebuiltWithoutLyrics = "rebuilt_without_lyrics"
)

type PublicationOptionalLyricsOutcome string

const (
	PublicationOptionalLyricsRecorded        PublicationOptionalLyricsOutcome = "recorded"
	PublicationOptionalLyricsAlreadyRecorded PublicationOptionalLyricsOutcome = "already_recorded"
	PublicationOptionalLyricsStale           PublicationOptionalLyricsOutcome = "stale"
)

// PublicationOptionalLyricsOutcomeEvidence is factual evidence supplied after
// inspecting the final private output. It is not authority to execute or expose it.
type PublicationOptionalLyricsOutcomeEvidence struct {
	Kind             string
	ManualLyrics     PublicationManualLyricsInput
	Preparation      PublicationPreparationRecord
	InputIdentity    PublicationFileIdentity
	OutputIdentity   PublicationFileIdentity
	FinalObservation PublicationStageObservation
	Diagnostic       string
}

type publicationOptionalLyricsOutcomeJournalRecord struct {
	Version          int                          `json:"version"`
	Kind             string                       `json:"kind"`
	Outcome          string                       `json:"outcome"`
	ManualLyrics     PublicationManualLyricsInput `json:"manual_lyrics"`
	Preparation      PublicationPreparationRecord `json:"preparation"`
	InputIdentity    PublicationFileIdentity      `json:"input_identity"`
	OutputIdentity   PublicationFileIdentity      `json:"output_identity"`
	FinalObservation PublicationStageObservation  `json:"final_observation"`
	Diagnostic       string                       `json:"diagnostic,omitempty"`
}

// GuardedRecordPublicationOptionalLyricsOutcome persists one verified V3
// optional-lyrics result while the original delivery still owns the attempt.
// The caller remains responsible for every filesystem operation and for the
// later commit/exposure protocol.
func (repository *SetupManagerRepository) GuardedRecordPublicationOptionalLyricsOutcome(ctx context.Context, guard PublicationDeliveryGuard, evidence PublicationOptionalLyricsOutcomeEvidence) (PublicationOptionalLyricsOutcome, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return PublicationOptionalLyricsStale, err
	}
	outcome := PublicationOptionalLyricsStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, _, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "record optional lyrics outcome")
		if err != nil {
			return err
		}
		if !live || attempt.Phase != "preparing" || operation.Stage != "preparing" || attempt.IntentKind != "rewrite" {
			return nil
		}
		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil {
			return fmt.Errorf("record optional lyrics outcome: decode frozen inputs: %w", err)
		}
		if input.Version != PublicationInputsVersion3 || input.IntentKind != "rewrite" || input.ManualLyrics == nil || *input.ManualLyrics != evidence.ManualLyrics || input.Rewrite == nil || attempt.ExpectedGeneration == nil || input.Rewrite.CurrentGeneration != *attempt.ExpectedGeneration || input.ManualLyrics.ConfirmedCurrentGeneration != *attempt.ExpectedGeneration {
			return errors.New("record optional lyrics outcome: frozen manual-lyrics intent does not match")
		}
		if err := validatePublicationRewriteNamespace(ctx, tx, attempt, input.Rewrite.RelativePath); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("record optional lyrics outcome: validate namespace: %w", err)
		}
		if !validOptionalLyricsOutcomeEvidence(evidence, guard, input) {
			return errors.New("record optional lyrics outcome: invalid final output evidence")
		}
		entries, err := decodeJournal(attempt.MutationJournal)
		if err != nil {
			return fmt.Errorf("record optional lyrics outcome: decode mutation journal: %w", err)
		}
		preparations, alreadyRecorded, valid := parseOptionalLyricsOutcomeJournal(entries, evidence, guard)
		if !valid {
			return errors.New("record optional lyrics outcome: malformed or conflicting mutation journal")
		}
		if !canonicalOptionalLyricsPreparation(preparations, evidence, guard) {
			return errors.New("record optional lyrics outcome: canonical preparation journal is required")
		}
		if alreadyRecorded {
			outcome = PublicationOptionalLyricsAlreadyRecorded
			return nil
		}
		record := publicationOptionalLyricsOutcomeJournalRecord{Version: PublicationOptionalLyricsOutcomeVersion, Kind: "publication_optional_lyrics_outcome", Outcome: evidence.Kind, ManualLyrics: evidence.ManualLyrics, Preparation: evidence.Preparation, InputIdentity: evidence.InputIdentity, OutputIdentity: evidence.OutputIdentity, FinalObservation: evidence.FinalObservation.Clone(), Diagnostic: evidence.Diagnostic}
		if !validOptionalLyricsOutcomeRecord(record, guard, input) {
			return errors.New("record optional lyrics outcome: invalid journal record")
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		entries = append(entries, encoded)
		journal, err := json.Marshal(entries)
		if err != nil {
			return err
		}
		result, err := tx.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=? AND phase='preparing'`, string(journal), guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("record optional lyrics outcome: update attempt: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return errors.New("record optional lyrics outcome: attempt changed concurrently")
		}
		outcome = PublicationOptionalLyricsRecorded
		return nil
	})
	return outcome, err
}

func parseOptionalLyricsOutcomeJournal(entries []json.RawMessage, evidence PublicationOptionalLyricsOutcomeEvidence, guard PublicationDeliveryGuard) ([]PublicationPreparationRecord, bool, bool) {
	preparations := make([]PublicationPreparationRecord, 0, 3)
	var rebuilds []PublicationCandidateRebuildRecord
	var outcome *publicationOptionalLyricsOutcomeJournalRecord
	for _, raw := range entries {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &header) != nil {
			return nil, false, false
		}
		switch header.Kind {
		case "publication_preparation":
			if len(preparations) >= 3 || len(rebuilds) != 0 || outcome != nil {
				return nil, false, false
			}
			var record PublicationPreparationRecord
			if decodeStrictJSON(raw, &record) != nil || !validPreparationJournalRecord(record) {
				return nil, false, false
			}
			preparations = append(preparations, record)
		case "publication_candidate_rebuild":
			if len(preparations) != 3 || outcome != nil || len(rebuilds) >= 2 {
				return nil, false, false
			}
			var record PublicationCandidateRebuildRecord
			if decodeStrictJSON(raw, &record) != nil || !validCandidateRebuildRecord(record, guard) {
				return nil, false, false
			}
			rebuilds = append(rebuilds, record)
		case "publication_optional_lyrics_outcome":
			if outcome != nil || len(preparations) != 3 {
				return nil, false, false
			}
			var record publicationOptionalLyricsOutcomeJournalRecord
			if decodeStrictJSON(raw, &record) != nil {
				return nil, false, false
			}
			outcome = &record
		default:
			return nil, false, false
		}
	}
	if len(preparations) != 3 {
		return nil, false, false
	}
	if outcome != nil {
		if len(entries) == 0 || !sameOptionalLyricsOutcome(*outcome, evidence) {
			return nil, false, false
		}
		var last struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(entries[len(entries)-1], &last) != nil || last.Kind != "publication_optional_lyrics_outcome" {
			return nil, false, false
		}
	}
	if evidence.Kind == PublicationOptionalLyricsRebuiltWithoutLyrics {
		if len(rebuilds) != 2 || rebuilds[0].Step != "intent" || rebuilds[1].Step != "complete" || !sameCandidateRebuildPlan(rebuilds[0], rebuilds[1]) || !validOptionalLyricsRebuildPair(rebuilds, preparations[2], evidence, guard) {
			return nil, false, false
		}
	} else if len(rebuilds) != 0 {
		return nil, false, false
	}
	return preparations, outcome != nil, true
}

func validOptionalLyricsRebuildPair(records []PublicationCandidateRebuildRecord, prep PublicationPreparationRecord, evidence PublicationOptionalLyricsOutcomeEvidence, guard PublicationDeliveryGuard) bool {
	before, after := records[0].Candidate, records[1].Candidate
	if before == nil || after == nil || prep.CopiedInput == nil || records[0].Guard != guard || records[1].Guard != guard ||
		records[0].OwnerToken != prep.OwnerToken || records[1].OwnerToken != prep.OwnerToken ||
		records[0].PrivateDirRel != prep.PrivateDirRel || records[1].PrivateDirRel != prep.PrivateDirRel || records[0].InputRel != prep.InputRel || records[1].InputRel != prep.InputRel || records[0].OutputRel != prep.OutputRel || records[1].OutputRel != prep.OutputRel ||
		prep.DirectoryIdentity == nil || records[0].DirectoryIdentity != *prep.DirectoryIdentity || records[1].DirectoryIdentity != *prep.DirectoryIdentity ||
		records[0].Input == nil || !samePublicationExposureJSON(records[0].Input, prep.CopiedInput) ||
		!validCopiedInputIdentity(before) || !validCopiedInputIdentity(after) || before.SizeBytes == nil || before.MTime == nil || after.SizeBytes == nil || after.MTime == nil ||
		!validOptionalLyricsPreparedInputIdentity(evidence, prep) {
		return false
	}
	// The final output is the restored, inspected artifact; it need not retain the
	// candidate's identity. Path and final evidence are validated independently.
	return evidence.Preparation.OutputRel == records[1].OutputRel && evidence.Preparation.InputRel == records[1].InputRel
}

func canonicalOptionalLyricsPreparation(records []PublicationPreparationRecord, evidence PublicationOptionalLyricsOutcomeEvidence, guard PublicationDeliveryGuard) bool {
	if len(records) != 3 || records[0].Step != "intent" || records[1].Step != "directory_created" || records[2].Step != "input_ready" {
		return false
	}
	plan, created, ready := records[0], records[1], records[2]
	return samePreparationPlan(plan, created) && samePreparationPlan(plan, ready) &&
		plan.OperationID == guard.OperationID && plan.ClaimUUID == guard.ClaimUUID &&
		plan.Version == PublicationPreparationVersion && plan.Kind == "publication_preparation" &&
		created.DirectoryIdentity != nil && ready.DirectoryIdentity != nil &&
		*created.DirectoryIdentity == *ready.DirectoryIdentity &&
		evidence.Preparation.Step == "input_ready" && samePreparationRecord(evidence.Preparation, ready) &&
		ready.CopiedInput != nil && ready.InputSize != nil && ready.InputMTime != nil
}

func validOptionalLyricsOutcomeEvidence(e PublicationOptionalLyricsOutcomeEvidence, guard PublicationDeliveryGuard, input PublicationInputs) bool {
	if e.FinalObservation.QualityPolicyVersion != input.QualityPolicyVersion {
		return false
	}
	if e.Preparation.Step != "input_ready" || !validPublicationPreparationRecord(e.Preparation, guard) || e.Preparation.CopiedInput == nil || e.Preparation.DirectoryIdentity == nil ||
		e.Preparation.InputRel == "" || e.Preparation.OutputRel == "" || e.OutputIdentity.SizeBytes == nil || *e.OutputIdentity.SizeBytes < 0 || e.OutputIdentity.MTime == nil || !validSHA256(e.OutputIdentity.SHA256) ||
		!validPublicationStageObservation(e.FinalObservation) || e.FinalObservation.RelativePath != e.Preparation.OutputRel || e.FinalObservation.Ownership.ExpectedSize == nil || *e.FinalObservation.Ownership.ExpectedSize != *e.OutputIdentity.SizeBytes ||
		e.FinalObservation.Ownership.ExpectedMTime == nil || *e.FinalObservation.Ownership.ExpectedMTime != *e.OutputIdentity.MTime || e.FinalObservation.PayloadSHA256 != e.OutputIdentity.SHA256 || len(e.FinalObservation.ActualAssets) == 0 {
		return false
	}
	if !optionalLyricsInputMatchesFrozenRewrite(e, input.Rewrite) {
		return false
	}
	var metadata PublicationCommitMetadata
	if strictMetadata(e.FinalObservation.ActualMetadata, &metadata) != nil {
		return false
	}
	assets, err := DecodePublicationAssetOutcomes(e.FinalObservation.ActualAssets)
	if err != nil || assets.ValidateAgainstMetadata(metadata.Tags, nil) != nil {
		return false
	}
	if input.Rewrite == nil || !stageMatchesIntent(e.FinalObservation, PublicationExposureIntent{StagedPath: e.Preparation.OutputRel}) {
		return false
	}
	var observedAudio PublicationCurrentAudio
	if decodeStrictJSON(e.FinalObservation.ActualAudio, &observedAudio) != nil || observedAudio.Version != 1 || !samePublicationExposureJSON(observedAudio.Facts, input.Rewrite.AudioFacts) {
		return false
	}
	switch e.Kind {
	case PublicationOptionalLyricsExactRoundTrip:
		if input.ManualLyrics.State == "explicit_empty" {
			_, present := metadata.Tags["LYRICS"]
			return !present && assets.Lyrics.State == PublicationAssetAbsent && e.Diagnostic == ""
		}
		digest := sha256.Sum256([]byte(input.ManualLyrics.Value))
		return input.ManualLyrics.State == "value" && len(metadata.Tags["LYRICS"]) == 1 && metadata.Tags["LYRICS"][0] == input.ManualLyrics.Value && assets.Lyrics.State == PublicationAssetEmbedded && assets.Lyrics.SHA256 == hex.EncodeToString(digest[:]) && assets.Lyrics.Format == "lrc" && e.Diagnostic == ""
	case PublicationOptionalLyricsRebuiltWithoutLyrics:
		_, present := metadata.Tags["LYRICS"]
		return !present && assets.Lyrics.State == PublicationAssetAbsent && e.Diagnostic == "optional lyrics did not round-trip; rebuilt output without lyrics" &&
			(input.ManualLyrics.State == "value" || input.ManualLyrics.State == "explicit_empty")
	default:
		return false
	}
}

func validOptionalLyricsPreparedInputIdentity(e PublicationOptionalLyricsOutcomeEvidence, prep PublicationPreparationRecord) bool {
	identity := e.InputIdentity
	if !validCopiedInputIdentity(&identity) || !validSHA256(identity.SHA256) || prep.CopiedInput == nil ||
		prep.CopiedInput.SizeBytes == nil || prep.CopiedInput.MTime == nil ||
		*identity.SizeBytes != *prep.CopiedInput.SizeBytes || *identity.MTime != *prep.CopiedInput.MTime {
		return false
	}
	return prep.CopiedInput.SHA256 == "" || prep.CopiedInput.SHA256 == identity.SHA256
}

func optionalLyricsInputMatchesFrozenRewrite(e PublicationOptionalLyricsOutcomeEvidence, rewrite *PublicationRewriteInput) bool {
	if rewrite == nil || !validOptionalLyricsPreparedInputIdentity(e, e.Preparation) || e.Preparation.InputSize == nil {
		return false
	}
	return (rewrite.Ownership.SizeBytes == nil || (*e.Preparation.InputSize == *rewrite.Ownership.SizeBytes && *e.InputIdentity.SizeBytes == *rewrite.Ownership.SizeBytes)) &&
		(rewrite.Ownership.SHA256 == "" || e.InputIdentity.SHA256 == rewrite.Ownership.SHA256)
}

// validOptionalLyricsOutcomeRecord is the DB-independent contract validator used
// both when recording the outcome and when committing/replaying its journal.
func validOptionalLyricsOutcomeRecord(record publicationOptionalLyricsOutcomeJournalRecord, guard PublicationDeliveryGuard, input PublicationInputs) bool {
	evidence := PublicationOptionalLyricsOutcomeEvidence{
		Kind: record.Outcome, ManualLyrics: record.ManualLyrics, Preparation: record.Preparation,
		InputIdentity: record.InputIdentity, OutputIdentity: record.OutputIdentity,
		FinalObservation: record.FinalObservation,
		Diagnostic:       record.Diagnostic,
	}
	return record.Version == PublicationOptionalLyricsOutcomeVersion &&
		record.Kind == "publication_optional_lyrics_outcome" && input.Version == PublicationInputsVersion3 &&
		input.ManualLyrics != nil && *input.ManualLyrics == record.ManualLyrics &&
		validOptionalLyricsOutcomeEvidence(evidence, guard, input)
}

func sameOptionalLyricsOutcome(prior publicationOptionalLyricsOutcomeJournalRecord, evidence PublicationOptionalLyricsOutcomeEvidence) bool {
	want := publicationOptionalLyricsOutcomeJournalRecord{Version: PublicationOptionalLyricsOutcomeVersion, Kind: "publication_optional_lyrics_outcome", Outcome: evidence.Kind, ManualLyrics: evidence.ManualLyrics, Preparation: evidence.Preparation, InputIdentity: evidence.InputIdentity, OutputIdentity: evidence.OutputIdentity, FinalObservation: evidence.FinalObservation.Clone(), Diagnostic: evidence.Diagnostic}
	a, _ := json.Marshal(prior)
	b, _ := json.Marshal(want)
	var left, right any
	decode := func(raw []byte, target *any) bool {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		return decoder.Decode(target) == nil && decoder.Decode(new(any)) == io.EOF
	}
	return decode(a, &left) && decode(b, &right) && samePublicationExposureJSON(left, right)
}
