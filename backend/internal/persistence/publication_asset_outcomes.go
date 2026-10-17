package persistence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
)

const PublicationAssetOutcomesVersion = 1

type PublicationAssetState string

const (
	PublicationAssetAbsent   PublicationAssetState = "absent"
	PublicationAssetEmbedded PublicationAssetState = "embedded"
)

// PublicationCoverOutcome describes only an observed cover outcome. It does
// not identify a source, downloader, or producer.
type PublicationCoverOutcome struct {
	State     PublicationAssetState `json:"state"`
	SHA256    string                `json:"sha256,omitempty"`
	MIME      string                `json:"mime,omitempty"`
	SizeBytes *int64                `json:"size_bytes,omitempty"`
}

// PublicationLyricsOutcome describes only an observed synced-LRC outcome.
type PublicationLyricsOutcome struct {
	State  PublicationAssetState `json:"state"`
	SHA256 string                `json:"sha256,omitempty"`
	Format string                `json:"format,omitempty"`
}

// PublicationAssetOutcomes is a versioned record of the two optional assets.
type PublicationAssetOutcomes struct {
	Version int                      `json:"version"`
	Cover   PublicationCoverOutcome  `json:"cover"`
	Lyrics  PublicationLyricsOutcome `json:"lyrics"`
}

func DecodePublicationAssetOutcomes(raw json.RawMessage) (PublicationAssetOutcomes, error) {
	var rawValue any
	if err := json.Unmarshal(raw, &rawValue); err != nil {
		return PublicationAssetOutcomes{}, fmt.Errorf("decode publication asset outcomes: %w", err)
	}
	if containsJSONNull(rawValue) {
		return PublicationAssetOutcomes{}, errors.New("decode publication asset outcomes: null values are not allowed")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var outcomes PublicationAssetOutcomes
	if err := decoder.Decode(&outcomes); err != nil {
		return PublicationAssetOutcomes{}, fmt.Errorf("decode publication asset outcomes: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return PublicationAssetOutcomes{}, errors.New("decode publication asset outcomes: trailing JSON document")
		}
		return PublicationAssetOutcomes{}, fmt.Errorf("decode publication asset outcomes: %w", err)
	}
	if err := outcomes.Validate(); err != nil {
		return PublicationAssetOutcomes{}, err
	}
	return outcomes.Clone(), nil
}

func containsJSONNull(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case []any:
		for _, item := range typed {
			if containsJSONNull(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsJSONNull(item) {
				return true
			}
		}
	}
	return false
}

func (outcomes PublicationAssetOutcomes) Validate() error {
	if outcomes.Version != PublicationAssetOutcomesVersion {
		return fmt.Errorf("unsupported publication asset outcomes version %d", outcomes.Version)
	}
	if err := outcomes.Cover.Validate(); err != nil {
		return fmt.Errorf("invalid publication cover outcome: %w", err)
	}
	if err := outcomes.Lyrics.Validate(); err != nil {
		return fmt.Errorf("invalid publication lyrics outcome: %w", err)
	}
	return nil
}

func (outcome PublicationCoverOutcome) Validate() error {
	switch outcome.State {
	case PublicationAssetAbsent:
		if outcome.SHA256 != "" || outcome.MIME != "" || outcome.SizeBytes != nil {
			return errors.New("absent cover outcome must not contain asset data")
		}
	case PublicationAssetEmbedded:
		if !publicationSHA256(outcome.SHA256) || !validPublicationMIME(outcome.MIME) || outcome.SizeBytes == nil || *outcome.SizeBytes <= 0 {
			return errors.New("embedded cover requires a valid digest, MIME type and positive size")
		}
	default:
		return fmt.Errorf("unsupported publication asset state %q", outcome.State)
	}
	return nil
}

func (outcome PublicationLyricsOutcome) Validate() error {
	switch outcome.State {
	case PublicationAssetAbsent:
		if outcome.SHA256 != "" || outcome.Format != "" {
			return errors.New("absent lyrics outcome must not contain asset data")
		}
	case PublicationAssetEmbedded:
		if !publicationSHA256(outcome.SHA256) || outcome.Format != "lrc" {
			return errors.New("embedded lyrics require a valid digest and lrc format")
		}
	default:
		return fmt.Errorf("unsupported publication asset state %q", outcome.State)
	}
	return nil
}

// ValidateAgainstMetadata checks read-back facts, without normalizing lyric
// bytes: the digest is over the exact single LYRIC value as observed.
func (outcomes PublicationAssetOutcomes) ValidateAgainstMetadata(tags map[string][]string, lyricBytes []byte) error {
	if err := outcomes.Validate(); err != nil {
		return err
	}
	lyrics := tags["LYRICS"]
	if len(lyrics) > 1 {
		return errors.New("publication metadata contains multiple LYRICS values")
	}
	if outcomes.Lyrics.State == PublicationAssetAbsent {
		if len(lyrics) != 0 && lyrics[0] != "" {
			return errors.New("absent lyrics outcome has non-empty LYRICS metadata")
		}
		if len(lyricBytes) != 0 {
			return errors.New("absent lyrics outcome has lyric bytes")
		}
		return nil
	}
	if len(lyrics) != 1 || len(lyricBytes) != 0 && !bytes.Equal([]byte(lyrics[0]), lyricBytes) {
		return errors.New("embedded lyrics must have exactly one matching LYRICS metadata value")
	}
	if !utf8.ValidString(lyrics[0]) {
		return errors.New("embedded lyrics are not valid UTF-8")
	}
	syncedLyrics, err := lrclib.ValidateSyncedLyrics(lyrics[0])
	if err != nil || syncedLyrics == "" {
		return errors.New("embedded lyrics are not valid synced LRC")
	}
	digest := sha256.Sum256([]byte(lyrics[0]))
	if hex.EncodeToString(digest[:]) != outcomes.Lyrics.SHA256 {
		return errors.New("embedded lyrics digest does not match LYRICS metadata")
	}
	return nil
}

func (outcomes PublicationAssetOutcomes) Clone() PublicationAssetOutcomes {
	if outcomes.Cover.SizeBytes != nil {
		size := *outcomes.Cover.SizeBytes
		outcomes.Cover.SizeBytes = &size
	}
	return outcomes
}

var publicationAssetSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func publicationSHA256(value string) bool { return publicationAssetSHA256.MatchString(value) }

func validPublicationMIME(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.HasPrefix(mediaType, "image/")
}
