package persistence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
)

const PublicationCoverEmbeddingProjectionVersion = 1

// PublicationCoverEmbeddingProjection is the bounded current result of applying
// a frozen first-audio cover receipt. It contains no cover bytes or paths.
type PublicationCoverEmbeddingProjection struct {
	Version             int       `json:"version"`
	Generation          uuid.UUID `json:"generation"`
	ReceiptOperationID  uuid.UUID `json:"receipt_operation_id"`
	ReceiptReleaseID    uuid.UUID `json:"receipt_release_id"`
	ReceiptRevision     int64     `json:"receipt_revision"`
	ReceiptMode         string    `json:"receipt_mode"`
	RequestedGeneration uuid.UUID `json:"requested_generation"`
	RequestedSHA256     string    `json:"requested_sha256"`
	Outcome             string    `json:"outcome"`
	WarningCode         string    `json:"warning_code"`
}

func (projection PublicationCoverEmbeddingProjection) Validate() error {
	if projection.Version != PublicationCoverEmbeddingProjectionVersion || projection.Generation == uuid.Nil ||
		projection.ReceiptOperationID == uuid.Nil || projection.ReceiptReleaseID == uuid.Nil || projection.ReceiptRevision < 1 ||
		(projection.ReceiptMode != "automatic" && projection.ReceiptMode != "manual") ||
		projection.RequestedGeneration == uuid.Nil || !publicationSHA256(projection.RequestedSHA256) {
		return errors.New("publication cover embedding projection identity is invalid")
	}
	wantWarning := ""
	switch projection.Outcome {
	case "verified":
	case "write_failed":
		wantWarning = "cover_write_failed"
	case "read_failed":
		wantWarning = "cover_read_failed"
	case "mismatch":
		wantWarning = "cover_mismatch"
	default:
		return fmt.Errorf("unsupported publication cover embedding outcome %q", projection.Outcome)
	}
	if projection.WarningCode != wantWarning {
		return errors.New("publication cover embedding warning does not match outcome")
	}
	return nil
}

func decodePublicationCoverEmbeddingProjection(raw json.RawMessage) (PublicationCoverEmbeddingProjection, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var projection PublicationCoverEmbeddingProjection
	if err := decoder.Decode(&projection); err != nil {
		return PublicationCoverEmbeddingProjection{}, fmt.Errorf("decode publication cover embedding projection: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return PublicationCoverEmbeddingProjection{}, errors.New("decode publication cover embedding projection: trailing JSON document")
		}
		return PublicationCoverEmbeddingProjection{}, fmt.Errorf("decode publication cover embedding projection: %w", err)
	}
	if err := projection.Validate(); err != nil {
		return PublicationCoverEmbeddingProjection{}, err
	}
	return projection, nil
}

func validPublicationCoverEmbeddingForReceipt(projection PublicationCoverEmbeddingProjection, receipt PublicationAttemptCoverInput, candidateGeneration uuid.UUID, assets PublicationAssetOutcomes) bool {
	if projection.Validate() != nil || receipt.State != "value" || projection.Generation != candidateGeneration ||
		projection.ReceiptOperationID != receipt.OperationID || projection.ReceiptReleaseID != receipt.ReleaseID ||
		projection.ReceiptRevision != receipt.Revision || projection.ReceiptMode != receipt.Mode ||
		receipt.Generation == nil || projection.RequestedGeneration != *receipt.Generation ||
		projection.RequestedSHA256 != fmt.Sprintf("%x", receipt.SHA256) {
		return false
	}
	if projection.Outcome == "verified" {
		return assets.Cover.State == PublicationAssetEmbedded && assets.Cover.SHA256 == projection.RequestedSHA256
	}
	return true
}
