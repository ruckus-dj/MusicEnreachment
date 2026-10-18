package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// canonicalManualLyricsCommitPrefix is the validated, immutable V3 rewrite
// prefix. It is deliberately only a parser result; it grants no commit authority.
type canonicalManualLyricsCommitPrefix struct {
	Preparation [3]PublicationPreparationRecord
	Rebuild     []PublicationCandidateRebuildRecord
	Outcome     publicationOptionalLyricsOutcomeJournalRecord
	Exposure    publicationExposureJournalRecord
	Replacement [4]PublicationReplacementEvidence
	Commit      replacementPublicationCommitWitness
	PrefixEnd   int
}

// parseCanonicalManualLyricsCommitPrefix accepts only the canonical V3 journal
// ending at its commit witness, optionally followed by replacement cleanup.
func parseCanonicalManualLyricsCommitPrefix(entries []json.RawMessage, guard PublicationDeliveryGuard, attempt *TrackPublicationAttempt) (canonicalManualLyricsCommitPrefix, bool) {
	var p canonicalManualLyricsCommitPrefix
	if attempt == nil || attempt.ExpectedGeneration == nil || len(entries) < 12 || len(entries) > 18 {
		return p, false
	}
	// V3's outcome and optional rebuild pair are inserted between preparation
	// and stage evidence. Locate them by their exact canonical order.
	idx := 3
	for idx < len(entries) {
		var h struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(entries[idx], &h) != nil {
			return p, false
		}
		if h.Kind != "publication_candidate_rebuild" {
			break
		}
		if len(p.Rebuild) >= 2 {
			return p, false
		}
		var r PublicationCandidateRebuildRecord
		if decodeStrictJSON(entries[idx], &r) != nil || !validCandidateRebuildRecord(r, guard) {
			return p, false
		}
		p.Rebuild = append(p.Rebuild, r)
		idx++
	}
	if len(p.Rebuild) != 0 && (len(p.Rebuild) != 2 || p.Rebuild[0].Step != "intent" || p.Rebuild[1].Step != "complete" || !sameCandidateRebuildPlan(p.Rebuild[0], p.Rebuild[1])) {
		return p, false
	}
	if idx >= len(entries) || decodeStrictJSON(entries[idx], &p.Outcome) != nil || p.Outcome.Kind != "publication_optional_lyrics_outcome" {
		return p, false
	}
	idx++
	if len(entries)-idx < 8 {
		return p, false
	}
	var initialStage struct {
		Observation PublicationStageObservation `json:"observation"`
	}
	if json.Unmarshal(entries[idx], &initialStage) != nil || initialStage.Observation.RelativePath != p.Outcome.FinalObservation.RelativePath {
		return p, false
	}
	for i := range p.Preparation {
		if decodeStrictJSON(entries[i], &p.Preparation[i]) != nil || !validPreparationJournalRecord(p.Preparation[i]) || p.Preparation[i].Step != [3]string{"intent", "directory_created", "input_ready"}[i] {
			return p, false
		}
	}
	evidence := PublicationOptionalLyricsOutcomeEvidence{Kind: p.Outcome.Outcome, ManualLyrics: p.Outcome.ManualLyrics, Preparation: p.Outcome.Preparation, InputIdentity: p.Outcome.InputIdentity, OutputIdentity: p.Outcome.OutputIdentity, FinalObservation: p.Outcome.FinalObservation, Diagnostic: p.Outcome.Diagnostic}
	if _, _, valid := parseOptionalLyricsOutcomeJournal(entries[:idx], evidence, guard); !valid {
		return p, false
	}
	if !samePreparationPlan(p.Preparation[0], p.Outcome.Preparation) ||
		(p.Outcome.Outcome == PublicationOptionalLyricsRebuiltWithoutLyrics) != (len(p.Rebuild) == 2) ||
		!canonicalOptionalLyricsPreparation(p.Preparation[:], evidence, guard) {
		return p, false
	}

	// Reconstruct the V2 canonical base in its established ordering and reuse its
	// full identity/transition checks without relaxing that parser's contract.
	base := make([]json.RawMessage, 11)
	copy(base[:3], entries[:3])
	copy(base[3:5], entries[idx:idx+2])
	copy(base[5:10], entries[idx+2:idx+7])
	var commit replacementPublicationCommitWitness
	commitIndex := idx + 7
	if decodeStrictJSON(entries[commitIndex], &commit) != nil || commit.Version != replacementManualLyricsCommitVersion || commit.ManualLyricsReceipt == nil {
		return p, false
	}
	// The V2 checker only validates the shared projection and records. Adapt its
	// terminal witness locally, then independently validate the V3 receipt below.
	adapted := commit
	adapted.Version = replacementPublicationCommitVersion
	adapted.ManualLyricsReceipt = nil
	base[10], _ = json.Marshal(adapted)
	shared, ok := validateCanonicalReplacementPrefixRaw(base, guard, attempt)
	if !ok {
		return p, false
	}
	// The adapted check uses frozen inputs, which are V3; baseline identity
	// validation depends only on the rewrite contract, copied through the intent.
	p.Preparation = shared.preparation
	p.Exposure = shared.exposure
	p.Replacement = shared.replacement
	var old TrackPublication
	var oldOwnership PublicationCurrentOwnership
	var oldAudio PublicationCurrentAudio
	if json.Unmarshal(p.Exposure.Current, &old) != nil || old.Generation != *attempt.ExpectedGeneration || old.PositionID != attempt.PositionID || old.Epoch != attempt.Epoch ||
		decodePublicationCurrentContract(old.Ownership, &oldOwnership) != nil || oldOwnership.Version != 1 ||
		decodePublicationCurrentContract(old.ActualAudio, &oldAudio) != nil || oldAudio.Version != 1 || oldAudio.Facts.Validate() != nil {
		return p, false
	}
	// This verification context is not an execution input. The immutable old
	// projection and recorded manual intent survive consumption of frozen inputs.
	manual := p.Outcome.ManualLyrics
	attemptInputs := PublicationInputs{Version: PublicationInputsVersion3, IntentKind: "rewrite", QualityPolicyVersion: old.QualityPolicyVersion, ManualLyrics: &manual,
		Rewrite: &PublicationRewriteInput{CurrentGeneration: old.Generation, RelativePath: old.RelativePath, Ownership: oldOwnership.Identity, AudioFacts: oldAudio.Facts}}
	if attempt.FrozenInputs != nil {
		frozen, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil || frozen.Version != PublicationInputsVersion3 || frozen.ManualLyrics == nil || *frozen.ManualLyrics != manual || !publicationRewriteMatchesCurrent(old, attempt.Epoch, *frozen.Rewrite) {
			return p, false
		}
		attemptInputs = frozen
	}
	if !validOptionalLyricsOutcomeRecord(p.Outcome, guard, attemptInputs) || !samePublicationStageObservation(p.Outcome.FinalObservation, p.Exposure.Observation) ||
		!samePublicationExposureJSON(p.Replacement[0].Identity, oldOwnership.Identity) ||
		!sameCopiedBackupIdentity(p.Replacement[0].BackupIdentity, *p.Preparation[2].CopiedInput, oldOwnership.Identity) {
		return p, false
	}
	var oldMetadata, finalMetadata PublicationCommitMetadata
	oldAssets, oldAssetsErr := DecodePublicationAssetOutcomes(old.ActualAssets)
	finalAssets, finalAssetsErr := DecodePublicationAssetOutcomes(p.Outcome.FinalObservation.ActualAssets)
	if strictMetadata(old.ActualMetadata, &oldMetadata) != nil || strictMetadata(p.Outcome.FinalObservation.ActualMetadata, &finalMetadata) != nil ||
		oldAssetsErr != nil || finalAssetsErr != nil || !samePublicationExposureJSON(oldAssets.Cover, finalAssets.Cover) {
		return p, false
	}
	withoutLyrics := func(tags map[string][]string) map[string][]string {
		out := make(map[string][]string, len(tags))
		for name, values := range tags {
			if !strings.EqualFold(name, "LYRICS") {
				out[name] = values
			}
		}
		return out
	}
	if !samePublicationExposureJSON(withoutLyrics(oldMetadata.Tags), withoutLyrics(finalMetadata.Tags)) {
		return p, false
	}
	if len(entries) < commitIndex+1 || commit.Kind != "publication_replacement_commit" || commit.Guard != guard || commit.PositionID != attempt.PositionID || commit.Epoch != attempt.Epoch || commit.ExpectedGeneration != *attempt.ExpectedGeneration || commit.CandidateGeneration != attempt.CandidateGeneration || !currentIsCandidate(commit.Projection, attempt) {
		return p, false
	}
	p.Commit = commit
	if !sameJSONValue(commit.Projection.ActualAudio, old.ActualAudio) || commit.Projection.QualityPolicyVersion != old.QualityPolicyVersion ||
		!sameJSONValue(commit.Projection.ActualMetadata, p.Outcome.FinalObservation.ActualMetadata) || !sameJSONValue(commit.Projection.ActualAssets, p.Outcome.FinalObservation.ActualAssets) {
		return p, false
	}
	var installedOwnership PublicationCurrentOwnership
	if decodePublicationCurrentContract(commit.Projection.Ownership, &installedOwnership) != nil || installedOwnership.Version != 1 || !samePublicationExposureJSON(installedOwnership.Identity, p.Replacement[3].Identity) {
		return p, false
	}
	if !manualLyricsCommitReceiptMatches(commit.ManualLyricsReceipt, p, attempt, attemptInputs) {
		return canonicalManualLyricsCommitPrefix{}, false
	}
	p.PrefixEnd = commitIndex + 1
	if len(entries)-p.PrefixEnd > 4 || !validManualLyricsCleanupSuffix(entries[p.PrefixEnd:], guard, attempt, p) {
		return canonicalManualLyricsCommitPrefix{}, false
	}
	return p, true
}

