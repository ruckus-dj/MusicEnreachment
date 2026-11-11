package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/uptrace/bun"
)

type persistedArtistCreditOrigin struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
}

func readArtistCreditDisplay(ctx context.Context, tx bun.Tx, scope librarymetadata.Scope, releaseID, ownerID uuid.UUID, envelope librarymetadata.Envelope) ([]librarymetadata.ArtistCredit, *librarymetadata.ResolvedArtistCredits, error) {
	switch scope {
	case librarymetadata.ScopeRelease:
		baseline, err := readLocalArtistCredits(ctx, tx, "release_artist", "release_id", ownerID)
		if err != nil {
			return nil, nil, err
		}
		resolved, err := librarymetadata.ResolveReleaseArtistCredits(&envelope, baseline)
		if err == nil {
			resolved.Credits, err = hydrateArtistCredits(ctx, tx, resolved.Credits)
		}
		return baseline, resolvedPointer(resolved), err
	case librarymetadata.ScopeRecording:
		baseline, err := readLocalArtistCredits(ctx, tx, "recording_artist", "recording_id", ownerID)
		if err != nil {
			return nil, nil, err
		}
		resolved, err := librarymetadata.ResolveRecordingArtistCredits(&envelope, baseline)
		if err == nil {
			resolved.Credits, err = hydrateArtistCredits(ctx, tx, resolved.Credits)
		}
		return baseline, resolvedPointer(resolved), err
	case librarymetadata.ScopeTrack:
		baseline, err := readLocalArtistCredits(ctx, tx, "track_artist", "track_id", ownerID)
		if err != nil {
			return nil, nil, err
		}
		trackMetadata, err := readLocalOwnerMetadata(ctx, tx, "local_track", ownerID)
		if err != nil {
			return nil, nil, err
		}
		manual := manualCreditOverridePresent(trackMetadata)
		var manualCandidate, providerCandidate librarymetadata.CreditCandidate
		if manual {
			manualCandidate = librarymetadata.CreditCandidate{Present: true, Credits: baseline, Origin: librarymetadata.CreditOriginManualMatching}
		} else if !hasDesiredCredit(envelope) {
			marker, hasMarker, err := artistCreditOrigin(trackMetadata)
			if err != nil {
				return nil, nil, err
			}
			if hasMarker && marker.Kind == artistCreditOriginProviderPosition {
				providerCandidate = librarymetadata.CreditCandidate{Present: true, Credits: baseline, Origin: librarymetadata.CreditOriginProviderPosition}
			} else if !hasMarker {
				return baseline, unresolvedArtistCredits(librarymetadata.CreditResolutionUnclassified), nil
			} else {
				return baseline, unresolvedArtistCredits(librarymetadata.CreditResolutionUnavailable), nil
			}
		}
		resolved, err := librarymetadata.ResolveArtistCredits(&envelope, manualCandidate, providerCandidate, nil, librarymetadata.CreditCandidate{})
		if err == nil {
			resolved.Credits, err = hydrateArtistCredits(ctx, tx, resolved.Credits)
		}
		return baseline, resolvedPointer(resolved), err
	case librarymetadata.ScopePosition:
		var owner struct {
			TrackID     uuid.UUID  `bun:"track_id,type:uuid"`
			RecordingID *uuid.UUID `bun:"recording_id,type:uuid"`
		}
		if err := tx.NewRaw(`SELECT track_id,recording_id FROM release_track WHERE id=? AND release_id=?`, ownerID, releaseID).Scan(ctx, &owner); err != nil {
			return nil, nil, err
		}
		baseline, err := readLocalArtistCredits(ctx, tx, "track_artist", "track_id", owner.TrackID)
		if err != nil {
			return nil, nil, err
		}
		trackMetadata, err := readLocalOwnerMetadata(ctx, tx, "local_track", owner.TrackID)
		if err != nil {
			return nil, nil, err
		}
		var manualCandidate, providerCandidate librarymetadata.CreditCandidate
		if manualCreditOverridePresent(trackMetadata) {
			manualCandidate = librarymetadata.CreditCandidate{Present: true, Credits: baseline, Origin: librarymetadata.CreditOriginManualMatching}
		} else if !hasDesiredCredit(envelope) {
			marker, hasMarker, err := artistCreditOrigin(trackMetadata)
			if err != nil {
				return nil, nil, err
			}
			if hasMarker && marker.Kind == artistCreditOriginProviderPosition {
				providerCandidate = librarymetadata.CreditCandidate{Present: true, Credits: baseline, Origin: librarymetadata.CreditOriginProviderPosition}
			} else if !hasMarker {
				return baseline, unresolvedArtistCredits(librarymetadata.CreditResolutionUnclassified), nil
			} else if owner.RecordingID == nil {
				return baseline, unresolvedArtistCredits(librarymetadata.CreditResolutionUnavailable), nil
			}
		}
		var recordingEnvelope *librarymetadata.Envelope
		var recordingCandidate librarymetadata.CreditCandidate
		if owner.RecordingID != nil {
			recordingMetadata, err := readLocalOwnerMetadata(ctx, tx, "local_recording", *owner.RecordingID)
			if err != nil {
				return nil, nil, err
			}
			decoded, err := decodeMetadataOwnerEnvelope(recordingMetadata, librarymetadata.ScopeRecording)
			if err != nil {
				return nil, nil, err
			}
			recordingEnvelope = &decoded
			recordingCredits, err := readLocalArtistCredits(ctx, tx, "recording_artist", "recording_id", *owner.RecordingID)
			if err != nil {
				return nil, nil, err
			}
			recordingCandidate = librarymetadata.CreditCandidate{Present: true, Credits: recordingCredits, Origin: librarymetadata.CreditOriginRecording}
		}
		resolved, err := librarymetadata.ResolveArtistCredits(&envelope, manualCandidate, providerCandidate, recordingEnvelope, recordingCandidate)
		if err != nil {
			return nil, nil, err
		}
		resolved.Credits, err = hydrateArtistCredits(ctx, tx, resolved.Credits)
		if err == nil && resolved.Status != librarymetadata.CreditResolutionResolved {
			resolved.Status = librarymetadata.CreditResolutionUnavailable
		}
		return baseline, resolvedPointer(resolved), err
	default:
		return nil, nil, nil
	}
}

