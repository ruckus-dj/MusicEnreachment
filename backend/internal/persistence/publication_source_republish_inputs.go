package persistence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"math"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
)

// PublicationSourceRepublishInput freezes all source-derived authority needed
// to republish a selected source without performing preview-time lookups again.
type PublicationSourceRepublishInput struct {
	Current         PublicationCurrentWitness        `json:"current"`
	Mode            string                           `json:"mode"`
	SourceAuthority PublicationSourceAuthority       `json:"source_authority"`
	Association     PublicationRepublishAssociation  `json:"association"`
	Lyrics          PublicationRepublishLyrics       `json:"lyrics"`
	LyricsAuthority PublicationInitialLyricsInput    `json:"lyrics_authority"`
	LyricsLookup    PublicationRepublishLyricsLookup `json:"lyrics_lookup"`
	Cover           PublicationRepublishCover        `json:"cover"`
}

// PublicationSourceAuthority distinguishes an explicitly absent authority
// from a present authority revision.
type PublicationSourceAuthority struct {
	Present               bool      `json:"present"`
	PositionID            uuid.UUID `json:"position_id,omitempty"`
	Mode                  string    `json:"mode,omitempty"`
	Revision              uuid.UUID `json:"revision,omitempty"`
	PublicationEpoch      uuid.UUID `json:"publication_epoch,omitempty"`
	PublicationGeneration uuid.UUID `json:"publication_generation,omitempty"`
	SourceLocationID      uuid.UUID `json:"source_location_id,omitempty"`
	MediaVariantID        uuid.UUID `json:"media_variant_id,omitempty"`
}

// PublicationRepublishAssociation ties the selected association to the frozen
// audio location and canonical media variant.
type PublicationRepublishAssociation struct {
	ID             uuid.UUID `json:"id"`
	LocationID     uuid.UUID `json:"location_id"`
	MediaVariantID uuid.UUID `json:"media_variant_id"`
}

