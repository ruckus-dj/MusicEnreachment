package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// ObservePublicationAssets derives publication asset outcomes only from a
// successful metadata capture. The cover observation is TagLib's first
// normalized picture, not an exhaustive inventory or a front-cover claim.
func ObservePublicationAssets(capture metadata.Capture) (persistence.PublicationAssetOutcomes, error) {
	if capture.CoverInspection == nil || !capture.CoverInspection.Observed {
		return persistence.PublicationAssetOutcomes{}, errors.New("publication cover inspection is unavailable")
	}
	if capture.Tags == nil {
		return persistence.PublicationAssetOutcomes{}, errors.New("publication metadata tags are unavailable")
	}

	outcomes := persistence.PublicationAssetOutcomes{
		Version: persistence.PublicationAssetOutcomesVersion,
		Cover:   persistence.PublicationCoverOutcome{State: persistence.PublicationAssetAbsent},
		Lyrics:  persistence.PublicationLyricsOutcome{State: persistence.PublicationAssetAbsent},
	}
	if cover := capture.CoverInspection.Cover; cover != nil {
		if len(cover.Data) == 0 {
			return persistence.PublicationAssetOutcomes{}, errors.New("observed publication cover has no bytes")
		}
		mimeType := http.DetectContentType(cover.Data)
		digest := sha256.Sum256(cover.Data)
		size := int64(len(cover.Data))
		outcomes.Cover = persistence.PublicationCoverOutcome{
			State: persistence.PublicationAssetEmbedded, SHA256: hex.EncodeToString(digest[:]),
			MIME: mimeType, SizeBytes: &size,
		}
	}

	lyrics := capture.Tags["LYRICS"]
	if len(lyrics) > 1 {
		return persistence.PublicationAssetOutcomes{}, errors.New("publication metadata contains multiple LYRICS values")
	}
	if len(lyrics) == 1 && lyrics[0] != "" {
		digest := sha256.Sum256([]byte(lyrics[0]))
		outcomes.Lyrics = persistence.PublicationLyricsOutcome{
			State: persistence.PublicationAssetEmbedded, SHA256: hex.EncodeToString(digest[:]), Format: "lrc",
		}
	}
	if err := outcomes.ValidateAgainstMetadata(capture.Tags, nil); err != nil {
		return persistence.PublicationAssetOutcomes{}, fmt.Errorf("observe publication assets: %w", err)
	}
	return outcomes.Clone(), nil
}