func manualLyricsCommitReceiptMatches(receipt *manualLyricsCommitReceipt, p canonicalManualLyricsCommitPrefix, attempt *TrackPublicationAttempt, input PublicationInputs) bool {
	if receipt == nil || input.ManualLyrics == nil || input.Rewrite == nil || p.Outcome.FinalObservation.ActualAudio == nil {
		return false
	}
	manual := input.ManualLyrics
	state := ManualLyricsExplicitEmpty
	var digest *string
	if manual.State == "value" {
		state = ManualLyricsValue
		sum := sha256.Sum256([]byte(manual.Value))
		h := hex.EncodeToString(sum[:])
		digest = &h
	}
	warning := (*string)(nil)
	if p.Outcome.Outcome == PublicationOptionalLyricsRebuiltWithoutLyrics {
		w := manualLyricsReadbackMismatchWarning
		warning = &w
	}
	return manual.ExpectedMetadataRevision >= 0 && manual.ConfirmedCurrentGeneration == *attempt.ExpectedGeneration &&
		receipt.PositionID == attempt.PositionID && receipt.Epoch == attempt.Epoch && receipt.DesiredRevision == manual.ExpectedMetadataRevision && receipt.ConfirmedGeneration == *attempt.ExpectedGeneration && receipt.PubGeneration == attempt.CandidateGeneration && receipt.DesiredState == string(state) && sameOptionalString(receipt.DesiredSHA256, digest) && sameOptionalString(receipt.WarningCode, warning) && !receipt.RecordedAt.IsZero() && receipt.RecordedAt.Equal(p.Commit.Projection.ValidatedAt)
}

