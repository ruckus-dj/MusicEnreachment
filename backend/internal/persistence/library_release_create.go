package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/uptrace/bun"
)

// LibraryReleaseCreateRequest is the raw, persistence-boundary request for a
// standalone release. Keys express draft identity; names intentionally do not.
type LibraryReleaseCreateRequest struct {
	ReleaseID     *uuid.UUID                      `json:"release_id,omitempty"`
	Title         string                          `json:"title"`
	ReleaseDate   string                          `json:"release_date"`
	Country       string                          `json:"country"`
	Label         string                          `json:"label"`
	CatalogNumber string                          `json:"catalog_number"`
	ReleaseType   string                          `json:"release_type"`
	NewArtists    []LibraryReleaseCreateArtist    `json:"new_artists"`
	NewRecordings []LibraryReleaseCreateRecording `json:"new_recordings"`
	Credits       []LibraryReleaseCreateCredit    `json:"credits"`
	Media         []LibraryReleaseCreateMedium    `json:"media"`
	Cover         *LibraryReleaseCreateCover      `json:"cover,omitempty"`
}

type LibraryReleaseCreateArtist struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}
type LibraryReleaseCreateRecording struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}
type LibraryReleaseCreateArtistRef struct {
	Kind string    `json:"kind"`
	ID   uuid.UUID `json:"id,omitempty"`
	Key  string    `json:"key,omitempty"`
}
type LibraryReleaseCreateCredit struct {
	ArtistRef  LibraryReleaseCreateArtistRef `json:"artist_ref"`
	CreditName string                        `json:"credit_name"`
	Role       string                        `json:"role"`
	JoinPhrase string                        `json:"join_phrase,omitempty"`
}
type LibraryReleaseCreateMedium struct {
	Key       string                         `json:"key"`
	Title     string                         `json:"title"`
	Format    string                         `json:"format"`
	Positions []LibraryReleaseCreatePosition `json:"positions"`
}
type LibraryReleaseCreatePosition struct {
	Key                    string                            `json:"key"`
	DisplayedNumber        string                            `json:"displayed_number"`
	Title                  string                            `json:"title"`
	DurationMS             *int64                            `json:"duration_ms,omitempty"`
	RecordingRef           *LibraryReleaseCreateRecordingRef `json:"recording_ref,omitempty"`
	Sources                []LibraryReleaseCreateSource      `json:"sources,omitempty"`
	SelectedMediaVariantID *uuid.UUID                        `json:"selected_media_variant_id,omitempty"`
}
type LibraryReleaseCreateRecordingRef struct {
	Kind                  string    `json:"kind"`
	Key                   string    `json:"key,omitempty"`
	ID                    uuid.UUID `json:"id,omitempty"`
	ProviderID            uuid.UUID `json:"provider_id,omitempty"`
	ProviderSourceID      uuid.UUID `json:"provider_source_id,omitempty"`
	ProviderRecordingID   uuid.UUID `json:"provider_recording_id,omitempty"`
	ConfigurationIdentity string    `json:"configuration_identity,omitempty"`
}
type LibraryReleaseCreateSource struct {
	MediaVariantID   uuid.UUID  `json:"media_variant_id"`
	MetadataResultID *uuid.UUID `json:"metadata_result_id,omitempty"`
}
type LibraryReleaseCreateCover struct {
	MetadataResultID uuid.UUID `json:"metadata_result_id"`
	ExpectedSHA256   string    `json:"expected_sha256"`
}

type LibraryReleaseCreateResult struct {
	ReleaseID uuid.UUID                            `json:"release_id"`
	Media     []LibraryReleaseCreateMediaResult    `json:"media"`
	Positions []LibraryReleaseCreatePositionResult `json:"positions"`
}
type LibraryReleaseCreateMediaResult struct {
	MediaKey string    `json:"media_key"`
	MediaID  uuid.UUID `json:"media_id"`
}
type LibraryReleaseCreatePositionResult struct {
	PositionKey string     `json:"position_key"`
	PositionID  uuid.UUID  `json:"position_id"`
	TrackID     uuid.UUID  `json:"track_id"`
	RecordingID *uuid.UUID `json:"recording_id,omitempty"`
}

