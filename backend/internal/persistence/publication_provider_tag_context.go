package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationProviderTagContext is a bounded factual snapshot of the accepted
// MusicBrainz release/position projection and its local owners. It deliberately
// contains no source inventory or optional-asset lookup results.
type PublicationProviderTagContext struct {
	Source         ProviderSource              `json:"source"`
	Release        MatchingRelease             `json:"release"`
	Position       MatchingReleaseTrack        `json:"position"`
	Recording      *MatchingProjectionEntity   `json:"recording,omitempty"`
	ArtistEntities []MatchingProjectionEntity  `json:"artist_entities"`
	ReleaseOwner   PublicationProviderTagOwner `json:"release_owner"`
	PositionOwner  PublicationProviderTagEdge  `json:"position_owner"`
	TrackOwner     PublicationProviderTagOwner `json:"track_owner"`
	RecordingOwner PublicationProviderTagOwner `json:"recording_owner"`
	RecordingBound bool                        `json:"recording_bound"`
	Digest         string                      `json:"digest"`
}

// PublicationProviderTagEdge captures the local release-track relationship and
// its position-scoped metadata authority.
type PublicationProviderTagEdge struct {
	TrackID         uuid.UUID                   `json:"track_id"`
	RecordingID     *uuid.UUID                  `json:"recording_id,omitempty"`
	Medium          int                         `json:"medium"`
	Position        int                         `json:"position"`
	DisplayedNumber string                      `json:"displayed_number"`
	Owner           PublicationProviderTagOwner `json:"owner"`
}

// PublicationProviderTagOwner preserves local authority state without deciding
// whether a manual field value or override protects a provider update.
type PublicationProviderTagOwner struct {
	ID                    uuid.UUID       `json:"id"`
	Title                 string          `json:"title,omitempty"`
	Metadata              json.RawMessage `json:"metadata"`
	Revision              int64           `json:"revision"`
	ManualOverridePresent bool            `json:"manual_matching_override_present"`
	ManualOverride        json.RawMessage `json:"manual_matching_override,omitempty"`
	BindingMethod         string          `json:"binding_method,omitempty"`
	BindingPolicyVersion  *string         `json:"binding_policy_version,omitempty"`
}

