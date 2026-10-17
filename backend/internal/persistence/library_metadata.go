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

var (
	ErrLibraryMetadataNotFound = errors.New("library metadata owner not found")
	ErrLibraryMetadataConflict = errors.New("library metadata revision conflict")
)

type LibraryMetadata struct {
	Envelope librarymetadata.Envelope
	Revision int64
}

type metadataOwner struct {
	table     string
	updatedAt bool
	where     string
}

func metadataOwnerForScope(scope librarymetadata.Scope) (metadataOwner, bool) {
	switch scope {
	case librarymetadata.ScopeRelease:
		return metadataOwner{table: "local_release", updatedAt: true, where: "id=?"}, true
	case librarymetadata.ScopeRecording:
		return metadataOwner{table: "local_recording", updatedAt: true, where: "id=?"}, true
	case librarymetadata.ScopeTrack:
		return metadataOwner{table: "local_track", updatedAt: true, where: "id=?"}, true
	case librarymetadata.ScopePosition:
		return metadataOwner{table: "release_track", where: "id=? AND release_id=?"}, true
	default:
		return metadataOwner{}, false
	}
}

func (repository *LibraryRepository) ReadMetadata(ctx context.Context, scope librarymetadata.Scope, releaseID, ownerID uuid.UUID) (LibraryMetadata, error) {
	result := LibraryMetadata{Envelope: librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: scope, Desired: map[string]librarymetadata.FieldValue{}}}
	var metadata []byte
	var query string
	var args []any
	switch scope {
	case librarymetadata.ScopeRelease:
		query, args = `SELECT metadata,metadata_revision FROM local_release WHERE id=?`, []any{ownerID}
	case librarymetadata.ScopeRecording:
		query, args = `SELECT metadata,metadata_revision FROM local_recording WHERE id=?`, []any{ownerID}
	case librarymetadata.ScopeTrack:
		query, args = `SELECT metadata,metadata_revision FROM local_track WHERE id=?`, []any{ownerID}
	case librarymetadata.ScopePosition:
		query, args = `SELECT metadata,metadata_revision FROM release_track WHERE id=? AND release_id=?`, []any{ownerID, releaseID}
	default:
		return LibraryMetadata{}, fmt.Errorf("read library metadata: unknown scope %q", scope)
	}
	if err := repository.db.NewRaw(query, args...).Scan(ctx, &metadata, &result.Revision); errors.Is(err, sql.ErrNoRows) {
		return LibraryMetadata{}, ErrLibraryMetadataNotFound
	} else if err != nil {
		return LibraryMetadata{}, fmt.Errorf("read library metadata: %w", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &root); err != nil {
		return LibraryMetadata{}, fmt.Errorf("read library metadata: %w", err)
	}
	if desired := root["local_metadata"]; len(desired) != 0 {
		stored, err := librarymetadata.DecodeEnvelope(desired)
		if err != nil {
			return LibraryMetadata{}, fmt.Errorf("read library metadata envelope: %w", err)
		}
		if stored.Version != librarymetadata.EnvelopeVersion || stored.Scope != scope || stored.Desired == nil {
			return LibraryMetadata{}, fmt.Errorf("read library metadata: stored envelope does not match owner")
		}
		if err := stored.Validate(); err != nil {
			return LibraryMetadata{}, fmt.Errorf("read library metadata: %w", err)
		}
		result.Envelope = stored
	}
	return result, nil
}

func (repository *LibraryRepository) UpdateMetadata(ctx context.Context, scope librarymetadata.Scope, releaseID, ownerID uuid.UUID, expected int64, envelope librarymetadata.Envelope) (LibraryMetadata, error) {
	if err := envelope.Validate(); err != nil {
		return LibraryMetadata{}, err
	}
	if envelope.Scope != scope {
		return LibraryMetadata{}, fmt.Errorf("metadata scope mismatch")
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return LibraryMetadata{}, err
	}
	var result LibraryMetadata
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		var current int64
		var raw []byte
		var query string
		var args []any
		switch scope {
		case librarymetadata.ScopeRelease:
			query, args = `SELECT metadata,metadata_revision FROM local_release WHERE id=? FOR UPDATE`, []any{ownerID}
		case librarymetadata.ScopeRecording:
			query, args = `SELECT metadata,metadata_revision FROM local_recording WHERE id=? FOR UPDATE`, []any{ownerID}
		case librarymetadata.ScopeTrack:
			query, args = `SELECT metadata,metadata_revision FROM local_track WHERE id=? FOR UPDATE`, []any{ownerID}
		case librarymetadata.ScopePosition:
			query, args = `SELECT metadata,metadata_revision FROM release_track WHERE id=? AND release_id=? FOR UPDATE`, []any{ownerID, releaseID}
		default:
			return fmt.Errorf("update library metadata: unknown scope %q", scope)
		}
		if err := tx.NewRaw(query, args...).Scan(ctx, &raw, &current); errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryMetadataNotFound
		} else if err != nil {
			return err
		}
		if current != expected {
			return ErrLibraryMetadataConflict
		}
		owner, ok := metadataOwnerForScope(scope)
		if !ok {
			return fmt.Errorf("update library metadata: unknown scope %q", scope)
		}
		set := `metadata=jsonb_set(COALESCE(metadata,'{}'::jsonb),'{local_metadata}',?::jsonb,true),metadata_revision=metadata_revision+1`
		if owner.updatedAt {
			set += `,updated_at=now()`
		}
		args = []any{string(encoded), ownerID}
		if scope == librarymetadata.ScopePosition {
			args = append(args, releaseID)
		}
		if err := tx.NewRaw(`UPDATE `+owner.table+` SET `+set+` WHERE `+owner.where+` RETURNING metadata_revision`, args...).Scan(ctx, &result.Revision); err != nil {
			return err
		}
		result.Envelope = envelope
		return nil
	})
	if err != nil {
		return LibraryMetadata{}, fmt.Errorf("update library metadata: %w", err)
	}
	return result, nil
}
