package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
)

const MatchingReleaseShortlistLimit = 10

// MatchingAcceptedShortlist separates every accepted search hit from the
// ranked subset authorized for layout hydration. Ranking order is not SQL
// child order; callers should sort a copy before registering lookup children.
type MatchingAcceptedShortlist struct {
	ReleaseIDs          []string `json:"release_ids"`
	RankedReleaseIDs    []string `json:"ranked_release_ids"`
	SearchReceiptDigest string   `json:"search_receipt_sha256"`
}

// ReviewOnlyReleaseIDs returns accepted editions excluded from the ranked
// shortlist, in canonical search-universe order.
func (shortlist MatchingAcceptedShortlist) ReviewOnlyReleaseIDs() []string {
	extra := make([]string, 0)
	for _, key := range shortlist.ReleaseIDs {
		if !slices.Contains(shortlist.RankedReleaseIDs, key) {
			extra = append(extra, key)
		}
	}
	return extra
}

// ValidateFallbackSearch checks the shortlist against a registered search
// receipt. It verifies exact coverage and scope, not the service's text rank.
func (shortlist MatchingAcceptedShortlist) ValidateFallbackSearch(kind string, receipt MatchingFallbackSearchReceipt) error {
	if receipt.PayloadDigestVersion != 2 || receipt.RequestID == uuid.Nil ||
		receipt.PayloadSHA256 != shortlist.SearchReceiptDigest {
		return errors.New("matching shortlist search receipt identity is invalid")
	}
	payload, err := canonicalMatchingJSON(receipt.Payload)
	if err != nil {
		return fmt.Errorf("decode matching shortlist search receipt: %w", err)
	}
	digest := sha256.Sum256(payload)
	if hex.EncodeToString(digest[:]) != receipt.PayloadSHA256 {
		return errors.New("matching shortlist search receipt digest changed")
	}
	accepted, err := fallbackReceiptReleaseIDs(kind, payload)
	if err != nil {
		return fmt.Errorf("read matching shortlist search universe: %w", err)
	}
	if !slices.Equal(accepted, shortlist.ReleaseIDs) {
		return errors.New("matching shortlist does not retain the full accepted release universe")
	}
	if len(shortlist.RankedReleaseIDs) != min(len(accepted), MatchingReleaseShortlistLimit) {
		return errors.New("matching shortlist has an invalid number of ranked releases")
	}
	scope := make(map[string]bool, len(accepted))
	for _, key := range accepted {
		if key == uuid.Nil.String() {
			return errors.New("matching shortlist has an invalid release identity")
		}
		scope[key] = true
	}
	for _, key := range shortlist.RankedReleaseIDs {
		if !scope[key] {
			return errors.New("matching shortlist has an off-scope or repeated release")
		}
		delete(scope, key)
	}
	return nil
}