var ErrLibraryReleaseCreateConflict = errors.New("standalone release identity already exists")
var ErrLibraryReleaseCreateProviderConflict = errors.New("provider recording authority changed")

type releaseCreateArtist struct {
	id   uuid.UUID
	name string
}

// CreateStandaloneRelease atomically materializes a manually-authored release
// and its optional credits, media, source associations, and saved cover.
func (repository *LibraryRepository) CreateStandaloneRelease(ctx context.Context, request LibraryReleaseCreateRequest) (LibraryReleaseCreateResult, error) {
	if err := validateLibraryReleaseCreate(request); err != nil {
		return LibraryReleaseCreateResult{}, fmt.Errorf("create standalone release: %w", err)
	}
	id := uuid.New()
	if request.ReleaseID != nil {
		id = *request.ReleaseID
		if id == uuid.Nil {
			return LibraryReleaseCreateResult{}, errors.New("create standalone release: release ID is invalid")
		}
	}
	result := LibraryReleaseCreateResult{ReleaseID: id, Media: []LibraryReleaseCreateMediaResult{}, Positions: []LibraryReleaseCreatePositionResult{}}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("lock output admission gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("lock source fingerprint mutations: %w", err)
		}
		if libraryReleaseCreateUsesProviderRecording(request) {
			if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
				return fmt.Errorf("lock provider configuration: %w", err)
			}
		}
		var exists bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM local_release WHERE id=?)`, id).Scan(ctx, &exists); err != nil {
			return fmt.Errorf("check release identity: %w", err)
		}
		if exists {
			return ErrLibraryReleaseCreateConflict
		}
		if _, err := tx.NewRaw(`INSERT INTO local_release(id,title,metadata) VALUES (?,?, '{}'::jsonb)`, id, request.Title).Exec(ctx); err != nil {
			return fmt.Errorf("create local release: %w", err)
		}
		if err := writeStandaloneReleaseMetadata(ctx, tx, id, request); err != nil {
			return err
		}
		artists := make(map[string]releaseCreateArtist, len(request.NewArtists))
		for _, artist := range request.NewArtists {
			aid := uuid.New()
			if _, err := tx.NewRaw(`INSERT INTO local_artist(id,name,metadata) VALUES (?,?,'{}'::jsonb)`, aid, artist.Name).Exec(ctx); err != nil {
				return fmt.Errorf("create local artist: %w", err)
			}
			artists[artist.Key] = releaseCreateArtist{id: aid, name: artist.Name}
		}
		recordings := make(map[string]uuid.UUID, len(request.NewRecordings))
		for _, recording := range request.NewRecordings {
			rid := uuid.New()
			if _, err := tx.NewRaw(`INSERT INTO local_recording(id,title,metadata) VALUES (?,?,'{}'::jsonb)`, rid, recording.Title).Exec(ctx); err != nil {
				return fmt.Errorf("create local recording: %w", err)
			}
			recordings[recording.Key] = rid
		}
		for position, credit := range request.Credits {
			artist, err := resolveCreateArtist(ctx, tx, artists, credit.ArtistRef)
			if err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO release_artist(release_id,position,artist_id,name,name_join_phrase,role) VALUES (?,?,?,?,?,?)`, id, position, artist.id, credit.CreditName, credit.JoinPhrase, credit.Role).Exec(ctx); err != nil {
				return fmt.Errorf("create release artist credit: %w", err)
			}
		}
		for mediumIndex, medium := range request.Media {
			mid := uuid.New()
			if _, err := tx.NewRaw(`INSERT INTO release_medium(id,release_id,medium,title,format) VALUES (?,?,?,?,?)`, mid, id, mediumIndex, medium.Title, medium.Format).Exec(ctx); err != nil {
				return fmt.Errorf("create release medium: %w", err)
			}
			result.Media = append(result.Media, LibraryReleaseCreateMediaResult{MediaKey: medium.Key, MediaID: mid})
			for positionIndex, pos := range medium.Positions {
				trackID, recordingID := uuid.New(), uuid.Nil
				var recordingPtr *uuid.UUID
				if pos.RecordingRef != nil {
					var err error
					recordingID, err = resolveCreateRecording(ctx, tx, recordings, *pos.RecordingRef)
					if err != nil {
						return err
					}
					recordingPtr = &recordingID
				}
				if _, err := tx.NewRaw(`INSERT INTO local_track(id,title,metadata) VALUES (?,?,'{}'::jsonb)`, trackID, pos.Title).Exec(ctx); err != nil {
					return fmt.Errorf("create local track: %w", err)
				}
				var positionID uuid.UUID
				err := tx.NewRaw(`INSERT INTO release_track(release_id,position,track_id,recording_id,medium,displayed_number,duration_ms) VALUES (?,?,?,?,?,?,?) RETURNING id`, id, positionIndex, trackID, recordingPtr, mediumIndex, pos.DisplayedNumber, pos.DurationMS).Scan(ctx, &positionID)
				if err != nil {
					return fmt.Errorf("create release position: %w", err)
				}
				for _, source := range pos.Sources {
					if err := validateCreateSource(ctx, tx, source); err != nil {
						return err
					}
					if err := rejectCleanupOwnedSourceVariant(ctx, tx, source.MediaVariantID); err != nil {
						return fmt.Errorf("attach standalone release source: %w", err)
					}
					if _, err := tx.NewRaw(`INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,policy_version,metadata,source_metadata_result_id) VALUES (?,?,?,false,true,'manual',NULL,'{}'::jsonb,?)`, uuid.New(), trackID, source.MediaVariantID, source.MetadataResultID).Exec(ctx); err != nil {
						return fmt.Errorf("attach standalone release source: %w", err)
					}
				}
				if pos.SelectedMediaVariantID != nil {
					if _, err := tx.NewRaw(`UPDATE track_source SET selected=true WHERE track_id=? AND media_variant_id=?`, trackID, *pos.SelectedMediaVariantID).Exec(ctx); err != nil {
						return fmt.Errorf("select standalone release source: %w", err)
					}
				}
				result.Positions = append(result.Positions, LibraryReleaseCreatePositionResult{PositionKey: pos.Key, PositionID: positionID, TrackID: trackID, RecordingID: recordingPtr})
			}
		}
		if request.Cover != nil {
			if err := saveStandaloneCover(ctx, tx, id, *request.Cover); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return LibraryReleaseCreateResult{}, fmt.Errorf("create standalone release: %w", err)
	}
	return result, nil
}

