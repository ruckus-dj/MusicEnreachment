package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/uptrace/bun"
)

var ErrPublicationPositionNotFound = errors.New("publication position not found")

type LibraryPublicationOperation struct {
	ID    uuid.UUID `json:"id"`
	State string    `json:"state"`
	Stage string    `json:"stage"`
}

type LibraryPublicationCurrent struct {
	Record        TrackPublication
	InitialLyrics *PublicationInitialLyricsProjection
	Ownership     PublicationCurrentOwnership
	Audio         PublicationCurrentAudio
	Metadata      PublicationCommitMetadata
	Assets        PublicationAssetOutcomes
}

type LibraryPublicationNamespaceNode struct {
	Claim      PublicationNamespaceClaim
	PositionID *uuid.UUID
	Generation *uuid.UUID
}

type LibraryPublicationContext struct {
	ReleaseID         uuid.UUID
	PositionID        uuid.UUID
	ActiveEpoch       uuid.UUID
	Metadata          LibraryMetadata
	Current           *LibraryPublicationCurrent
	ActiveAttempt     *LibraryPublicationOperation
	AttemptCandidate  *uuid.UUID
	AttemptPhase      *string
	NamespaceManifest []LibraryPublicationNamespaceNode
	ManualLyrics      ManualLyricsReceiptProjection
}

