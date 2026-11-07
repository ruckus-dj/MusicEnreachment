package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type FirstPublicationCoverPlan struct {
	AuthorityRevision   int64                       `json:"authority_revision"`
	AuthorityMode       string                      `json:"authority_mode"`
	AuthorityGeneration string                      `json:"authority_generation,omitempty"`
	ReleaseID           string                      `json:"release_id"`
	Original            *FirstPublicationCoverFacts `json:"original,omitempty"`
	Derivative          *FirstPublicationCoverFacts `json:"derivative,omitempty"`
	Derivation          string                      `json:"derivation"`
	FailureReason       string                      `json:"failure_reason,omitempty"`
	DerivativeBytes     []byte                      `json:"-"`
}

type FirstPublicationCoverFacts struct {
	SHA256 string `json:"sha256"`
	MIME   string `json:"mime"`
	Size   int64  `json:"size"`
}

func planFirstPublicationCover(ctx context.Context, tempDir string, input persistence.PublicationAttemptCoverInput) (FirstPublicationCoverPlan, error) {
	if input.ReleaseID == [16]byte{} || input.Revision < 1 || (input.Mode != "automatic" && input.Mode != "manual") {
		return FirstPublicationCoverPlan{}, errors.New("first publication cover authority is incomplete")
	}
	plan := FirstPublicationCoverPlan{AuthorityRevision: input.Revision, AuthorityMode: input.Mode, ReleaseID: input.ReleaseID.String()}
	switch input.State {
	case "absent":
		if len(input.Bytes) != 0 || input.MIME != nil || len(input.SHA256) != 0 || input.Size != nil || input.Generation != nil {
			return FirstPublicationCoverPlan{}, errors.New("first publication absent cover contains image data")
		}
		plan.Derivation = "absent"
		return plan, nil
	case "value":
		if input.MIME == nil || input.Size == nil || *input.Size != int64(len(input.Bytes)) || len(input.Bytes) == 0 {
			return FirstPublicationCoverPlan{}, errors.New("first publication cover snapshot is incomplete")
		}
		digest := sha256.Sum256(input.Bytes)
		if !bytes.Equal(digest[:], input.SHA256) {
			return FirstPublicationCoverPlan{}, errors.New("first publication cover snapshot digest is invalid")
		}
		plan.Original = &FirstPublicationCoverFacts{SHA256: hex.EncodeToString(digest[:]), MIME: *input.MIME, Size: *input.Size}
		if input.Generation == nil || *input.Generation == [16]byte{} {
			return FirstPublicationCoverPlan{}, errors.New("first publication cover authority generation is incomplete")
		}
		plan.AuthorityGeneration = input.Generation.String()
	default:
		return FirstPublicationCoverPlan{}, errors.New("first publication cover snapshot state is invalid")
	}
	if err := ctx.Err(); err != nil {
		return FirstPublicationCoverPlan{}, err
	}
	privateDir, err := os.MkdirTemp(tempDir, "first-cover-plan-")
	if err != nil {
		if ctx.Err() != nil {
			return FirstPublicationCoverPlan{}, ctx.Err()
		}
		plan.Derivation, plan.FailureReason = "optional_failure", "selected cover could not be converted to a publication derivative"
		return plan, nil
	}
	defer func() { _ = os.RemoveAll(privateDir) }()
	if err := os.Chmod(privateDir, 0o700); err != nil {
		if ctx.Err() != nil {
			return FirstPublicationCoverPlan{}, ctx.Err()
		}
		plan.Derivation, plan.FailureReason = "optional_failure", "selected cover could not be converted to a publication derivative"
		return plan, nil
	}
	thumbnail, err := publicationCoverThumbnail(ctx, privateDir, &input)
	if err != nil {
		if ctx.Err() != nil {
			return FirstPublicationCoverPlan{}, ctx.Err()
		}
		plan.Derivation, plan.FailureReason = "optional_failure", "selected cover could not be converted to a publication derivative"
		return plan, nil
	}
	digest := sha256.Sum256(thumbnail.Data)
	plan.Derivative = &FirstPublicationCoverFacts{SHA256: hex.EncodeToString(digest[:]), MIME: thumbnail.MIME, Size: int64(len(thumbnail.Data))}
	plan.DerivativeBytes = append([]byte(nil), thumbnail.Data...)
	plan.Derivation = "ready"
	return plan, nil
}