// PublicationRepublishLyrics is the exact selected lyrics value or absence.
type PublicationRepublishLyrics struct {
	State  string `json:"state"`  // value | absent
	Origin string `json:"origin"` // manual | source | lrclib | none
	Text   string `json:"text,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// PublicationRepublishLyricsLookup freezes the lookup eligibility and exact
// request fields derived from analysed source metadata.
type PublicationRepublishLyricsLookup struct {
	Enabled          bool      `json:"enabled"`
	MetadataResultID uuid.UUID `json:"metadata_result_id"`
	WinningResultID  uuid.UUID `json:"winning_result_id"`
	Artist           string    `json:"artist"`
	Album            string    `json:"album"`
	Track            string    `json:"track"`
	DurationSeconds  *float64  `json:"duration_seconds,omitempty"`
}

// PublicationRepublishCover freezes selected cover provenance and derivative.
type PublicationRepublishCover struct {
	ReleaseID          uuid.UUID `json:"release_id"`
	AuthorityRevision  int64     `json:"authority_revision"`
	AuthorityMode      string    `json:"authority_mode"`
	SelectedGeneration uuid.UUID `json:"selected_generation"`
	OriginalSHA256     string    `json:"original_sha256,omitempty"`
	OriginalMIME       string    `json:"original_mime,omitempty"`
	OriginalSizeBytes  *int64    `json:"original_size_bytes,omitempty"`
	Derivation         string    `json:"derivation"` // absent | ready | optional_failure
	PNG                []byte    `json:"png,omitempty"`
	SHA256             string    `json:"sha256,omitempty"`
	MIME               string    `json:"mime,omitempty"`
	SizeBytes          int64     `json:"size_bytes,omitempty"`
	Failure            string    `json:"failure,omitempty"`
}

func (input PublicationSourceRepublishInput) Validate(audio PublicationAudioInput) error {
	if err := input.Current.Validate(); err != nil {
		return fmt.Errorf("invalid current witness: %w", err)
	}
	if input.Mode != "ordinary" && input.Mode != "forced" {
		return errors.New("source republish mode must be ordinary or forced")
	}
	if err := input.SourceAuthority.Validate(audio, input.Current.Generation); err != nil {
		return err
	}
	if input.Association.ID == uuid.Nil || input.Association.LocationID == uuid.Nil || input.Association.LocationID != audio.LocationID || input.Association.MediaVariantID == uuid.Nil || input.Association.MediaVariantID != audio.MediaVariantID {
		return errors.New("source republish requires a selected association identity")
	}
	if err := input.Lyrics.Validate(); err != nil {
		return err
	}
	if input.LyricsAuthority.ExpectedMetadataRevision < 0 {
		return errors.New("source republish lyrics authority revision is invalid")
	}
	if input.LyricsAuthority.Desired != nil {
		field := input.LyricsAuthority.Desired
		envelope := librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{"synced_lyrics": *field}}
		if err := envelope.Validate(); err != nil {
			return fmt.Errorf("invalid source republish lyrics authority: %w", err)
		}
		if field.Authority == librarymetadata.AuthorityManual {
			if field.State == librarymetadata.StateValue && (input.Lyrics.State != "value" || input.Lyrics.Origin != "manual" || input.Lyrics.Text != field.Value) || field.State != librarymetadata.StateValue && (input.Lyrics.State != "absent" || input.Lyrics.Origin != "none") {
				return errors.New("manual lyrics selection conflicts with frozen raw authority")
			}
		}
	}
	if input.Lyrics.Origin == "manual" && (input.LyricsAuthority.Desired == nil || input.LyricsAuthority.Desired.Authority != librarymetadata.AuthorityManual || input.LyricsAuthority.Desired.State != librarymetadata.StateValue || input.LyricsAuthority.Desired.Value != input.Lyrics.Text) {
		return errors.New("manual lyrics require an exact saved manual value receipt")
	}
	if input.Lyrics.Origin == "lrclib" && !input.LyricsLookup.Enabled {
		return errors.New("LRCLIB lyrics require enabled frozen lookup provenance")
	}
	if input.Lyrics.Origin == "source" && (audio.AnalysedMetadata == nil || uniqueAnalysedSyncedLyrics(audio.AnalysedMetadata.Tags) != input.Lyrics.Text) {
		return errors.New("source lyrics do not match the exact frozen analysed source value")
	}
	if input.LyricsLookup.Enabled {
		if audio.AnalysedMetadata == nil || input.LyricsLookup.MetadataResultID != audio.AnalysedMetadata.MetadataResultID || input.LyricsLookup.WinningResultID != audio.AnalysedMetadata.WinningResultID || input.LyricsLookup.Artist == "" || input.LyricsLookup.Track == "" {
			return errors.New("enabled lyrics lookup requires frozen analysed metadata and exact lookup fields")
		}
	}
	if audio.AnalysedMetadata != nil {
		metadata := audio.AnalysedMetadata
		if input.LyricsLookup.MetadataResultID != uuid.Nil || input.LyricsLookup.WinningResultID != uuid.Nil || input.LyricsLookup.Artist != "" || input.LyricsLookup.Album != "" || input.LyricsLookup.Track != "" || input.LyricsLookup.DurationSeconds != nil {
			if input.LyricsLookup.MetadataResultID != metadata.MetadataResultID || input.LyricsLookup.WinningResultID != metadata.WinningResultID ||
				input.LyricsLookup.Artist != exactSingleAnalysedTag(metadata.Tags, "ARTIST") || input.LyricsLookup.Track != exactSingleAnalysedTag(metadata.Tags, "TITLE") ||
				input.LyricsLookup.Album != exactSingleAnalysedTag(metadata.Tags, "ALBUM") || !sameOptionalFloat(input.LyricsLookup.DurationSeconds, metadata.DurationSeconds) {
				return errors.New("lyrics lookup provenance does not match frozen analysed metadata")
			}
		}
	} else if input.LyricsLookup.MetadataResultID != uuid.Nil || input.LyricsLookup.WinningResultID != uuid.Nil || input.LyricsLookup.Artist != "" || input.LyricsLookup.Album != "" || input.LyricsLookup.Track != "" || input.LyricsLookup.DurationSeconds != nil {
		return errors.New("lyrics lookup provenance requires frozen analysed metadata")
	}
	if input.LyricsLookup.Enabled && (input.LyricsLookup.DurationSeconds == nil || *input.LyricsLookup.DurationSeconds <= 0 || math.IsNaN(*input.LyricsLookup.DurationSeconds) || math.IsInf(*input.LyricsLookup.DurationSeconds, 0)) {
		return errors.New("enabled lyrics lookup duration must be finite and positive")
	}
	return input.Cover.Validate()
}

func (authority PublicationSourceAuthority) Validate(audio PublicationAudioInput, currentGeneration uuid.UUID) error {
	if !authority.Present {
		if authority.PositionID != uuid.Nil || authority.Mode != "" || authority.Revision != uuid.Nil || authority.PublicationEpoch != uuid.Nil || authority.PublicationGeneration != uuid.Nil || authority.SourceLocationID != uuid.Nil || authority.MediaVariantID != uuid.Nil {
			return errors.New("absent source authority must not contain identity fields")
		}
		return nil
	}
	if authority.PositionID == uuid.Nil || authority.Revision == uuid.Nil || authority.PublicationEpoch == uuid.Nil || authority.PublicationGeneration == uuid.Nil || authority.SourceLocationID == uuid.Nil || authority.MediaVariantID == uuid.Nil || authority.PublicationGeneration != currentGeneration || authority.SourceLocationID != audio.LocationID || authority.MediaVariantID != audio.MediaVariantID || (authority.Mode != "manual" && authority.Mode != "automatic") {
		return errors.New("present source authority does not match its frozen publication and selected source")
	}
	return nil
}

func exactSingleAnalysedTag(tags map[string][]string, key string) string {
	if len(tags[key]) != 1 {
		return ""
	}
	return tags[key][0]
}

func uniqueAnalysedSyncedLyrics(tags map[string][]string) string {
	values := make(map[string]struct{})
	for key, lyricsValues := range tags {
		if !strings.EqualFold(strings.TrimSpace(key), "LYRICS") {
			continue
		}
		for _, value := range lyricsValues {
			lyrics, err := lrclib.ValidateSyncedLyrics(value)
			if err == nil && lyrics != "" {
				values[lyrics] = struct{}{}
			}
		}
	}
	if len(values) != 1 {
		return ""
	}
	for value := range values {
		return value
	}
	return ""
}

func sameOptionalFloat(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (lyrics PublicationRepublishLyrics) Validate() error {
	switch lyrics.State {
	case "absent":
		if lyrics.Text != "" || lyrics.SHA256 != "" || lyrics.Origin != "none" {
			return errors.New("absent republish lyrics must have no text, digest, or origin")
		}
	case "value":
		if lyrics.Origin != "manual" && lyrics.Origin != "source" && lyrics.Origin != "lrclib" {
			return errors.New("republish lyrics value has invalid origin")
		}
		validated, err := lrclib.ValidateSyncedLyrics(lyrics.Text)
		if err != nil || validated == "" || validated != lyrics.Text {
			return errors.New("republish lyrics must be exact valid synced LRC")
		}
		digest := sha256.Sum256([]byte(lyrics.Text))
		if lyrics.SHA256 != hex.EncodeToString(digest[:]) {
			return errors.New("republish lyrics digest does not match exact text")
		}
	default:
		return errors.New("republish lyrics state must be value or absent")
	}
	return nil
}

func (cover PublicationRepublishCover) Validate() error {
	if cover.ReleaseID == uuid.Nil || cover.AuthorityRevision < 1 || (cover.AuthorityMode != "manual" && cover.AuthorityMode != "automatic") {
		return errors.New("source republish cover authority is invalid")
	}
	if cover.OriginalSHA256 != "" && !validSHA256(cover.OriginalSHA256) {
		return errors.New("source republish original cover digest is invalid")
	}
	originalAbsent := cover.OriginalSHA256 == "" && cover.OriginalMIME == "" && cover.OriginalSizeBytes == nil
	originalPresent := validSHA256(cover.OriginalSHA256) && validPublicationMIME(cover.OriginalMIME) && cover.OriginalSizeBytes != nil && *cover.OriginalSizeBytes > 0
	if !originalAbsent && !originalPresent {
		return errors.New("source republish original cover facts are incomplete")
	}
	switch cover.Derivation {
	case "absent":
		if cover.SelectedGeneration != uuid.Nil || !originalAbsent || len(cover.PNG) != 0 || cover.SHA256 != "" || cover.MIME != "" || cover.SizeBytes != 0 || cover.Failure != "" {
			return errors.New("absent cover derivation contains output")
		}
	case "ready":
		if cover.SelectedGeneration == uuid.Nil || !originalPresent || len(cover.PNG) == 0 || cover.SizeBytes != int64(len(cover.PNG)) || cover.MIME != "image/png" || cover.Failure != "" {
			return errors.New("ready cover derivation has invalid PNG output")
		}
		config, err := png.DecodeConfig(bytes.NewReader(cover.PNG))
		if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 2048 || config.Height > 2048 {
			return errors.New("cover derivative is not a valid PNG within 2048 pixels per side")
		}
		decoded, err := png.Decode(bytes.NewReader(cover.PNG))
		if err != nil || decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
			return errors.New("cover derivative pixel data does not match its bounded PNG header")
		}
		digest := sha256.Sum256(cover.PNG)
		if cover.SHA256 != hex.EncodeToString(digest[:]) {
			return errors.New("cover derivative digest does not match bytes")
		}
	case "optional_failure":
		if cover.SelectedGeneration == uuid.Nil || !originalPresent || len(cover.PNG) != 0 || cover.SHA256 != "" || cover.MIME != "" || cover.SizeBytes != 0 || !validRepublishCoverFailure(cover.Failure) {
			return errors.New("optional cover failure must contain only a safe diagnostic")
		}
	default:
		return errors.New("unknown cover derivation state")
	}
	return nil
}

func validRepublishCoverFailure(value string) bool {
	switch value {
	case "unsupported_source", "decode_failed", "derivative_failed", "source_unavailable":
		return true
	default:
		return false
	}
}

func (input PublicationSourceRepublishInput) Clone() PublicationSourceRepublishInput {
	input.Current = clonePublicationCurrentWitness(input.Current)
	if input.LyricsAuthority.Desired != nil {
		field := *input.LyricsAuthority.Desired
		field.Value = clonePublicationJSONValue(field.Value)
		input.LyricsAuthority.Desired = &field
	}
	if input.LyricsLookup.DurationSeconds != nil {
		value := *input.LyricsLookup.DurationSeconds
		input.LyricsLookup.DurationSeconds = &value
	}
	if input.Cover.OriginalSizeBytes != nil {
		value := *input.Cover.OriginalSizeBytes
		input.Cover.OriginalSizeBytes = &value
	}
	input.Cover.PNG = append([]byte(nil), input.Cover.PNG...)
	return input
}