func validManualLyricsCleanupSuffix(entries []json.RawMessage, guard PublicationDeliveryGuard, _ *TrackPublicationAttempt, p canonicalManualLyricsCommitPrefix) bool {
	prep, installed := p.Preparation[2], p.Replacement[3]
	if len(entries) > 4 || prep.DirectoryIdentity == nil {
		return false
	}
	w := PublicationReplacementCleanupWitness{PrivateDirRel: prep.PrivateDirRel, BackupRel: prep.InputRel, Directory: *prep.DirectoryIdentity, Parent: prep.ParentIdentity, Backup: installed.BackupIdentity}
	steps := []struct{ step, name string }{{"backup_intent", w.BackupRel}, {"backup_result", w.BackupRel}, {"directory_intent", w.PrivateDirRel}, {"directory_result", w.PrivateDirRel}}
	for i, raw := range entries {
		var rec publicationReplacementCleanupRecord
		if decodeStrictJSON(raw, &rec) != nil || rec.Version != publicationReplacementCleanupVersion || rec.Kind != "publication_replacement_cleanup" || rec.Guard != guard || !samePublicationReplacementCleanupWitness(rec.Witness, w) || rec.Step != steps[i].step || rec.Name != steps[i].name || (i%2 == 0 && rec.Result != "") || (i%2 == 1 && rec.Result != "removed" && rec.Result != "already_absent") {
			return false
		}
	}
	return true
}
