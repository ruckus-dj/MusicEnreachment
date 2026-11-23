package persistence

import (
	"encoding/json"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// audioUpgradeAuthorityModeAllowed reports whether the frozen source-authority
// mode is coherent with the upgrade's admission origin. An ordinary
// reconciliation upgrade (nil receipt marker) is automatic-only. An explicit
// matching-receipt upgrade preserves the live mode typed admission confirmed.
func audioUpgradeAuthorityModeAllowed(input PublicationInputs) bool {
	if input.AudioUpgrade == nil {
		return false
	}
	if input.AudioUpgrade.ReceiptTrackSourceID == nil {
		return input.SourceAuthorityMode == "automatic"
	}
	return input.SourceAuthorityMode == "manual" || input.SourceAuthorityMode == "automatic"
}

func audioUpgradeExecutionShape(input PublicationInputs) bool {
	return input.Version == PublicationAudioUpgradeInputsVersion && input.QualityPolicyVersion == publication.QualityPolicyVersion &&
		input.IntentKind == "audio" && input.AudioUpgrade != nil && input.Audio != nil
}

// validAutomaticAudioUpgrade is the ordinary reconciliation contract: no
// explicit receipt marker and automatic live source authority.
func validAutomaticAudioUpgrade(input PublicationInputs) bool {
	return audioUpgradeExecutionShape(input) && input.AudioUpgrade.ReceiptTrackSourceID == nil &&
		input.SourceAuthorityMode == "automatic" && input.Validate() == nil
}

// validAudioUpgradeExecution admits the legacy automatic upgrade and an explicit
// matching-receipt upgrade with either frozen live authority mode.
func validAudioUpgradeExecution(input PublicationInputs) bool {
	if !audioUpgradeExecutionShape(input) {
		return false
	}
	if input.AudioUpgrade.ReceiptTrackSourceID == nil {
		return validAutomaticAudioUpgrade(input)
	}
	return (input.SourceAuthorityMode == "manual" || input.SourceAuthorityMode == "automatic") && input.Validate() == nil
}

// audioUpgradeCurrent proves the database-backed CURRENT witness and frozen
// source-authority revision, mode, epoch, and generation for an audio upgrade.
// Ordinary reconciliation upgrades still require automatic authority; explicit
// receipt upgrades preserve the mode frozen by typed admission.
// Filesystem identity is intentionally not rechecked here: installation may
// already have replaced the old artifact on a whole-job retry.
func audioUpgradeCurrent(current TrackPublication, authority PositionSourceAuthority, input PublicationInputs, epoch uuid.UUID) bool {
	if !validAudioUpgradeExecution(input) || input.AudioUpgrade.Current.Generation != current.Generation || current.Epoch != epoch {
		return false
	}
	witness := input.AudioUpgrade.Current
	var ownership PublicationCurrentOwnership
	if decodeStrictJSON(current.Ownership, &ownership) != nil || !validInstalledPublicationOwnership(ownership) ||
		witness.RelativePath != current.RelativePath || !sameJSONValue(mustPublicationJSON(witness.Ownership), mustPublicationJSON(ownership.Identity)) {
		return false
	}
	currentFacts, ok := publicationAudioFactsFromObservation(current.ActualAudio)
	if !ok || !sameJSONValue(mustPublicationJSON(currentFacts), mustPublicationJSON(witness.AudioFacts)) {
		return false
	}
	if authority.PositionID != current.PositionID || authority.Mode != input.SourceAuthorityMode || authority.Revision != input.AudioUpgrade.SourceAuthorityRevision ||
		authority.PublicationEpoch != current.Epoch || authority.PublicationGeneration != current.Generation {
		return false
	}
	var metadata PublicationCommitMetadata
	if strictMetadata(current.ActualMetadata, &metadata) != nil || !sameJSONValue(mustPublicationJSON(metadata.Tags), mustPublicationJSON(witness.Tags)) {
		return false
	}
	assets, err := DecodePublicationAssetOutcomes(current.ActualAssets)
	if err != nil || !sameJSONValue(mustPublicationJSON(assets), mustPublicationJSON(witness.Assets)) {
		return false
	}
	return true
}

// automaticAudioUpgradeCurrent retains the legacy preparation entry point for
// the same mode-general witness, so explicit receipt upgrades execute too.
func automaticAudioUpgradeCurrent(current TrackPublication, authority PositionSourceAuthority, input PublicationInputs, epoch uuid.UUID) bool {
	return audioUpgradeCurrent(current, authority, input, epoch)
}

func automaticAudioUpgradeQualityBetter(actual, current PersistedAudioFacts) bool {
	return publication.CompareQuality(qualityCandidateFromPersisted(actual), qualityCandidateFromPersisted(current)) == publication.QualityBetter
}

func qualityCandidateFromPersisted(facts PersistedAudioFacts) publication.QualityCandidate {
	return publication.QualityCandidate{
		StreamIndex: facts.StreamIndex, Codec: facts.Codec, Lossless: facts.Lossless,
		Channels: facts.Channels, Layout: facts.ChannelLayout, BitDepth: facts.BitDepth,
		SampleRate: facts.SampleRate, Bitrate: facts.Bitrate,
	}
}

func publicationAudioFactsFromObservation(raw json.RawMessage) (PersistedAudioFacts, bool) {
	var result struct {
		Version int                 `json:"version"`
		Facts   PersistedAudioFacts `json:"facts"`
	}
	if decodeStrictJSON(raw, &result) != nil || result.Version != 1 || result.Facts.Validate() != nil {
		return PersistedAudioFacts{}, false
	}
	return result.Facts, true
}
