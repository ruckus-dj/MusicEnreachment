package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// AcoustIDLookupRequestIdentity is the exact request identity persisted by
// durable lookup admission. Keep its JSON field names/order stable.
type AcoustIDLookupRequestIdentity struct {
	WorkID                  uuid.UUID `json:"work_id"`
	FingerprintRowID        uuid.UUID `json:"fingerprint_row_id"`
	WinningResultID         uuid.UUID `json:"winning_result_id"`
	WinnerFingerprint       string    `json:"winner_fingerprint"`
	AlgorithmNamespace      string    `json:"algorithm_namespace"`
	AlgorithmID             int       `json:"algorithm_id"`
	FPCalcVersion           string    `json:"fpcalc_version"`
	ParserContractVersion   int       `json:"parser_contract_version"`
	SavedReportedDuration   float64   `json:"saved_reported_duration"`
	SHA256                  string    `json:"sha256,omitempty"`
	ImmutableMemberIdentity string    `json:"immutable_member_identity,omitempty"`
}

func AcoustIDRequestCacheKey(identity AcoustIDLookupRequestIdentity) (string, error) {
	if identity.WorkID == uuid.Nil || identity.FingerprintRowID == uuid.Nil || identity.WinningResultID == uuid.Nil || identity.WinnerFingerprint == "" || identity.AlgorithmNamespace == "" || identity.AlgorithmID < 0 || identity.FPCalcVersion == "" || identity.ParserContractVersion < 1 || identity.SavedReportedDuration <= 0 {
		return "", fmt.Errorf("AcoustID request identity is incomplete")
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", fmt.Errorf("encode AcoustID request identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "acoustid-lookup-v1:" + hex.EncodeToString(digest[:]), nil
}

func AcoustIDRequestIdentityForCapture(capture IncomingGroupingCapture) (AcoustIDLookupRequestIdentity, error) {
	if capture.FingerprintResultID == nil || capture.FingerprintWinner == nil || capture.Fingerprint == nil || capture.FingerprintAlgorithmNamespace == nil || capture.FingerprintAlgorithmID == nil || capture.FingerprintVersion == nil || capture.FingerprintParserVersion == nil || capture.FingerprintDuration == nil {
		return AcoustIDLookupRequestIdentity{}, fmt.Errorf("captured AcoustID fingerprint provenance is incomplete")
	}
	identity := AcoustIDLookupRequestIdentity{
		WorkID: capture.WorkID, FingerprintRowID: *capture.FingerprintResultID,
		WinningResultID: *capture.FingerprintWinner, WinnerFingerprint: *capture.Fingerprint,
		AlgorithmNamespace: *capture.FingerprintAlgorithmNamespace, AlgorithmID: int(*capture.FingerprintAlgorithmID),
		FPCalcVersion: *capture.FingerprintVersion, ParserContractVersion: *capture.FingerprintParserVersion,
		SavedReportedDuration: *capture.FingerprintDuration, ImmutableMemberIdentity: capture.WorkID.String(),
	}
	if len(capture.SourceSHA256) != 0 {
		identity.SHA256 = hex.EncodeToString(capture.SourceSHA256)
	}
	return identity, nil
}

func acoustIDRequestCacheKey(capture IncomingGroupingCapture) (string, error) {
	identity, err := AcoustIDRequestIdentityForCapture(capture)
	if err != nil {
		return "", err
	}
	return AcoustIDRequestCacheKey(identity)
}

func AcoustIDRequestCacheKeyForCapture(capture IncomingGroupingCapture) (string, error) {
	return acoustIDRequestCacheKey(capture)
}

func sameAcoustIDFingerprintIdentity(left, right IncomingGroupingCapture) bool {
	return left.FingerprintResultID != nil && right.FingerprintResultID != nil && *left.FingerprintResultID == *right.FingerprintResultID &&
		left.FingerprintWinner != nil && right.FingerprintWinner != nil && *left.FingerprintWinner == *right.FingerprintWinner &&
		left.Fingerprint != nil && right.Fingerprint != nil && *left.Fingerprint == *right.Fingerprint &&
		left.FingerprintDuration != nil && right.FingerprintDuration != nil && *left.FingerprintDuration == *right.FingerprintDuration &&
		left.FingerprintVersion != nil && right.FingerprintVersion != nil && *left.FingerprintVersion == *right.FingerprintVersion &&
		left.FingerprintAlgorithmNamespace != nil && right.FingerprintAlgorithmNamespace != nil && *left.FingerprintAlgorithmNamespace == *right.FingerprintAlgorithmNamespace &&
		left.FingerprintAlgorithmID != nil && right.FingerprintAlgorithmID != nil && *left.FingerprintAlgorithmID == *right.FingerprintAlgorithmID &&
		left.FingerprintParserVersion != nil && right.FingerprintParserVersion != nil && *left.FingerprintParserVersion == *right.FingerprintParserVersion &&
		string(left.SourceSHA256) == string(right.SourceSHA256)
}

func SameAcoustIDFingerprintIdentity(left, right IncomingGroupingCapture) bool {
	return sameAcoustIDFingerprintIdentity(left, right)
}
