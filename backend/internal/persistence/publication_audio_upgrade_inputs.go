package persistence

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// PublicationAudioUpgradeInput freezes the CURRENT artifact witness required
// to replace its audio while preserving lyrics and artwork.
// ReceiptTrackSourceID marks an explicit matching-receipt upgrade bound to that
// exact confirmation association. A nil marker is the ordinary reconciliation
// upgrade and remains automatic-only; a present marker preserves whichever live
// source-authority mode typed receipt admission confirmed.
type PublicationAudioUpgradeInput struct {
	Current                 PublicationCurrentWitness `json:"current"`
	SourceAuthorityRevision uuid.UUID                 `json:"source_authority_revision"`
	ReceiptTrackSourceID    *uuid.UUID                `json:"receipt_track_source_id,omitempty"`
}

// PublicationCurrentWitness is neutral factual evidence about the selected
// CURRENT publication. Its audio facts may be insufficient for a quality policy.
type PublicationCurrentWitness struct {
	Generation       uuid.UUID                   `json:"generation"`
	RelativePath     string                      `json:"relative_path"`
	Ownership        PublicationFileIdentity     `json:"ownership"`
	AudioFacts       PersistedAudioFacts         `json:"audio_facts"`
	Assets           PublicationAssetOutcomes    `json:"assets"`
	Tags             map[string][]string         `json:"tags"`
	ArtworkInventory []PublicationArtworkWitness `json:"artwork_inventory"`
}

// PublicationArtworkWitness authorizes preservation of every CURRENT image,
// including native ordering and descriptors, without retaining image bytes.
type PublicationArtworkWitness struct {
	Index       int    `json:"index"`
	Type        string `json:"type"`
	Description string `json:"description"`
	MIME        string `json:"mime"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}

func (input PublicationAudioUpgradeInput) Validate() error {
	if input.SourceAuthorityRevision == uuid.Nil {
		return errors.New("audio upgrade source authority revision is required")
	}
	if input.ReceiptTrackSourceID != nil && *input.ReceiptTrackSourceID == uuid.Nil {
		return errors.New("audio upgrade receipt track source must be a valid association")
	}
	if err := input.Current.Validate(); err != nil {
		return fmt.Errorf("invalid current publication witness: %w", err)
	}
	return nil
}

func (current PublicationCurrentWitness) Validate() error {
	if current.Generation == uuid.Nil || !frozenPublicationRelativePath(current.RelativePath) {
		return errors.New("current generation and portable published path are required")
	}
	if current.Ownership.SizeBytes == nil || current.Ownership.MTime == nil || !validSHA256(current.Ownership.SHA256) {
		return errors.New("current publication size and modification-time ownership evidence are required")
	}
	if err := (PublicationRewriteInput{CurrentGeneration: current.Generation, RelativePath: current.RelativePath, Ownership: current.Ownership, AudioFacts: current.AudioFacts}).Validate(); err != nil {
		return err
	}
	if err := current.Assets.Validate(); err != nil {
		return fmt.Errorf("invalid current publication assets: %w", err)
	}
	if current.Tags == nil {
		return errors.New("current publication tags are required")
	}
	if current.ArtworkInventory == nil {
		return errors.New("current artwork inventory must explicitly include every image")
	}
	for index, image := range current.ArtworkInventory {
		if image.Index != index || image.Size < 0 || !validSHA256(image.SHA256) {
			return errors.New("current artwork inventory contains an invalid image witness")
		}
	}
	for name, values := range current.Tags {
		if strings.TrimSpace(name) == "" || values == nil {
			return errors.New("current publication tags must contain named, non-nil value lists")
		}
	}
	return nil
}
