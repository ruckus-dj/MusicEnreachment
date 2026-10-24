package persistence

import (
	"encoding/json"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// automaticAudioUpgradeCurrent proves the database-backed CURRENT witness and
// automatic source-authority revision frozen by an audio-upgrade attempt.
// Filesystem identity is intentionally not rechecked here: installation may
// already have replaced the old artifact on a whole-job retry.
func automaticAudioUpgradeCurrent(current TrackPublication, authority PositionSourceAuthority, input PublicationInputs, epoch uuid.UUID) bool {
	if !validAutomaticAudioUpgrade(input) || input.AudioUpgrade.Current.Generation != current.Generation || current.Epoch != epoch {
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
	if authority.PositionID != current.PositionID || authority.Mode != "automatic" || authority.Revision != input.AudioUpgrade.SourceAuthorityRevision ||
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

func validAutomaticAudioUpgrade(input PublicationInputs) bool {
	return input.Version == PublicationAudioUpgradeInputsVersion && input.QualityPolicyVersion == publication.QualityPolicyVersion && input.IntentKind == "audio" &&
		input.AudioUpgrade != nil && input.Audio != nil && input.SourceAuthorityMode == "automatic" && input.Validate() == nil
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
