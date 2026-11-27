package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type fallbackCaptureAnalysisInput struct {
	AnalysisRecordID string          `json:"analysis_record_id"`
	WorkID           uuid.UUID       `json:"work_id"`
	Step             string          `json:"step"`
	State            string          `json:"state"`
	InputSnapshot    json.RawMessage `json:"input_snapshot"`
	InputDigest      *string         `json:"input_digest"`
	ResultReference  *uuid.UUID      `json:"result_reference"`
}

type fallbackCaptureMember struct {
	VariantID      uuid.UUID                                    `json:"variant_id"`
	Ready          bool                                         `json:"ready"`
	Locations      []persistence.IncomingGroupingMemberLocation `json:"locations"`
	Captures       []persistence.IncomingGroupingCapture        `json:"captures"`
	AnalysisInputs []fallbackCaptureAnalysisInput               `json:"analysis_inputs"`
}

// fallbackCapture is the complete persisted target-capture schema shared by the
// frozen inventory and review-only evaluation paths.
type fallbackCapture struct {
	SchemaVersion     int                     `json:"schema_version"`
	GroupID           uuid.UUID               `json:"group_id"`
	GroupRevision     string                  `json:"group_revision"`
	AuthorityRevision int64                   `json:"authority_revision"`
	Members           []fallbackCaptureMember `json:"members"`
}

// UnmarshalJSON validates and reads the complete persisted target-capture
// schema. Locations and analysis state are authority-bearing persisted fields,
// even though consumers may only need the capture and selected metadata.
func (capture *fallbackCapture) UnmarshalJSON(raw []byte) error {
	var persisted struct {
		SchemaVersion       int                     `json:"schema_version"`
		GroupID             uuid.UUID               `json:"group_id"`
		GroupRevision       string                  `json:"group_revision"`
		ActiveGroupRevision string                  `json:"active_group_revision,omitempty"`
		AuthorityRevision   int64                   `json:"authority_revision"`
		Members             []fallbackCaptureMember `json:"members"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&persisted); err != nil {
		return err
	}
	*capture = fallbackCapture{
		SchemaVersion:     persisted.SchemaVersion,
		GroupID:           persisted.GroupID,
		GroupRevision:     persisted.GroupRevision,
		AuthorityRevision: persisted.AuthorityRevision,
		Members:           persisted.Members,
	}
	return nil
}

// fallbackCanonicalMBID returns the canonical lowercase spelling of a release
// or recording MBID so accepted hits match identity regardless of the provider's
// letter case. Synthetic non-UUID keys pass through unchanged.
func fallbackCanonicalMBID(id string) string {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return id
	}
	return parsed.String()
}

func acceptedFallbackSearchScores(queryKey, kind string, payload json.RawMessage, releaseIDs []string) (map[string]map[string]persistence.MatchingSearchScore, error) {
	var envelope struct {
		Releases []struct {
			ID    string          `json:"id"`
			Score json.RawMessage `json:"score"`
		} `json:"releases"`
		Recordings []struct {
			ID       string          `json:"id"`
			Score    json.RawMessage `json:"score"`
			Releases []struct {
				ID string `json:"id"`
			} `json:"releases"`
		} `json:"recordings"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("fallback applicability: malformed search response")
	}
	allowed := make(map[string]bool, len(releaseIDs))
	for _, id := range releaseIDs {
		allowed[fallbackCanonicalMBID(id)] = true
	}
	scores := make(map[string]map[string]persistence.MatchingSearchScore)
	put := func(id string, raw json.RawMessage) error {
		if id == "" {
			return fmt.Errorf("fallback applicability: search hit has no identity")
		}
		value := persistence.MatchingSearchScore{}
		if len(raw) != 0 && string(raw) != "null" {
			if json.Unmarshal(raw, &value.Value) != nil || math.IsNaN(value.Value) || math.IsInf(value.Value, 0) || value.Value < 0 || value.Value > 100 {
				return fmt.Errorf("fallback applicability: search score is invalid")
			}
			value.Present = true
		}
		if scores[queryKey] == nil {
			scores[queryKey] = make(map[string]persistence.MatchingSearchScore)
		}
		scores[queryKey][id] = value
		return nil
	}
	if kind == "provider_release_search" {
		for _, hit := range envelope.Releases {
			id := fallbackCanonicalMBID(hit.ID)
			if !allowed[id] {
				continue
			}
			if err := put(id, hit.Score); err != nil {
				return nil, err
			}
		}
	} else {
		for _, hit := range envelope.Recordings {
			recording := persistence.MatchingSearchScore{}
			if len(hit.Score) != 0 && string(hit.Score) != "null" {
				if json.Unmarshal(hit.Score, &recording.Value) != nil || math.IsNaN(recording.Value) || math.IsInf(recording.Value, 0) || recording.Value < 0 || recording.Value > 100 {
					return nil, fmt.Errorf("fallback applicability: search score is invalid")
				}
				recording.Present = true
			}
			recordingID := fallbackCanonicalMBID(hit.ID)
			for _, release := range hit.Releases {
				if allowed[fallbackCanonicalMBID(release.ID)] {
					if scores[queryKey] == nil {
						scores[queryKey] = make(map[string]persistence.MatchingSearchScore)
					}
					scores[queryKey][recordingID] = recording
				}
			}
		}
	}
	return scores, nil
}

func decodeFallbackStrict(raw json.RawMessage, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(output)
}

func canonicalFallbackJSON(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("JSON has trailing data")
	}
	return json.Marshal(value)
}

func isFallbackSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