// ReadPublicationProviderTagContext reads only accepted projection fields for
// one already-selected MusicBrainz release/track edge and the corresponding
// local owners. It is a repeatable, read-only snapshot; callers may also reuse
// readPublicationProviderTagContextTx inside an existing transaction.
func (repository *SetupManagerRepository) ReadPublicationProviderTagContext(ctx context.Context, releaseID, positionID uuid.UUID, endpoint, expectedIdentity, releaseKey, trackKey string) (*PublicationProviderTagContext, error) {
	var result *PublicationProviderTagContext
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		return readPublicationProviderTagContextTx(ctx, tx, releaseID, positionID, endpoint, expectedIdentity, releaseKey, trackKey, false, &result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ReadPublicationProviderTagContextForPosition resolves provider keys only
// from the existing MusicBrainz/local bindings for this position. The binding
// lookup and accepted projection read share one repeatable-read transaction.
func (repository *SetupManagerRepository) ReadPublicationProviderTagContextForPosition(ctx context.Context, releaseID, positionID uuid.UUID, endpoint, expectedIdentity string) (*PublicationProviderTagContext, error) {
	if releaseID == uuid.Nil || positionID == uuid.Nil || endpoint == "" || expectedIdentity == "" {
		return nil, fmt.Errorf("read publication provider tag context: required identity is missing")
	}
	var result *PublicationProviderTagContext
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		return readPublicationProviderTagContextForPositionTx(ctx, tx, releaseID, positionID, endpoint, expectedIdentity, false, &result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// readPublicationProviderTagContextForPositionTx derives the provider keys from
// the selected local bindings inside the caller's transaction. With lockRows,
// use READ COMMITTED: the keyed reader takes projection/binding locks and then
// verifies that the selected binding still identifies this exact position.
func readPublicationProviderTagContextForPositionTx(ctx context.Context, tx bun.Tx, releaseID, positionID uuid.UUID, endpoint, expectedIdentity string, lockRows bool, result **PublicationProviderTagContext) error {
	if releaseID == uuid.Nil || positionID == uuid.Nil || endpoint == "" || expectedIdentity == "" {
		return fmt.Errorf("read publication provider tag context: required identity is missing")
	}
	if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
		return fmt.Errorf("read publication provider tag context: acquire output gate: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
		return fmt.Errorf("read publication provider tag context: lock configuration: %w", err)
	}
	var binding struct {
		ReleaseKey string `bun:"release_key"`
		TrackKey   string `bun:"track_key"`
	}
	err := tx.NewRaw(`SELECT release.provider_key AS release_key, track_binding.provider_track_id::text AS track_key
		FROM provider_local_release release_binding
		JOIN provider_local_track track_binding ON track_binding.provider_id=release_binding.provider_id
		JOIN provider ON provider.id=release_binding.provider_id AND provider.code='musicbrainz'
		JOIN provider_release release ON release.id=release_binding.provider_release_id AND release.provider_id=release_binding.provider_id
		JOIN provider_source_release_track track ON track.provider_id=track_binding.provider_id
			AND track.track_id=track_binding.provider_track_id AND track.release_id=release_binding.provider_release_id AND track.present=true
		JOIN release_track local_edge ON local_edge.release_id=release_binding.local_release_id
			AND local_edge.track_id=track_binding.local_track_id AND local_edge.id=?
		WHERE release_binding.local_release_id=?
		ORDER BY release.provider_key,track_binding.provider_track_id`, positionID, releaseID).Scan(ctx, &binding)
	if err == sql.ErrNoRows {
		return fmt.Errorf("read publication provider tag context: selected local position has no accepted MusicBrainz binding: %w", ErrPublicationAdmissionConflict)
	}
	if err != nil {
		return fmt.Errorf("read publication provider tag context: resolve local provider binding: %w", err)
	}
	if binding.ReleaseKey == "" || binding.TrackKey == "" {
		return fmt.Errorf("read publication provider tag context: selected local provider binding is incomplete: %w", ErrPublicationAdmissionConflict)
	}
	return readPublicationProviderTagContextTx(ctx, tx, releaseID, positionID, endpoint, expectedIdentity, binding.ReleaseKey, binding.TrackKey, lockRows, result)
}

// readPublicationProviderTagContextTx is reusable in an existing transaction.
// With lockRows, callers must use READ COMMITTED; it acquires output/config
// gates, then locks projection, binding, and local-owner rows in order. The
// standalone repeatable-read API uses lockRows=false and is a factual snapshot.
func readPublicationProviderTagContextTx(ctx context.Context, tx bun.Tx, releaseID, positionID uuid.UUID, endpoint, expectedIdentity, releaseKey, trackKey string, lockRows bool, result **PublicationProviderTagContext) error {
	if releaseID == uuid.Nil || positionID == uuid.Nil || endpoint == "" || expectedIdentity == "" || releaseKey == "" || trackKey == "" {
		return fmt.Errorf("read publication provider tag context: required identity is missing")
	}
	if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
		return fmt.Errorf("read publication provider tag context: acquire output gate: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
		return fmt.Errorf("read publication provider tag context: lock configuration: %w", err)
	}
	var source ProviderSource
	if err := tx.NewRaw(`SELECT id,provider_id,namespace,endpoint,configuration_identity FROM provider_source WHERE provider_id=(SELECT id FROM provider WHERE code='musicbrainz') AND endpoint=? AND configuration_identity=?`, endpoint, expectedIdentity).Scan(ctx, &source); err != nil {
		return fmt.Errorf("read publication provider tag context: read active source: %w", err)
	}
	// Projection tables precede all local binding/owner locks. Never lock the
	// provider_source row: refresh holds it FOR UPDATE before entity mutations.
	if lockRows {
		if _, err := tx.ExecContext(ctx, `LOCK TABLE provider_source_entity, provider_source_release_track, provider_source_release_artist_credit, provider_release_track, provider_release_artist_credit IN SHARE MODE`); err != nil {
			return fmt.Errorf("read publication provider tag context: lock projection rows: %w", err)
		}
	}
	snapshot, err := readMusicBrainzMatchingSnapshot(ctx, tx, endpoint, expectedIdentity, []string{releaseKey}, nil, false)
	if err != nil {
		return fmt.Errorf("read publication provider tag context: read accepted projection: %w", err)
	}
	if snapshot.Source.ID != source.ID {
		return fmt.Errorf("read publication provider tag context: active source changed")
	}
	var release *MatchingRelease
	for index := range snapshot.Releases {
		if snapshot.Releases[index].ProviderKey == releaseKey {
			if release != nil {
				return fmt.Errorf("read publication provider tag context: selected release is ambiguous")
			}
			release = &snapshot.Releases[index]
		}
	}
	_, hasTrackEvidence := releaseEvidence(release, "__tracks")
	if release == nil || !hasTrackEvidence {
		return fmt.Errorf("read publication provider tag context: accepted release track graph is unavailable")
	}
	var selected *MatchingReleaseTrack
	for index := range release.Tracks {
		track := &release.Tracks[index]
		if track.ID.String() == trackKey {
			if track.ID == uuid.Nil || track.RecordingProviderKey == "" || selected != nil {
				return fmt.Errorf("read publication provider tag context: selected track is ambiguous")
			}
			selected = track
		}
	}
	if selected == nil {
		return fmt.Errorf("read publication provider tag context: selected release-track edge is unavailable")
	}
	// Freeze projection rows before explicit bindings and local owner rows. Never
	// lock provider_source here: refresh locks it FOR UPDATE before entity writes.
	var ctxSnapshot PublicationProviderTagContext
	ctxSnapshot.Source, ctxSnapshot.Release, ctxSnapshot.Position = snapshot.Source, *release, *selected
	ctxSnapshot.Source.CreatedAt, ctxSnapshot.Source.UpdatedAt = time.Time{}, time.Time{}
	for index := range snapshot.ProjectionEntities {
		entity := snapshot.ProjectionEntities[index]
		if entity.Kind == "recording" && entity.ProviderKey == selected.RecordingProviderKey {
			copy := entity
			ctxSnapshot.Recording = &copy
		} else if entity.Kind == "artist" {
			ctxSnapshot.ArtistEntities = append(ctxSnapshot.ArtistEntities, entity)
		}
	}
	if err := readPublicationProviderTagOwners(ctx, tx, release.ID, selected.ID, releaseID, positionID, lockRows, &ctxSnapshot); err != nil {
		return fmt.Errorf("read publication provider tag context: read local owners: %w", err)
	}
	// Preserve the local recording owner facts unconditionally, but do not expose
	// provider recording projection as an eligible fallback without its explicit
	// provider/local recording binding.
	if !ctxSnapshot.RecordingBound {
		ctxSnapshot.Recording = nil
	}
	encoded, err := json.Marshal(ctxSnapshot)
	if err != nil {
		return fmt.Errorf("encode publication provider tag context: %w", err)
	}
	digest := sha256.Sum256(encoded)
	ctxSnapshot.Digest = hex.EncodeToString(digest[:])
	*result = &ctxSnapshot
	return nil
}

func readPublicationProviderTagOwners(ctx context.Context, tx bun.IDB, providerReleaseID, providerTrackID, requestedReleaseID, requestedPositionID uuid.UUID, lockRows bool, result *PublicationProviderTagContext) error {
	if lockRows {
		if _, err := tx.ExecContext(ctx, `LOCK TABLE provider_local_release, provider_local_recording, provider_local_track IN SHARE MODE`); err != nil {
			return fmt.Errorf("lock provider/local bindings: %w", err)
		}
	}
	var releaseOwner struct {
		ID       uuid.UUID       `bun:"id,type:uuid"`
		Title    string          `bun:"title"`
		Metadata json.RawMessage `bun:"metadata,type:jsonb"`
		Revision int64           `bun:"metadata_revision"`
		Method   string          `bun:"method"`
		Policy   *string         `bun:"policy_version"`
	}
	queryRelease := `SELECT r.id,r.title,r.metadata,r.metadata_revision,m.method,m.policy_version FROM provider_local_release m JOIN local_release r ON r.id=m.local_release_id WHERE m.provider_id=? AND m.provider_release_id=? AND r.id=?`
	if lockRows {
		queryRelease += ` FOR SHARE OF m,r`
	}
	if err := tx.NewRaw(queryRelease, result.Source.ProviderID, providerReleaseID, requestedReleaseID).Scan(ctx, &releaseOwner); err != nil {
		return fmt.Errorf("read bound release owner: %w", err)
	}
	result.ReleaseOwner = publicationTagOwner(releaseOwner.ID, releaseOwner.Metadata, releaseOwner.Revision, releaseOwner.Method, releaseOwner.Policy)
	result.ReleaseOwner.Title = releaseOwner.Title
	var edge struct {
		TrackID         uuid.UUID       `bun:"track_id,type:uuid"`
		RecordingID     *uuid.UUID      `bun:"recording_id,type:uuid,nullzero"`
		Medium          int             `bun:"medium"`
		Position        int             `bun:"position"`
		DisplayedNumber string          `bun:"displayed_number"`
		ID              uuid.UUID       `bun:"id,type:uuid"`
		Metadata        json.RawMessage `bun:"metadata,type:jsonb"`
		Revision        int64           `bun:"metadata_revision"`
	}
	queryEdge := `SELECT track.id,track.track_id,track.recording_id,track.medium,track.position,track.displayed_number,track.metadata,track.metadata_revision FROM release_track track WHERE track.release_id=? AND track.id=?`
	if lockRows {
		queryEdge += ` FOR SHARE`
	}
	if err := tx.NewRaw(queryEdge, requestedReleaseID, requestedPositionID).Scan(ctx, &edge); err != nil {
		return fmt.Errorf("read local release-track edge: %w", err)
	}
	result.PositionOwner = PublicationProviderTagEdge{TrackID: edge.TrackID, RecordingID: edge.RecordingID, Medium: edge.Medium, Position: edge.Position, DisplayedNumber: edge.DisplayedNumber, Owner: publicationTagOwner(edge.ID, edge.Metadata, edge.Revision, "", nil)}
	var trackOwner struct {
		ID       uuid.UUID       `bun:"id,type:uuid"`
		Title    string          `bun:"title"`
		Metadata json.RawMessage `bun:"metadata,type:jsonb"`
		Revision int64           `bun:"metadata_revision"`
		Method   string          `bun:"method"`
		Policy   *string         `bun:"policy_version"`
	}
	queryTrack := `SELECT local_track.id,local_track.title,local_track.metadata,local_track.metadata_revision,m.method,m.policy_version FROM provider_local_track m JOIN local_track ON local_track.id=m.local_track_id WHERE m.provider_id=? AND m.provider_track_id=? AND m.local_track_id=?`
	if lockRows {
		queryTrack += ` FOR SHARE OF m,local_track`
	}
	if err := tx.NewRaw(queryTrack, result.Source.ProviderID, providerTrackID, edge.TrackID).Scan(ctx, &trackOwner); err != nil {
		return fmt.Errorf("read bound local track owner: %w", err)
	}
	result.TrackOwner = publicationTagOwner(trackOwner.ID, trackOwner.Metadata, trackOwner.Revision, trackOwner.Method, trackOwner.Policy)
	result.TrackOwner.Title = trackOwner.Title
	if edge.RecordingID == nil {
		return nil
	}
	var recordingOwner struct {
		ID       uuid.UUID       `bun:"id,type:uuid"`
		Title    string          `bun:"title"`
		Metadata json.RawMessage `bun:"metadata,type:jsonb"`
		Revision int64           `bun:"metadata_revision"`
	}
	queryRecording := `SELECT recording.id,recording.title,recording.metadata,recording.metadata_revision FROM local_recording recording WHERE recording.id=?`
	if lockRows {
		queryRecording += ` FOR SHARE`
	}
	if err := tx.NewRaw(queryRecording, *edge.RecordingID).Scan(ctx, &recordingOwner); err != nil {
		return fmt.Errorf("read bound recording owner: %w", err)
	}
	result.RecordingOwner = publicationTagOwner(recordingOwner.ID, recordingOwner.Metadata, recordingOwner.Revision, "", nil)
	result.RecordingOwner.Title = recordingOwner.Title
	var binding struct {
		Method string  `bun:"method"`
		Policy *string `bun:"policy_version"`
	}
	queryBinding := `SELECT m.method,m.policy_version FROM provider_local_recording m JOIN provider_recording pr ON pr.id=m.provider_recording_id AND pr.provider_id=m.provider_id WHERE m.provider_id=? AND pr.provider_key=? AND m.local_recording_id=?`
	if lockRows {
		queryBinding += ` FOR SHARE OF m`
	}
	if err := tx.NewRaw(queryBinding, result.Source.ProviderID, result.Position.RecordingProviderKey, *edge.RecordingID).Scan(ctx, &binding); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("read provider/local recording binding: %w", err)
	}
	result.RecordingBound = true
	result.RecordingOwner.BindingMethod = binding.Method
	result.RecordingOwner.BindingPolicyVersion = binding.Policy
	return nil
}

func releaseEvidence(release *MatchingRelease, field string) (ProviderFieldEvidence, bool) {
	if release == nil {
		return ProviderFieldEvidence{}, false
	}
	evidence, ok := release.FieldEvidence[field]
	return evidence, ok
}

func publicationTagOwner(id uuid.UUID, metadata json.RawMessage, revision int64, method string, policy *string) PublicationProviderTagOwner {
	owner := PublicationProviderTagOwner{ID: id, Metadata: append(json.RawMessage(nil), metadata...), Revision: revision, BindingMethod: method, BindingPolicyVersion: policy}
	var root map[string]json.RawMessage
	if json.Unmarshal(metadata, &root) == nil {
		value, present := root["manual_matching_override"]
		owner.ManualOverridePresent = present
		if present {
			owner.ManualOverride = append(json.RawMessage(nil), value...)
		}
	}
	return owner
}