func validateLibraryReleaseCreate(r LibraryReleaseCreateRequest) error {
	artists, recordings, media, positions := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, a := range r.NewArtists {
		if a.Key == "" || artists[a.Key] {
			return errors.New("new artist keys must be nonempty and unique")
		}
		artists[a.Key] = true
	}
	for _, v := range r.NewRecordings {
		if v.Key == "" || recordings[v.Key] {
			return errors.New("new recording keys must be nonempty and unique")
		}
		recordings[v.Key] = true
	}
	for _, m := range r.Media {
		if m.Key == "" || media[m.Key] {
			return errors.New("media keys must be nonempty and unique")
		}
		media[m.Key] = true
		for _, p := range m.Positions {
			if p.Key == "" || positions[p.Key] {
				return errors.New("position keys must be nonempty and unique")
			}
			positions[p.Key] = true
			if p.DurationMS != nil && *p.DurationMS < 0 {
				return errors.New("duration must not be negative")
			}
			if p.RecordingRef != nil && p.RecordingRef.Kind == "new" && !recordings[p.RecordingRef.Key] {
				return errors.New("position references an unknown new recording")
			}
			if p.RecordingRef != nil && p.RecordingRef.Kind != "new" && p.RecordingRef.Kind != "existing" && p.RecordingRef.Kind != "provider" {
				return errors.New("position recording reference kind is invalid")
			}
			seen := map[uuid.UUID]bool{}
			for _, s := range p.Sources {
				if s.MediaVariantID == uuid.Nil || seen[s.MediaVariantID] {
					return errors.New("source variants must be nonempty and unique per position")
				}
				seen[s.MediaVariantID] = true
			}
			if p.SelectedMediaVariantID != nil && !seen[*p.SelectedMediaVariantID] {
				return errors.New("selected source must be attached to the position")
			}
		}
	}
	for _, c := range r.Credits {
		switch c.ArtistRef.Kind {
		case "new":
			if !artists[c.ArtistRef.Key] {
				return errors.New("credit references an unknown new artist")
			}
		case "existing":
			if c.ArtistRef.ID == uuid.Nil {
				return errors.New("existing artist ID is required")
			}
		default:
			return errors.New("artist reference kind is invalid")
		}
	}
	if len(artists) > 0 {
		used := map[string]bool{}
		for _, c := range r.Credits {
			if c.ArtistRef.Kind == "new" {
				used[c.ArtistRef.Key] = true
			}
		}
		for key := range artists {
			if !used[key] {
				return errors.New("unreferenced new artist")
			}
		}
	}
	if len(recordings) > 0 {
		used := map[string]bool{}
		for _, m := range r.Media {
			for _, p := range m.Positions {
				if p.RecordingRef != nil && p.RecordingRef.Kind == "new" {
					used[p.RecordingRef.Key] = true
				}
			}
		}
		for key := range recordings {
			if !used[key] {
				return errors.New("unreferenced new recording")
			}
		}
	}
	return nil
}