func unresolvedArtistCredits(status librarymetadata.CreditResolutionStatus) *librarymetadata.ResolvedArtistCredits {
	return &librarymetadata.ResolvedArtistCredits{Credits: []librarymetadata.ArtistCredit{}, Status: status}
}

func resolvedPointer(resolved librarymetadata.ResolvedArtistCredits) *librarymetadata.ResolvedArtistCredits {
	return &resolved
}

func readLocalArtistCredits(ctx context.Context, tx bun.IDB, relation, parentColumn string, parentID uuid.UUID) ([]librarymetadata.ArtistCredit, error) {
	if (relation != "release_artist" && relation != "recording_artist" && relation != "track_artist") || (parentColumn != "release_id" && parentColumn != "recording_id" && parentColumn != "track_id") {
		return nil, errors.New("invalid local artist credit relation")
	}
	var credits []librarymetadata.ArtistCredit
	query := `SELECT credit.artist_id::text AS artist_id,credit.name AS credit_name,credit.name_join_phrase AS join_phrase FROM ` + relation + ` credit WHERE credit.` + parentColumn + `=? ORDER BY credit.position`
	if err := tx.NewRaw(query, parentID).Scan(ctx, &credits); err != nil {
		return nil, fmt.Errorf("read local artist credits: %w", err)
	}
	for index := range credits {
		if credits[index].ArtistName == "" {
			credits[index].ArtistName = credits[index].CreditName
		}
	}
	return hydrateArtistCredits(ctx, tx, credits)
}

func readLocalOwnerMetadata(ctx context.Context, tx bun.IDB, table string, id uuid.UUID) (json.RawMessage, error) {
	if table != "local_track" && table != "local_recording" {
		return nil, errors.New("invalid local metadata owner")
	}
	// Scan into a row struct: a top-level *json.RawMessage is treated by Bun as a
	// slice destination and scans each column into a byte element.
	var row struct {
		Metadata json.RawMessage `bun:"metadata,type:jsonb"`
	}
	if err := tx.NewRaw(`SELECT metadata FROM `+table+` WHERE id=?`, id).Scan(ctx, &row); err != nil {
		return nil, err
	}
	return row.Metadata, nil
}

func decodeMetadataOwnerEnvelope(metadata json.RawMessage, scope librarymetadata.Scope) (librarymetadata.Envelope, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &root); err != nil {
		return librarymetadata.Envelope{}, fmt.Errorf("decode local metadata owner: %w", err)
	}
	if len(root["local_metadata"]) == 0 {
		return librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: scope, Desired: map[string]librarymetadata.FieldValue{}}, nil
	}
	return librarymetadata.DecodeEnvelope(root["local_metadata"])
}

func manualCreditOverridePresent(metadata json.RawMessage) bool {
	var root map[string]json.RawMessage
	if json.Unmarshal(metadata, &root) != nil {
		return false
	}
	var override map[string]json.RawMessage
	return json.Unmarshal(root["manual_matching_override"], &override) == nil && override != nil && override["artist_overrides"] != nil
}

func hasDesiredCredit(envelope librarymetadata.Envelope) bool {
	_, exists := envelope.Desired["artist_credit"]
	return exists
}

func artistCreditOrigin(metadata json.RawMessage) (persistedArtistCreditOrigin, bool, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &root); err != nil {
		return persistedArtistCreditOrigin{}, false, fmt.Errorf("decode track metadata: %w", err)
	}
	encoded := root["artist_credit_origin"]
	if len(encoded) == 0 || string(encoded) == "null" {
		return persistedArtistCreditOrigin{}, false, nil
	}
	var marker persistedArtistCreditOrigin
	if err := json.Unmarshal(encoded, &marker); err != nil || marker.Version != 1 {
		return persistedArtistCreditOrigin{}, false, errors.New("artist credit origin marker is invalid")
	}
	if marker.Kind != artistCreditOriginProviderPosition && marker.Kind != artistCreditOriginRecordingFallback && marker.Kind != artistCreditOriginManualMatching {
		return persistedArtistCreditOrigin{}, false, errors.New("artist credit origin marker is invalid")
	}
	return marker, true, nil
}