// ReadLibraryPublicationContext returns an atomic, read-only view used by publication
// admission. It deliberately excludes frozen inputs, source selections and journals.
func (repository *SetupManagerRepository) ReadLibraryPublicationContext(ctx context.Context, releaseUUID, positionUUID uuid.UUID) (*LibraryPublicationContext, error) {
	var result *LibraryPublicationContext
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		return readLibraryPublicationContextTx(ctx, tx, releaseUUID, positionUUID, &result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func readLibraryPublicationContextTx(ctx context.Context, tx bun.IDB, releaseUUID, positionUUID uuid.UUID, result **LibraryPublicationContext) error {
	{
		var row struct {
			ReleaseID  uuid.UUID       `bun:"release_id,type:uuid"`
			PositionID uuid.UUID       `bun:"position_id,type:uuid"`
			Metadata   json.RawMessage `bun:"metadata,type:jsonb"`
			Revision   int64           `bun:"metadata_revision"`
			Epoch      uuid.UUID       `bun:"active_epoch,type:uuid"`
		}
		err := tx.NewRaw(`SELECT rt.release_id, rt.id AS position_id, rt.metadata, rt.metadata_revision, ns.output_epoch AS active_epoch
			FROM release_track rt CROSS JOIN publication_namespace_state ns
			WHERE rt.id=? AND rt.release_id=? AND ns.singleton=true`, positionUUID, releaseUUID).Scan(ctx, &row)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPublicationPositionNotFound
		}
		if err != nil {
			return fmt.Errorf("read publication position context: %w", err)
		}
		metadata, err := decodePositionPublicationMetadata(row.Metadata, row.Revision)
		if err != nil {
			return err
		}
		*result = &LibraryPublicationContext{ReleaseID: row.ReleaseID, PositionID: row.PositionID, ActiveEpoch: row.Epoch, Metadata: metadata}

		var current TrackPublication
		err = tx.NewSelect().Model(&current).Where("position_id=?", positionUUID).Scan(ctx)
		if err == nil {
			decoded := &LibraryPublicationCurrent{Record: current}
			if err := decodePublicationCurrentContract(current.Ownership, &decoded.Ownership); err != nil {
				return fmt.Errorf("decode current publication ownership: %w", err)
			}
			if err := decodePublicationCurrentContract(current.ActualAudio, &decoded.Audio); err != nil {
				return fmt.Errorf("decode current publication audio: %w", err)
			}
			if err := strictMetadata(current.ActualMetadata, &decoded.Metadata); err != nil {
				return fmt.Errorf("decode current publication metadata: %w", err)
			}
			if len(current.InitialLyrics) != 0 && string(current.InitialLyrics) != "null" {
				var projection PublicationInitialLyricsProjection
				if err := decodeStrictJSON(current.InitialLyrics, &projection); err != nil || !validPublicationInitialLyricsProjection(projection, current.Generation) {
					return errors.New("decode current initial lyrics evidence: invalid generation-bound projection")
				}
				decoded.InitialLyrics = &projection
			}
			assets, err := DecodePublicationAssetOutcomes(current.ActualAssets)
			if err != nil {
				return fmt.Errorf("decode current publication assets: %w", err)
			}
			decoded.Assets = assets
			(*result).Current = decoded
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read current publication: %w", err)
		}

		var attempt struct {
			OperationID uuid.UUID `bun:"operation_id,type:uuid"`
			Candidate   uuid.UUID `bun:"candidate_generation,type:uuid"`
			Phase       string    `bun:"phase"`
			State       string    `bun:"state"`
			Stage       string    `bun:"stage"`
		}
		err = tx.NewRaw(`SELECT a.operation_id, a.candidate_generation, a.phase, o.state, o.stage
			FROM track_publication_attempt a JOIN operation o ON o.id=a.operation_id WHERE a.position_id=?`, positionUUID).Scan(ctx, &attempt)
		if err == nil {
			(*result).ActiveAttempt = &LibraryPublicationOperation{ID: attempt.OperationID, State: attempt.State, Stage: attempt.Stage}
			(*result).AttemptCandidate = &attempt.Candidate
			(*result).AttemptPhase = &attempt.Phase
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read publication attempt summary: %w", err)
		}

		var rows []struct {
			ID                uuid.UUID  `bun:"id,type:uuid"`
			Epoch             uuid.UUID  `bun:"epoch,type:uuid"`
			KeyDigest         []byte     `bun:"key_digest"`
			CanonicalKey      string     `bun:"canonical_key"`
			RelativePath      string     `bun:"relative_path"`
			NodeKind          string     `bun:"node_kind"`
			OwnerPositionID   *uuid.UUID `bun:"owner_position_id,type:uuid"`
			DirectoryScopeKey *string    `bun:"directory_scope_key"`
			PositionID        *uuid.UUID `bun:"position_id,type:uuid"`
			Generation        *uuid.UUID `bun:"generation,type:uuid"`
		}
		if (*result).Current != nil {
			err = tx.NewRaw(`SELECT c.id, c.epoch, c.key_digest, c.canonical_key, c.relative_path, c.node_kind,
			c.owner_position_id, c.directory_scope_key, r.position_id, r.generation
			FROM publication_namespace_claim c JOIN publication_namespace_reservation r ON r.namespace_claim_id=c.id
			WHERE c.epoch=? AND r.position_id=? AND r.generation=?
				ORDER BY c.canonical_key`, (*result).Current.Record.Epoch, positionUUID, (*result).Current.Record.Generation).Scan(ctx, &rows)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read publication namespace manifest: %w", err)
		}
		(*result).NamespaceManifest = make([]LibraryPublicationNamespaceNode, 0, len(rows))
		for _, node := range rows {
			(*result).NamespaceManifest = append((*result).NamespaceManifest, LibraryPublicationNamespaceNode{
				Claim:      PublicationNamespaceClaim{ID: node.ID, Epoch: node.Epoch, KeyDigest: node.KeyDigest, CanonicalKey: node.CanonicalKey, RelativePath: node.RelativePath, NodeKind: node.NodeKind, OwnerPositionID: node.OwnerPositionID, DirectoryScopeKey: node.DirectoryScopeKey},
				PositionID: node.PositionID, Generation: node.Generation,
			})
		}
		(*result).ManualLyrics, err = readManualLyricsReceiptProjection(ctx, tx, positionUUID)
		if err != nil {
			return fmt.Errorf("read manual lyrics receipt: %w", err)
		}
		return nil
	}
}

func decodePositionPublicationMetadata(raw json.RawMessage, revision int64) (LibraryMetadata, error) {
	result := LibraryMetadata{Envelope: librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{}}, Revision: revision}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return LibraryMetadata{}, fmt.Errorf("decode position metadata: %w", err)
	}
	if value := root["local_metadata"]; len(value) != 0 {
		envelope, err := librarymetadata.DecodeEnvelope(value)
		if err != nil {
			return LibraryMetadata{}, fmt.Errorf("decode position metadata envelope: %w", err)
		}
		if envelope.Version != librarymetadata.EnvelopeVersion || envelope.Scope != librarymetadata.ScopePosition || envelope.Desired == nil {
			return LibraryMetadata{}, errors.New("position metadata envelope does not match owner")
		}
		if err := envelope.Validate(); err != nil {
			return LibraryMetadata{}, fmt.Errorf("validate position metadata envelope: %w", err)
		}
		result.Envelope = envelope
	}
	return result, nil
}