func resolveCreateArtist(ctx context.Context, tx bun.Tx, created map[string]releaseCreateArtist, ref LibraryReleaseCreateArtistRef) (releaseCreateArtist, error) {
	if ref.Kind == "new" {
		return created[ref.Key], nil
	}
	var a releaseCreateArtist
	err := tx.NewRaw(`SELECT id,name FROM local_artist WHERE id=? FOR KEY SHARE`, ref.ID).Scan(ctx, &a.id, &a.name)
	if errors.Is(err, sql.ErrNoRows) {
		return a, errors.New("existing artist was not found")
	}
	if err != nil {
		return a, fmt.Errorf("read existing artist: %w", err)
	}
	return a, nil
}
func resolveCreateRecording(ctx context.Context, tx bun.Tx, created map[string]uuid.UUID, ref LibraryReleaseCreateRecordingRef) (uuid.UUID, error) {
	switch ref.Kind {
	case "new":
		id, ok := created[ref.Key]
		if !ok {
			return uuid.Nil, errors.New("new recording was not found")
		}
		return id, nil
	case "existing":
		if ref.ID == uuid.Nil {
			return uuid.Nil, errors.New("existing recording ID is required")
		}
		var id uuid.UUID
		err := tx.NewRaw(`SELECT id FROM local_recording WHERE id=? FOR KEY SHARE`, ref.ID).Scan(ctx, &id)
		if errors.Is(err, sql.ErrNoRows) {
			return uuid.Nil, errors.New("existing recording was not found")
		}
		if err != nil {
			return uuid.Nil, fmt.Errorf("read existing recording: %w", err)
		}
		return id, nil
	case "provider":
		return resolveCreateProviderRecording(ctx, tx, ref)
	default:
		return uuid.Nil, errors.New("recording reference kind is invalid")
	}
}

func libraryReleaseCreateUsesProviderRecording(request LibraryReleaseCreateRequest) bool {
	for _, medium := range request.Media {
		for _, position := range medium.Positions {
			if position.RecordingRef != nil && position.RecordingRef.Kind == "provider" {
				return true
			}
		}
	}
	return false
}

