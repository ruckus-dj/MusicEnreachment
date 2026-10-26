package persistence

import "github.com/google/uuid"

const PublicationInitialLyricsVersion = 1

const (
	PublicationInitialLyricsNotRequested          = "not_requested"
	PublicationAutomaticLyricsRetryNoResult       = "automatic_retry_no_result"
	PublicationOptionalLyricsExactRoundTrip       = "exact_roundtrip"
	PublicationOptionalLyricsRebuiltWithoutLyrics = "rebuilt_without_lyrics"
)

// PublicationInitialLyricsProjection is the current-row summary of optional
// automatic lyrics resolution. It contains no lyric text or source paths.
type PublicationInitialLyricsProjection struct {
	Version                    int       `json:"version"`
	Generation                 uuid.UUID `json:"generation"`
	Source                     string    `json:"source"`
	SHA256                     string    `json:"sha256"`
	Outcome                    string    `json:"outcome"`
	ResolutionWarningCode      string    `json:"resolution_warning_code"`
	OutcomeWarningCode         string    `json:"outcome_warning_code"`
	AuthorityMetadataRevision  int64     `json:"authority_metadata_revision"`
	AuthorityState             string    `json:"authority_state"`
	Authority                  string    `json:"authority"`
	AuthorityProvenance        string    `json:"authority_provenance"`
	AuthorityFingerprintSHA256 string    `json:"authority_fingerprint_sha256,omitempty"`
}

// PublicationInitialLyricsAcquisitionRecord is the bounded resolver result.
type PublicationInitialLyricsAcquisitionRecord struct {
	Version     int                           `json:"version"`
	Authority   PublicationInitialLyricsInput `json:"authority"`
	Source      string                        `json:"source"`
	Lyrics      string                        `json:"lyrics"`
	SHA256      string                        `json:"sha256"`
	WarningCode string                        `json:"warning_code"`
}

func validPublicationInitialLyricsProjection(projection PublicationInitialLyricsProjection, generation uuid.UUID) bool {
	return projection.Version == PublicationInitialLyricsVersion && projection.Generation == generation &&
		(projection.Source == "none" && projection.SHA256 == "" || (projection.Source == "source" || projection.Source == "lrclib") && validSHA256(projection.SHA256)) &&
		(projection.Outcome == PublicationInitialLyricsNotRequested || projection.Outcome == PublicationAutomaticLyricsRetryNoResult || projection.Outcome == PublicationOptionalLyricsExactRoundTrip || projection.Outcome == PublicationOptionalLyricsRebuiltWithoutLyrics) &&
		projection.AuthorityMetadataRevision >= 0 && validInitialLyricsWarning(projection.ResolutionWarningCode) &&
		(projection.OutcomeWarningCode == "" || projection.Outcome == PublicationOptionalLyricsRebuiltWithoutLyrics && projection.OutcomeWarningCode == "readback_mismatch")
}

func validInitialLyricsWarning(code string) bool {
	switch code {
	case "", "lyrics_source_selection_required", "lyrics_settings_unavailable", "lyrics_lookup_metadata_incomplete", "lyrics_provider_unavailable", "lyrics_lookup_failed", "lyrics_lookup_invalid_synced_text":
		return true
	default:
		return false
	}
}
