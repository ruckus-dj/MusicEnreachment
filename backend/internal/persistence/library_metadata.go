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
	ErrLibraryMetadataNotFound       = errors.New("library metadata owner not found")
	ErrLibraryMetadataConflict       = errors.New("library metadata revision conflict")
	ErrLibraryMetadataArtistNotFound = errors.New("library metadata artist reference not found")
)

type LibraryMetadata struct {
	Envelope librarymetadata.Envelope
	Revision int64
}

type ProviderGenreBaseline struct {
	ProviderCode string
	SourceID     uuid.UUID
	ProviderKey  string
	Authority    int
	FetchOrder   int64
	Genres       []librarymetadata.GenreReference
	Digest       string
}

type LibraryMetadataDisplay struct {
	Metadata              LibraryMetadata
	ProviderGenreBaseline *ProviderGenreBaseline
	ArtistCreditBaseline  []librarymetadata.ArtistCredit
	EffectiveArtistCredit *librarymetadata.ResolvedArtistCredits
}

// GenreCatalogItems returns the persisted MusicBrainz catalog used to validate
// manual genre selections.
func (repository *LibraryRepository) GenreCatalogItems(ctx context.Context) ([]librarymetadata.GenreReference, error) {
	return readGenreCatalogItems(ctx, repository.db)
}

type metadataOwner struct {
	table     string
	updatedAt bool
	where     string
	revision  string
}

func metadataOwnerForScope(scope librarymetadata.Scope) (metadataOwner, bool) {
	switch scope {
	case librarymetadata.ScopeRelease:
		return metadataOwner{table: "local_release", updatedAt: true, where: "id=?", revision: "metadata_revision"}, true
	case librarymetadata.ScopeRecording:
		return metadataOwner{table: "local_recording", updatedAt: true, where: "id=?", revision: "metadata_revision"}, true
	case librarymetadata.ScopeTrack:
		return metadataOwner{table: "local_track", updatedAt: true, where: "id=?", revision: "metadata_revision"}, true
	case librarymetadata.ScopePosition:
		return metadataOwner{table: "release_track", where: "id=? AND release_id=?", revision: "metadata_revision"}, true
	case librarymetadata.ScopeArtist:
		return metadataOwner{table: "local_artist", updatedAt: true, where: "id=?", revision: "metadata_revision"}, true
	default:
		return metadataOwner{}, false
	}
}

func (repository *LibraryRepository) ReadMetadata(ctx context.Context, scope librarymetadata.Scope, releaseID, ownerID uuid.UUID) (LibraryMetadata, error) {
	return readMetadata(ctx, repository.db, scope, releaseID, ownerID)
}

func readMetadata(ctx context.Context, db bun.IDB, scope librarymetadata.Scope, releaseID, ownerID uuid.UUID) (LibraryMetadata, error) {
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
	case librarymetadata.ScopeArtist:
		query, args = `SELECT metadata,metadata_revision FROM local_artist WHERE id=?`, []any{ownerID}
	default:
		return LibraryMetadata{}, fmt.Errorf("read library metadata: unknown scope %q", scope)
	}
	if err := db.NewRaw(query, args...).Scan(ctx, &metadata, &result.Revision); errors.Is(err, sql.ErrNoRows) {
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

// ReadMetadataDisplay captures desired metadata and optional provider genre
// evidence from one repeatable-read snapshot. Desired-only ReadMetadata remains
// the write path's source of truth.
func (repository *LibraryRepository) ReadMetadataDisplay(ctx context.Context, scope librarymetadata.Scope, releaseID, ownerID uuid.UUID) (LibraryMetadataDisplay, error) {
	var result LibraryMetadataDisplay
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		metadata, err := readMetadata(ctx, tx, scope, releaseID, ownerID)
		if err != nil {
			return err
		}
		result.Metadata = metadata
		result.ArtistCreditBaseline, result.EffectiveArtistCredit, err = readArtistCreditDisplay(ctx, tx, scope, releaseID, ownerID, metadata.Envelope)
		if err != nil {
			return err
		}
		baseline, err := readProviderGenreBaseline(ctx, tx, scope, ownerID)
		if err != nil {
			return err
		}
		result.ProviderGenreBaseline = baseline
		return nil
	})
	if err != nil {
		return LibraryMetadataDisplay{}, fmt.Errorf("read library metadata display: %w", err)
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
		case librarymetadata.ScopeArtist:
			query, args = `SELECT metadata,metadata_revision FROM local_artist WHERE id=? FOR UPDATE`, []any{ownerID}
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
		if err := validateMetadataArtistReferences(ctx, tx, envelope); err != nil {
			return err
		}
		owner, ok := metadataOwnerForScope(scope)
		if !ok {
			return fmt.Errorf("update library metadata: unknown scope %q", scope)
		}
		set := `metadata=jsonb_set(COALESCE(metadata,'{}'::jsonb),'{local_metadata}',?::jsonb,true),` + owner.revision + `=` + owner.revision + `+1`
		if owner.updatedAt {
			set += `,updated_at=now()`
		}
		args = []any{string(encoded), ownerID}
		if scope == librarymetadata.ScopePosition {
			args = append(args, releaseID)
		}
		if err := tx.NewRaw(`UPDATE `+owner.table+` SET `+set+` WHERE `+owner.where+` RETURNING `+owner.revision, args...).Scan(ctx, &result.Revision); err != nil {
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

func validateMetadataArtistReferences(ctx context.Context, tx bun.Tx, envelope librarymetadata.Envelope) error {
	ids := make(map[uuid.UUID]struct{})
	for name, field := range envelope.Desired {
		if name != "artist_credit" {
			continue
		}
		if field.State != librarymetadata.StateValue {
			continue
		}
		if field.Value == nil {
			continue
		}
		encoded, err := json.Marshal(field.Value)
		if err != nil {
			return fmt.Errorf("encode metadata artist references: %w", err)
		}
		var credits []librarymetadata.ArtistCredit
		if err := json.Unmarshal(encoded, &credits); err != nil {
			return fmt.Errorf("decode metadata artist references: %w", err)
		}
		for _, credit := range credits {
			id, err := uuid.Parse(credit.ArtistID)
			if err != nil || id == uuid.Nil {
				return ErrLibraryMetadataArtistNotFound
			}
			ids[id] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	requested := make([]uuid.UUID, 0, len(ids))
	for id := range ids {
		requested = append(requested, id)
	}
	var found []uuid.UUID
	if err := tx.NewRaw(`SELECT id FROM local_artist WHERE id IN (?) ORDER BY id`, bun.List(requested)).Scan(ctx, &found); err != nil {
		return fmt.Errorf("validate metadata artist references: %w", err)
	}
	if len(found) != len(requested) {
		return ErrLibraryMetadataArtistNotFound
	}
	return nil
}