func resolveCreateProviderRecording(ctx context.Context, tx bun.Tx, ref LibraryReleaseCreateRecordingRef) (uuid.UUID, error) {
	if ref.ProviderID == uuid.Nil || ref.ProviderSourceID == uuid.Nil || ref.ProviderRecordingID == uuid.Nil || ref.ConfigurationIdentity == "" {
		return uuid.Nil, errors.New("provider recording authority is incomplete")
	}
	var currentIdentity string
	if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &currentIdentity); err != nil {
		return uuid.Nil, fmt.Errorf("read current provider configuration: %w", err)
	}
	if currentIdentity != ref.ConfigurationIdentity {
		return uuid.Nil, ErrLibraryReleaseCreateProviderConflict
	}
	var sourceIdentity string
	err := tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=? AND provider_id=? FOR SHARE`, ref.ProviderSourceID, ref.ProviderID).Scan(ctx, &sourceIdentity)
	if errors.Is(err, sql.ErrNoRows) || err == nil && sourceIdentity != ref.ConfigurationIdentity {
		return uuid.Nil, ErrLibraryReleaseCreateProviderConflict
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("read provider source authority: %w", err)
	}
	var providerKey, title string
	var fields, evidence json.RawMessage
	err = tx.NewRaw(`SELECT r.provider_key,COALESCE(NULLIF(e.name,''),e.fields->>'title',''),e.fields,e.field_evidence FROM provider_recording r JOIN provider_source_entity e ON e.provider_id=r.provider_id AND e.provider_key=r.provider_key AND e.provider_source_id=? AND e.entity_kind='recording' WHERE r.provider_id=? AND r.id=? FOR SHARE OF e`, ref.ProviderSourceID, ref.ProviderID, ref.ProviderRecordingID).Scan(ctx, &providerKey, &title, &fields, &evidence)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return uuid.Nil, errors.New("provider recording is not available in the accepted source")
		}
		return uuid.Nil, fmt.Errorf("read accepted provider recording: %w", err)
	}
	var mapped uuid.UUID
	err = tx.NewRaw(`SELECT local_recording_id FROM provider_local_recording WHERE provider_id=? AND provider_recording_id=? FOR UPDATE`, ref.ProviderID, ref.ProviderRecordingID).Scan(ctx, &mapped)
	if err == nil {
		if err := ensureManualRecordingAuthority(ctx, tx, ref.ProviderID, ref.ProviderRecordingID, materializationAuthority{method: "manual"}); err != nil {
			return uuid.Nil, err
		}
		return mapped, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("read provider recording mapping: %w", err)
	}
	// Configuration and accepted source are fenced before mapping authority is read.
	mapped, created, err := ensureManualLocalRecording(ctx, tx, ref.ProviderID, ref.ProviderRecordingID, title)
	if err != nil {
		return uuid.Nil, err
	}
	if !created {
		return mapped, nil
	}
	var fieldEvidence map[string]ProviderFieldEvidence
	if err := json.Unmarshal(evidence, &fieldEvidence); err != nil {
		return uuid.Nil, fmt.Errorf("decode accepted recording evidence: %w", err)
	}
	projectionRevision := acceptedProjectionRevision(nil, []MatchingProjectionEntity{{Kind: "recording", ProviderKey: providerKey, Fields: fields, FieldEvidence: evidence}}, nil)
	if created && strings.TrimSpace(title) != "" {
		if _, hasTitleEvidence := fieldEvidence["title"]; hasTitleEvidence {
			if err := writeManualMetadata(ctx, tx, "local_recording", mapped, "title", providerKey, ref.ProviderSourceID, ref.ProviderID, projectionRevision, evidence); err != nil {
				return uuid.Nil, fmt.Errorf("write accepted recording title provenance: %w", err)
			}
		}
	}
	credits, err := acceptedRecordingArtistCredits(ctx, tx, ref.ProviderID, ref.ProviderSourceID, ref.ProviderRecordingID)
	if err != nil {
		return uuid.Nil, err
	}
	if len(credits) > 0 {
		if err := materializeArtistCredits(ctx, tx, ref.ProviderID, ref.ProviderSourceID, mapped, "recording_artist", credits, "", projectionRevision, true); err != nil {
			return uuid.Nil, err
		}
	}
	return mapped, nil
}

func validateCreateSource(ctx context.Context, tx bun.Tx, s LibraryReleaseCreateSource) error {
	var exists bool
	if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_variant WHERE id=?)`, s.MediaVariantID).Scan(ctx, &exists); err != nil {
		return fmt.Errorf("validate source variant: %w", err)
	}
	if !exists {
		return errors.New("source variant was not found")
	}
	if s.MetadataResultID != nil {
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_metadata_result r JOIN media_variant v ON v.source_sha256=r.source_sha256 WHERE r.id=? AND v.id=?)`, *s.MetadataResultID, s.MediaVariantID).Scan(ctx, &exists); err != nil {
			return fmt.Errorf("validate source metadata result: %w", err)
		}
		if !exists {
			return errors.New("source metadata result does not belong to source variant")
		}
	}
	return nil
}

func writeStandaloneReleaseMetadata(ctx context.Context, tx bun.Tx, id uuid.UUID, r LibraryReleaseCreateRequest) error {
	values := map[string]any{"release_date": r.ReleaseDate, "release_country": r.Country, "catalog_number": r.CatalogNumber, "release_primary_type": r.ReleaseType}
	if r.Label != "" {
		values["label"] = []string{r.Label}
	}
	desired := map[string]librarymetadata.FieldValue{}
	for name, value := range values {
		if value == "" {
			continue
		}
		if _, ok := librarymetadata.Field(name); !ok {
			return fmt.Errorf("release metadata field %q is not registered", name)
		}
		desired[name] = librarymetadata.ManualFieldValue(librarymetadata.StateValue, value, true)
	}
	if len(desired) == 0 {
		return nil
	}
	envelope := librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease, Desired: desired}
	if err := envelope.Validate(); err != nil {
		return fmt.Errorf("validate release metadata: %w", err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("encode release metadata: %w", err)
	}
	_, err = tx.NewRaw(`UPDATE local_release SET metadata=jsonb_set(COALESCE(metadata,'{}'::jsonb),'{local_metadata}',?::jsonb,true),metadata_revision=metadata_revision+1 WHERE id=?`, string(encoded), id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("save release metadata: %w", err)
	}
	return nil
}

func saveStandaloneCover(ctx context.Context, tx bun.Tx, releaseID uuid.UUID, cover LibraryReleaseCreateCover) error {
	var data []byte
	var digest []byte
	var mime string
	err := tx.NewRaw(`SELECT cover_data,cover_sha256,cover_mime FROM media_metadata_result WHERE id=? AND cover_observed=true AND cover_data IS NOT NULL`, cover.MetadataResultID).Scan(ctx, &data, &digest, &mime)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("saved cover metadata result was not found")
	}
	if err != nil {
		return fmt.Errorf("read saved cover metadata: %w", err)
	}
	sum := sha256.Sum256(data)
	expected := strings.ToLower(strings.TrimSpace(cover.ExpectedSHA256))
	if expected == "" || hex.EncodeToString(sum[:]) != expected || len(digest) > 0 && !releaseCreateEqualBytes(digest, sum[:]) {
		return errors.New("saved cover digest does not match")
	}
	generation := uuid.New()
	if err := validateNewReleaseCoverImage(generation, data, mime); err != nil {
		return fmt.Errorf("saved cover image is invalid: %w", err)
	}
	size := int64(len(data))
	_, err = tx.NewRaw(`INSERT INTO release_publication_cover(release_id,revision,mode,selected_generation,selected_bytes,selected_mime,selected_sha256,selected_size,updated_at) VALUES (?,1,'manual',?,?,?,?,?,now())`, releaseID, generation, data, mime, sum[:], size).Exec(ctx)
	if err != nil {
		return fmt.Errorf("save standalone release cover: %w", err)
	}
	return nil
}

func releaseCreateEqualBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
