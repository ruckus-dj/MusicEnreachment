package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

const LibraryDeletionOperationKind = "delete_library_v1"

var ErrLibraryDeletionConflict = errors.New("library deletion scope changed")
var ErrLibraryDeletionBlocked = errors.New("library deletion has retained publication authority")
var libraryDeletionOperationNamespace = uuid.MustParse("4c8d707f-1aa0-4b68-b6d8-1a9c9af23137")

type LibraryDeletionScope struct {
	Kind                    string      `json:"kind"`
	ReleaseID               uuid.UUID   `json:"release_id"`
	PublicationIDs          []uuid.UUID `json:"publication_ids"`
	ManualReceiptRequestIDs []uuid.UUID `json:"manual_receipt_request_ids"`
	DependencyDigest        string      `json:"dependency_digest"`
}

type LibraryDeletionPreview struct {
	Scope          LibraryDeletionScope
	ScopeDigest    string
	Title          string
	PositionIDs    []uuid.UUID
	RecordingCount int         `json:"recording_count"`
	RecordingIDs   []uuid.UUID `json:"recording_ids"`
	Publications   []TrackPublication
	Blockers       []string
	orphanTrackIDs []uuid.UUID
	dependencies   libraryDeletionDependencies
}

type LibraryDeletionOperationSnapshot struct {
	SchemaVersion          int                         `json:"schema_version"`
	Scope                  LibraryDeletionScope        `json:"scope"`
	ScopeDigest            string                      `json:"scope_digest"`
	ConfirmationDigest     string                      `json:"confirmation_digest"`
	Dependencies           libraryDeletionDependencies `json:"dependencies"`
	OwnershipWitnesses     []json.RawMessage           `json:"ownership_witnesses"`
	ResidualPublicationIDs []uuid.UUID                 `json:"residual_publication_ids"`
}

// LibraryDeletionOperationID derives one stable operation identity from an
// explicit, nonce-bearing preview confirmation digest.
func LibraryDeletionOperationID(confirmationDigest string) (uuid.UUID, error) {
	if !validSHA256(confirmationDigest) {
		return uuid.Nil, errors.New("library deletion confirmation digest is invalid")
	}
	return uuid.NewSHA1(libraryDeletionOperationNamespace, []byte(confirmationDigest)), nil
}

type libraryDeletionDependencies struct {
	PositionIDs             []uuid.UUID       `json:"position_ids"`
	PositionTrackLinks      []json.RawMessage `json:"position_track_links"`
	SurvivingTrackLinks     []json.RawMessage `json:"surviving_track_links"`
	SurvivingRecordingLinks []json.RawMessage `json:"surviving_recording_links"`
	CandidateTrackIDs       []uuid.UUID       `json:"candidate_track_ids"`
	OrphanTrackIDs          []uuid.UUID       `json:"orphan_track_ids"`
	CandidateRecordingIDs   []uuid.UUID       `json:"candidate_recording_ids"`
	OrphanRecordingIDs      []uuid.UUID       `json:"orphan_recording_ids"`
	TrackMappings           []json.RawMessage `json:"track_mappings"`
	TrackCredits            []json.RawMessage `json:"track_credits"`
	TrackSources            []json.RawMessage `json:"track_sources"`
	RecordingMappings       []json.RawMessage `json:"recording_mappings"`
	RecordingCredits        []json.RawMessage `json:"recording_credits"`
	MediumIDs               []uuid.UUID       `json:"medium_ids"`
	ReleaseCredits          []json.RawMessage `json:"release_credits"`
	CollectionLinks         []json.RawMessage `json:"collection_links"`
	ProviderLinks           []json.RawMessage `json:"provider_links"`
	CoverRevision           *int64            `json:"cover_revision,omitempty"`
}

type libraryDeletionPosition struct {
	ID uuid.UUID `bun:"id"`
}

type libraryDeletionMedium struct {
	ID uuid.UUID `bun:"id"`
}

type libraryDeletionCredit struct {
	Position   int       `bun:"position"`
	ArtistID   uuid.UUID `bun:"artist_id"`
	Name       string    `bun:"name"`
	JoinPhrase string    `bun:"join_phrase"`
	Role       string    `bun:"role"`
}

type libraryDeletionCollectionLink struct {
	CollectionID uuid.UUID `bun:"collection_id"`
	Position     int       `bun:"position"`
}

type libraryDeletionProviderLink struct {
	ProviderID        uuid.UUID `bun:"provider_id"`
	ProviderReleaseID uuid.UUID `bun:"provider_release_id"`
}

type libraryDeletionPositionTrackLink struct {
	PositionID  uuid.UUID  `bun:"position_id" json:"position_id"`
	ReleaseID   uuid.UUID  `bun:"release_id" json:"release_id"`
	TrackID     uuid.UUID  `bun:"track_id" json:"track_id"`
	RecordingID *uuid.UUID `bun:"recording_id" json:"recording_id"`
}

type libraryDeletionTrackMapping struct {
	ProviderID      uuid.UUID `bun:"provider_id" json:"provider_id"`
	ProviderTrackID uuid.UUID `bun:"provider_track_id" json:"provider_track_id"`
	LocalTrackID    uuid.UUID `bun:"local_track_id" json:"local_track_id"`
}

type libraryDeletionRecordingMapping struct {
	ProviderID          uuid.UUID `bun:"provider_id" json:"provider_id"`
	ProviderRecordingID uuid.UUID `bun:"provider_recording_id" json:"provider_recording_id"`
	LocalRecordingID    uuid.UUID `bun:"local_recording_id" json:"local_recording_id"`
}

type libraryDeletionTrackSource struct {
	ID             uuid.UUID `bun:"id" json:"id"`
	TrackID        uuid.UUID `bun:"track_id" json:"track_id"`
	MediaVariantID uuid.UUID `bun:"media_variant_id" json:"media_variant_id"`
}

type libraryDeletionTrackCredit struct {
	TrackID    uuid.UUID `bun:"track_id" json:"track_id"`
	Position   int       `bun:"position" json:"position"`
	ArtistID   uuid.UUID `bun:"artist_id" json:"artist_id"`
	Name       string    `bun:"name" json:"name"`
	JoinPhrase string    `bun:"join_phrase" json:"join_phrase"`
}

type libraryDeletionRecordingCredit struct {
	RecordingID uuid.UUID `bun:"recording_id" json:"recording_id"`
	Position    int       `bun:"position" json:"position"`
	ArtistID    uuid.UUID `bun:"artist_id" json:"artist_id"`
	Name        string    `bun:"name" json:"name"`
	JoinPhrase  string    `bun:"join_phrase" json:"join_phrase"`
}

// LibraryDeletionDeliveryGuard fences one River delivery of a deletion operation.
type LibraryDeletionDeliveryGuard struct {
	OperationID        uuid.UUID
	ApplicationAttempt int
	RiverJobID         int64
	RiverAttempt       int
	ClaimUUID          uuid.UUID
	BootUUID           uuid.UUID
	DeliveryToken      string
}

type LibraryDeletionDeliveryClaimOutcome string

const (
	LibraryDeletionDeliveryAcquired         LibraryDeletionDeliveryClaimOutcome = "acquired"
	LibraryDeletionDeliveryInterrupted      LibraryDeletionDeliveryClaimOutcome = "interrupted"
	LibraryDeletionDeliveryAlreadyOwned     LibraryDeletionDeliveryClaimOutcome = "already_owned"
	LibraryDeletionDeliveryBlocked          LibraryDeletionDeliveryClaimOutcome = "blocked"
	LibraryDeletionDeliveryRecoveryRequired LibraryDeletionDeliveryClaimOutcome = "recovery_required"
	LibraryDeletionDeliveryAlreadyTerminal  LibraryDeletionDeliveryClaimOutcome = "already_terminal"
	LibraryDeletionDeliveryStale            LibraryDeletionDeliveryClaimOutcome = "stale"
)

// LibraryDeletionPublicationWitness is caller-captured filesystem authority
// for exactly one current publication; its JSON is stored without modification.
type LibraryDeletionPublicationWitness struct {
	PositionID   uuid.UUID       `json:"position_id"`
	Epoch        uuid.UUID       `json:"epoch"`
	Generation   uuid.UUID       `json:"generation"`
	RelativePath string          `json:"relative_path"`
	Ownership    json.RawMessage `json:"ownership"`
}

type libraryDeletionReceipt struct {
	RequestID      uuid.UUID  `bun:"request_id"`
	OriginalID     uuid.UUID  `bun:"original_local_release_id"`
	LocalReleaseID *uuid.UUID `bun:"local_release_id,type:uuid,nullzero"`
	RetiredBy      *uuid.UUID `bun:"retired_by_operation_id,type:uuid,nullzero"`
}

// LibraryDeletionOperationArgs is the durable worker payload. It carries no
// mutable deletion scope; workers reload the frozen operation snapshot.
type LibraryDeletionOperationArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (LibraryDeletionOperationArgs) Kind() string { return LibraryDeletionOperationKind }

type libraryDeletionRiverArgs = LibraryDeletionOperationArgs

// PreviewLibraryDeletion resolves a stable release/publication scope and hashes
// the exact dependency identities which final settlement is permitted to remove.
func (repository *SetupManagerRepository) PreviewLibraryDeletion(ctx context.Context, kind string, releaseID, positionID uuid.UUID) (LibraryDeletionPreview, error) {
	if kind != "publication" && kind != "release" || releaseID == uuid.Nil || (kind == "publication" && positionID == uuid.Nil) || (kind == "release" && positionID != uuid.Nil) {
		return LibraryDeletionPreview{}, errors.New("library deletion selector is invalid")
	}
	var preview LibraryDeletionPreview
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var err error
		preview, err = readLibraryDeletionPreview(ctx, tx, kind, releaseID, positionID)
		if err == nil && kind == "publication" && len(preview.Publications) != 1 {
			return ErrLibraryDeletionConflict
		}
		return err
	})
	return preview, err
}

// CaptureLibraryDeletionPreview keeps the output-configuration gate held while
// the caller captures filesystem ownership for the exact CURRENT rows in the
// preview. The callback must not retain filesystem handles after returning.
func (repository *SetupManagerRepository) CaptureLibraryDeletionPreview(
	ctx context.Context,
	kind string,
	releaseID, positionID uuid.UUID,
	capture func(context.Context, string, LibraryDeletionPreview, map[uuid.UUID]uuid.UUID) ([]json.RawMessage, error),
) (LibraryDeletionPreview, []json.RawMessage, error) {
	if capture == nil {
		return LibraryDeletionPreview{}, nil, errors.New("capture library deletion preview: callback is required")
	}
	if kind != "publication" && kind != "release" || releaseID == uuid.Nil ||
		(kind == "publication" && positionID == uuid.Nil) || (kind == "release" && positionID != uuid.Nil) {
		return LibraryDeletionPreview{}, nil, errors.New("capture library deletion preview: selector is invalid")
	}
	var preview LibraryDeletionPreview
	var witnesses []json.RawMessage
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("capture library deletion preview: lock output configuration: %w", err)
		}
		var err error
		preview, err = readLibraryDeletionPreview(ctx, tx, kind, releaseID, positionID)
		if err != nil {
			return err
		}
		if kind == "publication" && len(preview.Publications) != 1 {
			return ErrLibraryDeletionConflict
		}
		var outputDirectory string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='output_directory'`).Scan(ctx, &outputDirectory); err != nil {
			return fmt.Errorf("capture library deletion preview: read output directory: %w", err)
		}
		claimIDs := make(map[uuid.UUID]uuid.UUID, len(preview.Publications))
		for _, current := range preview.Publications {
			var claimID uuid.UUID
			if err := tx.NewRaw(`SELECT c.id FROM publication_namespace_claim c
				JOIN publication_namespace_reservation r ON r.namespace_claim_id=c.id
				WHERE c.epoch=? AND c.relative_path=? AND c.node_kind='file' AND c.owner_position_id=?
				AND r.position_id=? AND r.generation=? ORDER BY c.id`,
				current.Epoch, current.RelativePath, current.PositionID, current.PositionID, current.Generation).Scan(ctx, &claimID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					preview.Blockers = append(preview.Blockers, current.PositionID.String())
					continue
				}
				return fmt.Errorf("capture library deletion preview: read file namespace claim: %w", err)
			}
			claimIDs[current.PositionID] = claimID
		}
		witnesses, err = capture(ctx, outputDirectory, preview, claimIDs)
		return err
	})
	return preview, witnesses, err
}

// FindLibraryDeletionConfirmation resolves a prior confirmation result under
// the exclusive source-fingerprint mutation gate, without reading the release
// or acquiring the output-configuration gate.
func (repository *SetupManagerRepository) FindLibraryDeletionConfirmation(
	ctx context.Context,
	operationID uuid.UUID,
	confirmationDigest string,
) (*Operation, error) {
	expectedID, err := LibraryDeletionOperationID(confirmationDigest)
	if err != nil || operationID == uuid.Nil || operationID != expectedID {
		return nil, errors.New("find library deletion confirmation: identity is invalid")
	}
	var existing *Operation
	err = repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("find library deletion confirmation: lock source mutation gate: %w", err)
		}
		operation, err := findLibraryDeletionConfirmationTx(ctx, tx, operationID, confirmationDigest)
		if err != nil {
			return err
		}
		existing = operation
		return nil
	})
	if err != nil {
		return nil, err
	}
	return existing, nil
}

func findLibraryDeletionConfirmationTx(ctx context.Context, tx bun.Tx, operationID uuid.UUID, confirmationDigest string) (*Operation, error) {
	operation := new(Operation)
	err := tx.NewSelect().Model(operation).Where("id=?", operationID).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock confirmed library deletion operation: %w", err)
	}
	var snapshot LibraryDeletionOperationSnapshot
	if operation.Kind != LibraryDeletionOperationKind || decodeStrictJSON(operation.InputSnapshot, &snapshot) != nil ||
		validateLibraryDeletionSnapshot(snapshot) != nil || snapshot.ConfirmationDigest != confirmationDigest {
		return nil, ErrLibraryDeletionConflict
	}
	return operation, nil
}

func readLibraryDeletionPreview(ctx context.Context, tx bun.IDB, kind string, releaseID, positionID uuid.UUID) (LibraryDeletionPreview, error) {
	var preview LibraryDeletionPreview
	var releaseExists bool
	if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM local_release WHERE id=?)`, releaseID).Scan(ctx, &releaseExists); err != nil {
		return preview, fmt.Errorf("preview library deletion: read release: %w", err)
	}
	if !releaseExists {
		return preview, ErrLibraryReleaseNotFound
	}
	var title string
	if err := tx.NewRaw(`SELECT title FROM local_release WHERE id=?`, releaseID).Scan(ctx, &title); err != nil {
		return preview, fmt.Errorf("preview library deletion: read release title: %w", err)
	}
	var currentEpoch uuid.UUID
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &currentEpoch); err != nil {
		return preview, fmt.Errorf("preview library deletion: read publication epoch: %w", err)
	}
	positions := make([]libraryDeletionPosition, 0)
	if kind == "publication" {
		if err := tx.NewRaw(`SELECT id FROM release_track WHERE id=? AND release_id=?`, positionID, releaseID).Scan(ctx, &positions); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return preview, ErrLibraryReleaseNotFound
			}
			return preview, fmt.Errorf("preview library deletion: read position: %w", err)
		}
		if len(positions) != 1 {
			return preview, ErrLibraryReleaseNotFound
		}
	} else if err := tx.NewRaw(`SELECT id FROM release_track WHERE release_id=? ORDER BY id`, releaseID).Scan(ctx, &positions); err != nil {
		return preview, fmt.Errorf("preview library deletion: read release positions: %w", err)
	}
	positionIDs := make([]uuid.UUID, 0, len(positions))
	for _, position := range positions {
		positionIDs = append(positionIDs, position.ID)
	}
	publications := make([]TrackPublication, 0)
	if len(positionIDs) > 0 {
		if err := tx.NewSelect().Model((*TrackPublication)(nil)).Where("position_id IN (?)", bun.List(positionIDs)).Order("position_id").Scan(ctx, &publications); err != nil {
			return preview, fmt.Errorf("preview library deletion: read current publications: %w", err)
		}
	}
	publicationByPosition := make(map[uuid.UUID]TrackPublication, len(publications))
	blockers := make([]string, 0)
	for _, publication := range publications {
		publicationByPosition[publication.PositionID] = publication
	}
	for _, id := range positionIDs {
		publication := publicationByPosition[id]
		generation := publication.Generation
		if publication.PositionID != uuid.Nil && publication.Epoch != currentEpoch {
			blockers = append(blockers, id.String())
		}
		blocked, err := libraryDeletionPositionBlocked(ctx, tx, id, currentEpoch, generation)
		if err != nil {
			return preview, err
		}
		if blocked {
			blockers = append(blockers, id.String())
		}
	}
	dependency := libraryDeletionDependencies{
		PositionIDs: positionIDs, PositionTrackLinks: []json.RawMessage{}, SurvivingTrackLinks: []json.RawMessage{},
		SurvivingRecordingLinks: []json.RawMessage{}, OrphanTrackIDs: []uuid.UUID{}, OrphanRecordingIDs: []uuid.UUID{},
		CandidateTrackIDs: []uuid.UUID{}, CandidateRecordingIDs: []uuid.UUID{},
		TrackMappings: []json.RawMessage{}, TrackCredits: []json.RawMessage{}, TrackSources: []json.RawMessage{},
		RecordingMappings: []json.RawMessage{}, RecordingCredits: []json.RawMessage{}, MediumIDs: []uuid.UUID{},
		ReleaseCredits: []json.RawMessage{}, CollectionLinks: []json.RawMessage{}, ProviderLinks: []json.RawMessage{},
	}
	if kind == "release" {
		links := make([]libraryDeletionPositionTrackLink, 0)
		if err := tx.NewRaw(`SELECT id AS position_id,release_id,track_id,recording_id FROM release_track WHERE release_id=? ORDER BY id`, releaseID).Scan(ctx, &links); err != nil {
			return preview, fmt.Errorf("preview library deletion: read position track links: %w", err)
		}
		trackSet := make(map[uuid.UUID]struct{}, len(links))
		recordingSet := make(map[uuid.UUID]struct{}, len(links))
		for _, link := range links {
			raw, err := json.Marshal(link)
			if err != nil {
				return preview, fmt.Errorf("preview library deletion: encode position track link: %w", err)
			}
			dependency.PositionTrackLinks = append(dependency.PositionTrackLinks, raw)
			trackSet[link.TrackID] = struct{}{}
			if link.RecordingID != nil {
				recordingSet[*link.RecordingID] = struct{}{}
			}
		}
		trackIDs := sortedUUIDSet(trackSet)
		recordingIDs := sortedUUIDSet(recordingSet)
		dependency.CandidateTrackIDs = trackIDs
		dependency.CandidateRecordingIDs = recordingIDs
		trackSurvivors := make([]libraryDeletionPositionTrackLink, 0)
		if len(trackIDs) > 0 {
			if err := tx.NewRaw(`SELECT id AS position_id,release_id,track_id,recording_id FROM release_track WHERE track_id IN (?) AND release_id<>? ORDER BY id`, bun.List(trackIDs), releaseID).Scan(ctx, &trackSurvivors); err != nil {
				return preview, fmt.Errorf("preview library deletion: read surviving track links: %w", err)
			}
		}
		for _, link := range trackSurvivors {
			raw, err := json.Marshal(link)
			if err != nil {
				return preview, fmt.Errorf("preview library deletion: encode surviving track link: %w", err)
			}
			dependency.SurvivingTrackLinks = append(dependency.SurvivingTrackLinks, raw)
		}
		recordingSurvivors := make([]libraryDeletionPositionTrackLink, 0)
		if len(recordingIDs) > 0 {
			if err := tx.NewRaw(`SELECT id AS position_id,release_id,track_id,recording_id FROM release_track WHERE recording_id IN (?) AND release_id<>? ORDER BY id`, bun.List(recordingIDs), releaseID).Scan(ctx, &recordingSurvivors); err != nil {
				return preview, fmt.Errorf("preview library deletion: read surviving recording links: %w", err)
			}
		}
		for _, link := range recordingSurvivors {
			raw, err := json.Marshal(link)
			if err != nil {
				return preview, fmt.Errorf("preview library deletion: encode surviving recording link: %w", err)
			}
			dependency.SurvivingRecordingLinks = append(dependency.SurvivingRecordingLinks, raw)
		}
		survivingTracks := make(map[uuid.UUID]struct{}, len(trackSurvivors))
		for _, link := range trackSurvivors {
			survivingTracks[link.TrackID] = struct{}{}
		}
		for _, id := range trackIDs {
			if _, survives := survivingTracks[id]; !survives {
				dependency.OrphanTrackIDs = append(dependency.OrphanTrackIDs, id)
			}
		}
		survivingRecordings := make(map[uuid.UUID]struct{}, len(recordingSurvivors))
		for _, link := range recordingSurvivors {
			survivingRecordings[*link.RecordingID] = struct{}{}
		}
		for _, id := range recordingIDs {
			if _, survives := survivingRecordings[id]; !survives {
				dependency.OrphanRecordingIDs = append(dependency.OrphanRecordingIDs, id)
			}
		}
		preview.RecordingIDs = slices.Clone(dependency.OrphanRecordingIDs)
		preview.RecordingCount = len(preview.RecordingIDs)
		preview.orphanTrackIDs = slices.Clone(dependency.OrphanTrackIDs)
		if err := readLibraryDeletionOrphanIdentities(ctx, tx, &dependency); err != nil {
			return preview, err
		}
		media := make([]libraryDeletionMedium, 0)
		if err := tx.NewRaw(`SELECT id FROM release_medium WHERE release_id=? ORDER BY id`, releaseID).Scan(ctx, &media); err != nil {
			return preview, fmt.Errorf("preview library deletion: read release media: %w", err)
		}
		for _, item := range media {
			dependency.MediumIDs = append(dependency.MediumIDs, item.ID)
		}
		credits := make([]libraryDeletionCredit, 0)
		if err := tx.NewRaw(`SELECT position,artist_id,name,name_join_phrase AS join_phrase,role FROM release_artist WHERE release_id=? ORDER BY position`, releaseID).Scan(ctx, &credits); err != nil {
			return preview, fmt.Errorf("preview library deletion: read release credits: %w", err)
		}
		for _, item := range credits {
			raw, err := json.Marshal(item)
			if err != nil {
				return preview, fmt.Errorf("preview library deletion: encode release credit: %w", err)
			}
			dependency.ReleaseCredits = append(dependency.ReleaseCredits, raw)
		}
		collections := make([]libraryDeletionCollectionLink, 0)
		if err := tx.NewRaw(`SELECT collection_id,position FROM collection_release WHERE release_id=? ORDER BY collection_id,position`, releaseID).Scan(ctx, &collections); err != nil {
			return preview, fmt.Errorf("preview library deletion: read collection links: %w", err)
		}
		for _, item := range collections {
			raw, err := json.Marshal(item)
			if err != nil {
				return preview, fmt.Errorf("preview library deletion: encode collection link: %w", err)
			}
			dependency.CollectionLinks = append(dependency.CollectionLinks, raw)
		}
		providerLinks := make([]libraryDeletionProviderLink, 0)
		if err := tx.NewRaw(`SELECT provider_id,provider_release_id FROM provider_local_release WHERE local_release_id=? ORDER BY provider_id,provider_release_id`, releaseID).Scan(ctx, &providerLinks); err != nil {
			return preview, fmt.Errorf("preview library deletion: read provider release links: %w", err)
		}
		for _, item := range providerLinks {
			raw, err := json.Marshal(item)
			if err != nil {
				return preview, fmt.Errorf("preview library deletion: encode provider link: %w", err)
			}
			dependency.ProviderLinks = append(dependency.ProviderLinks, raw)
		}
		var coverRevision int64
		err := tx.NewRaw(`SELECT revision FROM release_publication_cover WHERE release_id=?`, releaseID).Scan(ctx, &coverRevision)
		if err == nil {
			dependency.CoverRevision = &coverRevision
		} else if !errors.Is(err, sql.ErrNoRows) {
			return preview, fmt.Errorf("preview library deletion: read release cover: %w", err)
		}
		var retiredReceipt bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM manual_matching_receipt WHERE original_local_release_id=? AND (local_release_id IS NULL OR retired_by_operation_id IS NOT NULL))`, releaseID).Scan(ctx, &retiredReceipt); err != nil {
			return preview, fmt.Errorf("preview library deletion: inspect retired manual receipts: %w", err)
		}
		if retiredReceipt {
			blockers = append(blockers, releaseID.String())
		}
	}
	receiptIDs := make([]uuid.UUID, 0)
	if kind == "release" {
		if err := tx.NewRaw(`SELECT request_id FROM manual_matching_receipt WHERE original_local_release_id=? ORDER BY request_id`, releaseID).Scan(ctx, &receiptIDs); err != nil {
			return preview, fmt.Errorf("preview library deletion: read manual matching receipts: %w", err)
		}
	}
	dependencyDigest, err := libraryDeletionDependencyDigest(dependency)
	if err != nil {
		return preview, fmt.Errorf("preview library deletion: encode dependency witness: %w", err)
	}
	scope := LibraryDeletionScope{
		Kind: kind, ReleaseID: releaseID, PublicationIDs: slices.Clone(positionIDs),
		ManualReceiptRequestIDs: receiptIDs, DependencyDigest: dependencyDigest,
	}
	scopeJSON, err := json.Marshal(scope)
	if err != nil {
		return preview, fmt.Errorf("preview library deletion: encode scope: %w", err)
	}
	scopeHash := sha256.Sum256(scopeJSON)
	return LibraryDeletionPreview{
		Scope: scope, ScopeDigest: hex.EncodeToString(scopeHash[:]), Title: title,
		PositionIDs: positionIDs, Publications: publications, Blockers: blockers,
		RecordingIDs: slices.Clone(dependency.OrphanRecordingIDs), RecordingCount: len(dependency.OrphanRecordingIDs),
		orphanTrackIDs: slices.Clone(dependency.OrphanTrackIDs), dependencies: dependency,
	}, nil
}

func libraryDeletionDependencyDigest(dependency libraryDeletionDependencies) (string, error) {
	raw, err := json.Marshal(dependency)
	if err != nil {
		return "", err
	}
	raw, err = canonicalMatchingJSON(raw)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func sortedUUIDSet(values map[uuid.UUID]struct{}) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	slices.SortFunc(result, func(left, right uuid.UUID) int { return strings.Compare(left.String(), right.String()) })
	return result
}

func appendDeletionIdentities[T any](destination *[]json.RawMessage, values []T) error {
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		*destination = append(*destination, raw)
	}
	return nil
}

func readLibraryDeletionOrphanIdentities(ctx context.Context, tx bun.IDB, dependency *libraryDeletionDependencies) error {
	if len(dependency.OrphanTrackIDs) > 0 {
		mappings := make([]libraryDeletionTrackMapping, 0)
		if err := tx.NewRaw(`SELECT provider_id,provider_track_id,local_track_id FROM provider_local_track WHERE local_track_id IN (?) ORDER BY provider_id,provider_track_id`, bun.List(dependency.OrphanTrackIDs)).Scan(ctx, &mappings); err != nil {
			return fmt.Errorf("preview library deletion: read orphan track mappings: %w", err)
		}
		if err := appendDeletionIdentities(&dependency.TrackMappings, mappings); err != nil {
			return fmt.Errorf("preview library deletion: encode orphan track mapping: %w", err)
		}
		credits := make([]libraryDeletionTrackCredit, 0)
		if err := tx.NewRaw(`SELECT track_id,position,artist_id,name,name_join_phrase AS join_phrase FROM track_artist WHERE track_id IN (?) ORDER BY track_id,position`, bun.List(dependency.OrphanTrackIDs)).Scan(ctx, &credits); err != nil {
			return fmt.Errorf("preview library deletion: read orphan track credits: %w", err)
		}
		if err := appendDeletionIdentities(&dependency.TrackCredits, credits); err != nil {
			return fmt.Errorf("preview library deletion: encode orphan track credit: %w", err)
		}
		sources := make([]libraryDeletionTrackSource, 0)
		if err := tx.NewRaw(`SELECT id,track_id,media_variant_id FROM track_source WHERE track_id IN (?) ORDER BY id`, bun.List(dependency.OrphanTrackIDs)).Scan(ctx, &sources); err != nil {
			return fmt.Errorf("preview library deletion: read orphan track sources: %w", err)
		}
		if err := appendDeletionIdentities(&dependency.TrackSources, sources); err != nil {
			return fmt.Errorf("preview library deletion: encode orphan track source: %w", err)
		}
	}
	if len(dependency.OrphanRecordingIDs) > 0 {
		mappings := make([]libraryDeletionRecordingMapping, 0)
		if err := tx.NewRaw(`SELECT provider_id,provider_recording_id,local_recording_id FROM provider_local_recording WHERE local_recording_id IN (?) ORDER BY provider_id,provider_recording_id`, bun.List(dependency.OrphanRecordingIDs)).Scan(ctx, &mappings); err != nil {
			return fmt.Errorf("preview library deletion: read orphan recording mappings: %w", err)
		}
		if err := appendDeletionIdentities(&dependency.RecordingMappings, mappings); err != nil {
			return fmt.Errorf("preview library deletion: encode orphan recording mapping: %w", err)
		}
		credits := make([]libraryDeletionRecordingCredit, 0)
		if err := tx.NewRaw(`SELECT recording_id,position,artist_id,name,name_join_phrase AS join_phrase FROM recording_artist WHERE recording_id IN (?) ORDER BY recording_id,position`, bun.List(dependency.OrphanRecordingIDs)).Scan(ctx, &credits); err != nil {
			return fmt.Errorf("preview library deletion: read orphan recording credits: %w", err)
		}
		if err := appendDeletionIdentities(&dependency.RecordingCredits, credits); err != nil {
			return fmt.Errorf("preview library deletion: encode orphan recording credit: %w", err)
		}
	}
	return nil
}

func lockLibraryDeletionDependencies(ctx context.Context, tx bun.IDB, dependency libraryDeletionDependencies) error {
	if len(dependency.CandidateTrackIDs) > 0 {
		var locked []uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM local_track WHERE id IN (?) ORDER BY id FOR UPDATE`, bun.List(dependency.CandidateTrackIDs)).Scan(ctx, &locked); err != nil {
			return fmt.Errorf("lock library deletion candidate tracks: %w", err)
		}
		if len(locked) != len(dependency.CandidateTrackIDs) {
			return ErrLibraryDeletionConflict
		}
	}
	if len(dependency.CandidateRecordingIDs) > 0 {
		var locked []uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM local_recording WHERE id IN (?) ORDER BY id FOR UPDATE`, bun.List(dependency.CandidateRecordingIDs)).Scan(ctx, &locked); err != nil {
			return fmt.Errorf("lock library deletion candidate recordings: %w", err)
		}
		if len(locked) != len(dependency.CandidateRecordingIDs) {
			return ErrLibraryDeletionConflict
		}
	}
	if len(dependency.CandidateTrackIDs) > 0 || len(dependency.CandidateRecordingIDs) > 0 {
		conditions := make([]string, 0, 2)
		args := make([]any, 0, 2)
		if len(dependency.CandidateTrackIDs) > 0 {
			conditions = append(conditions, `track_id IN (?)`)
			args = append(args, bun.List(dependency.CandidateTrackIDs))
		}
		if len(dependency.CandidateRecordingIDs) > 0 {
			conditions = append(conditions, `recording_id IN (?)`)
			args = append(args, bun.List(dependency.CandidateRecordingIDs))
		}
		query := `SELECT id FROM release_track WHERE ` + strings.Join(conditions, ` OR `) + ` ORDER BY id FOR UPDATE`
		var locked []uuid.UUID
		if err := tx.NewRaw(query, args...).Scan(ctx, &locked); err != nil {
			return fmt.Errorf("lock library deletion position references: %w", err)
		}
	}
	if len(dependency.OrphanTrackIDs) > 0 {
		var mappingIDs, sourceIDs []uuid.UUID
		var creditPositions []int
		if err := tx.NewRaw(`SELECT provider_track_id FROM provider_local_track WHERE local_track_id IN (?) ORDER BY provider_id,provider_track_id FOR UPDATE`, bun.List(dependency.OrphanTrackIDs)).Scan(ctx, &mappingIDs); err != nil {
			return fmt.Errorf("lock library deletion track mappings: %w", err)
		}
		if err := tx.NewRaw(`SELECT position FROM track_artist WHERE track_id IN (?) ORDER BY track_id,position FOR UPDATE`, bun.List(dependency.OrphanTrackIDs)).Scan(ctx, &creditPositions); err != nil {
			return fmt.Errorf("lock library deletion track credits: %w", err)
		}
		if err := tx.NewRaw(`SELECT id FROM track_source WHERE track_id IN (?) ORDER BY id FOR UPDATE`, bun.List(dependency.OrphanTrackIDs)).Scan(ctx, &sourceIDs); err != nil {
			return fmt.Errorf("lock library deletion track sources: %w", err)
		}
	}
	if len(dependency.OrphanRecordingIDs) > 0 {
		var mappingIDs []uuid.UUID
		var creditPositions []int
		if err := tx.NewRaw(`SELECT provider_recording_id FROM provider_local_recording WHERE local_recording_id IN (?) ORDER BY provider_id,provider_recording_id FOR UPDATE`, bun.List(dependency.OrphanRecordingIDs)).Scan(ctx, &mappingIDs); err != nil {
			return fmt.Errorf("lock library deletion recording mappings: %w", err)
		}
		if err := tx.NewRaw(`SELECT position FROM recording_artist WHERE recording_id IN (?) ORDER BY recording_id,position FOR UPDATE`, bun.List(dependency.OrphanRecordingIDs)).Scan(ctx, &creditPositions); err != nil {
			return fmt.Errorf("lock library deletion recording credits: %w", err)
		}
	}
	return nil
}

func libraryDeletionPositionBlocked(ctx context.Context, tx bun.IDB, positionID, epoch, generation uuid.UUID) (bool, error) {
	var blocked bool
	err := tx.NewRaw(`WITH target AS (SELECT ?::uuid AS position_id, ?::uuid AS epoch, ?::uuid AS generation)
		SELECT
			EXISTS(SELECT 1 FROM track_publication_attempt a, target t WHERE a.position_id=t.position_id)
			OR EXISTS(SELECT 1 FROM publication_execution_claim c JOIN track_publication_attempt a USING (operation_id), target t WHERE a.position_id=t.position_id)
			OR EXISTS(SELECT 1 FROM position_source_authority a LEFT JOIN track_publication p ON p.position_id=a.position_id, target t
				WHERE a.position_id=t.position_id AND (p.position_id IS NULL OR a.publication_epoch<>p.epoch OR a.publication_generation<>p.generation))
			OR EXISTS(SELECT 1 FROM publication_namespace_reservation r JOIN publication_namespace_claim c ON c.id=r.namespace_claim_id, target t
				WHERE r.position_id=t.position_id AND NOT (r.generation=t.generation AND c.epoch=t.epoch AND (
					(c.node_kind='file' AND c.owner_position_id=t.position_id)
					OR (c.node_kind='dir' AND c.owner_position_id IS NULL))))
			OR EXISTS(SELECT 1 FROM publication_namespace_claim c, target t WHERE c.owner_position_id=t.position_id AND c.epoch=t.epoch
				AND NOT EXISTS (SELECT 1 FROM publication_namespace_reservation r WHERE r.namespace_claim_id=c.id AND r.position_id=t.position_id AND r.generation=t.generation))
			OR EXISTS(SELECT 1 FROM publication_root_abandonment a, target t WHERE a.position_id=t.position_id)
			OR EXISTS(SELECT 1 FROM obsolete_publication_output_cleanup o, target t WHERE o.position_id=t.position_id)
			OR EXISTS(SELECT 1 FROM source_cleanup_item i, target t WHERE i.position_id=t.position_id)
			OR EXISTS(SELECT 1 FROM source_cleanup_owner o JOIN source_cleanup_item i ON i.id=o.item_id, target t WHERE i.position_id=t.position_id)
			OR EXISTS(SELECT 1 FROM source_cleanup_explicit_retry_item r JOIN source_cleanup_item i ON i.id=r.item_id, target t WHERE i.position_id=t.position_id)
			OR EXISTS(SELECT 1 FROM source_cleanup_recovery_item r JOIN source_cleanup_item i ON i.id=r.item_id, target t WHERE i.position_id=t.position_id)
			OR EXISTS(SELECT 1 FROM publication_source_cleanup_request r, target t WHERE r.position_id=t.position_id)
			OR EXISTS(SELECT 1 FROM publication_reconciliation_request r, target t WHERE r.position_id=t.position_id)
			OR EXISTS(SELECT 1 FROM publication_reconciliation_dirty_scope r, target t WHERE r.position_id=t.position_id)`,
		positionID, epoch, generation).Scan(ctx, &blocked)
	if err != nil {
		return false, fmt.Errorf("check library deletion publication authority: %w", err)
	}
	return blocked, nil
}

// AdmitLibraryDeletionOperation confirms the exact preview and enqueues its
// ID-only worker atomically with the durable operation snapshot.
func (repository *SetupManagerRepository) AdmitLibraryDeletionOperation(
	ctx context.Context,
	operationID uuid.UUID,
	confirmationDigest string,
	expiresAt time.Time,
	preview LibraryDeletionPreview,
	ownershipWitnesses []json.RawMessage,
	client RiverInserter,
	options *river.InsertOpts,
) (*Operation, error) {
	expectedID, identityErr := LibraryDeletionOperationID(confirmationDigest)
	if client == nil || operationID == uuid.Nil || identityErr != nil || operationID != expectedID {
		return nil, errors.New("library deletion admission is incomplete")
	}
	operation := &Operation{
		ID: operationID, Kind: LibraryDeletionOperationKind, State: "queued", Stage: "queued",
	}
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("admit library deletion: lock output admission gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("admit library deletion: lock source mutations: %w", err)
		}
		existing, err := findLibraryDeletionConfirmationTx(ctx, tx, operationID, confirmationDigest)
		if err != nil {
			return err
		}
		if existing != nil {
			operation = existing
			return nil
		}
		if expiresAt.IsZero() || preview.ScopeDigest == "" || len(preview.Blockers) != 0 {
			return errors.New("library deletion admission is incomplete")
		}
		if !expiresAt.After(time.Now()) {
			return errors.New("library deletion preview confirmation expired")
		}
		var releaseID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM local_release WHERE id=? FOR UPDATE`, preview.Scope.ReleaseID).Scan(ctx, &releaseID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrLibraryReleaseNotFound
			}
			return fmt.Errorf("admit library deletion: lock release: %w", err)
		}
		var lockedPositions []uuid.UUID
		positionQuery := `SELECT id FROM release_track WHERE release_id=? ORDER BY id FOR UPDATE`
		positionArgs := []any{releaseID}
		if preview.Scope.Kind == "publication" && len(preview.Scope.PublicationIDs) == 1 {
			positionQuery = `SELECT id FROM release_track WHERE release_id=? AND id=? ORDER BY id FOR UPDATE`
			positionArgs = append(positionArgs, preview.Scope.PublicationIDs[0])
		}
		if err := tx.NewRaw(positionQuery, positionArgs...).Scan(ctx, &lockedPositions); err != nil {
			return fmt.Errorf("admit library deletion: lock release positions: %w", err)
		}
		var positionID uuid.UUID
		if preview.Scope.Kind == "publication" && len(preview.Scope.PublicationIDs) == 1 {
			positionID = preview.Scope.PublicationIDs[0]
		}
		current, err := readLibraryDeletionPreview(ctx, tx, preview.Scope.Kind, preview.Scope.ReleaseID, positionID)
		if err != nil {
			return err
		}
		if err := lockLibraryDeletionDependencies(ctx, tx, current.dependencies); err != nil {
			return fmt.Errorf("admit library deletion: lock dependency witness: %w", err)
		}
		current, err = readLibraryDeletionPreview(ctx, tx, preview.Scope.Kind, preview.Scope.ReleaseID, positionID)
		if err != nil {
			return err
		}
		if current.ScopeDigest != preview.ScopeDigest || current.Scope.Kind != preview.Scope.Kind || current.Scope.ReleaseID != preview.Scope.ReleaseID || len(current.Blockers) != 0 || !slices.Equal(current.PositionIDs, lockedPositions) {
			return ErrLibraryDeletionConflict
		}
		if len(current.Publications) != len(ownershipWitnesses) {
			return ErrLibraryDeletionConflict
		}
		if err := validateDeletionWitnessPositions(current.Publications, ownershipWitnesses); err != nil {
			return err
		}
		residual := make([]uuid.UUID, 0, len(current.Publications))
		for _, publication := range current.Publications {
			residual = append(residual, publication.PositionID)
		}
		snapshot := LibraryDeletionOperationSnapshot{
			SchemaVersion: 2, Scope: current.Scope, ScopeDigest: current.ScopeDigest,
			ConfirmationDigest: confirmationDigest,
			Dependencies:       current.dependencies,
			OwnershipWitnesses: cloneLibraryDeletionWitnesses(ownershipWitnesses), ResidualPublicationIDs: residual,
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return fmt.Errorf("admit library deletion: encode operation snapshot: %w", err)
		}
		operation.InputSnapshot = raw
		args := LibraryDeletionOperationArgs{OperationID: operation.ID}
		return repository.createOperationAndEnqueueTx(ctx, tx, operation, client, args, options)
	})
	if err != nil {
		return nil, err
	}
	return operation, nil
}

func validateDeletionWitnessPositions(publications []TrackPublication, witnesses []json.RawMessage) error {
	positions := make(map[uuid.UUID]TrackPublication, len(publications))
	for _, publication := range publications {
		positions[publication.PositionID] = publication
	}
	seen := make(map[uuid.UUID]struct{}, len(witnesses))
	for _, raw := range witnesses {
		var witness LibraryDeletionPublicationWitness
		if !publicationJSONObject(raw) || json.Unmarshal(raw, &witness) != nil || witness.PositionID == uuid.Nil || witness.Epoch == uuid.Nil || witness.Generation == uuid.Nil || witness.RelativePath == "" || !publicationJSONObject(witness.Ownership) {
			return errors.New("library deletion ownership witness is invalid")
		}
		publication, ok := positions[witness.PositionID]
		if !ok || publication.Epoch != witness.Epoch || publication.Generation != witness.Generation || publication.RelativePath != witness.RelativePath || !sameJSON(publication.Ownership, witness.Ownership) {
			return ErrLibraryDeletionConflict
		}
		if _, duplicate := seen[witness.PositionID]; duplicate {
			return ErrLibraryDeletionConflict
		}
		seen[witness.PositionID] = struct{}{}
	}
	if len(seen) != len(positions) {
		return ErrLibraryDeletionConflict
	}
	return nil
}

func cloneLibraryDeletionWitnesses(witnesses []json.RawMessage) []json.RawMessage {
	cloned := make([]json.RawMessage, len(witnesses))
	for index, witness := range witnesses {
		cloned[index] = append(json.RawMessage(nil), witness...)
	}
	return cloned
}

// ClaimLibraryDeletionDelivery claims the queued operation or replaces an
// interrupted River delivery without changing its frozen scope or witnesses.
func (repository *SetupManagerRepository) ClaimLibraryDeletionDelivery(
	ctx context.Context,
	guard LibraryDeletionDeliveryGuard,
) (LibraryDeletionDeliveryClaimOutcome, error) {
	if err := validateLibraryDeletionDeliveryGuard(guard); err != nil {
		return LibraryDeletionDeliveryBlocked, err
	}
	outcome := LibraryDeletionDeliveryStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("claim library deletion delivery: acquire output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("claim library deletion delivery: lock source mutations: %w", err)
		}
		snapshot, operation, epoch, positions, live, err := lockLibraryDeletionScope(ctx, tx, guard, "claim library deletion delivery")
		if err != nil || !live {
			return err
		}
		if operation.State == "succeeded" || operation.State == "failed" || operation.State == "dismissed" {
			outcome = LibraryDeletionDeliveryAlreadyTerminal
			return nil
		}
		if operation.State != "queued" && operation.State != "running" {
			outcome = LibraryDeletionDeliveryBlocked
			return nil
		}
		var riverKind, riverState string
		var riverAttempt int
		var riverArgs []byte
		if err := tx.NewRaw(`SELECT kind,state,attempt,args FROM river_job WHERE id=? FOR UPDATE`, guard.RiverJobID).Scan(ctx, &riverKind, &riverState, &riverAttempt, &riverArgs); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("claim library deletion delivery: lock River job: %w", err)
		}
		var args libraryDeletionRiverArgs
		if err := decodeStrictJSON(riverArgs, &args); err != nil {
			return fmt.Errorf("claim library deletion delivery: decode River args: %w", err)
		}
		if riverKind != LibraryDeletionOperationKind || riverState != "running" || riverAttempt != guard.RiverAttempt || args.OperationID != guard.OperationID {
			return nil
		}
		claims := make([]PublicationExecutionClaim, 0, 1)
		if err := tx.NewSelect().Model(&claims).Where("operation_id=?", guard.OperationID).Order("application_attempt").For("UPDATE").Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("claim library deletion delivery: lock claims: %w", err)
		}
		if len(claims) > 1 {
			outcome = LibraryDeletionDeliveryBlocked
			return nil
		}
		if operation.State == "running" {
			if len(claims) == 1 && claims[0].State == "executing" && libraryDeletionClaimMatches(claims[0], guard) {
				outcome = LibraryDeletionDeliveryAlreadyOwned
				return nil
			}
			if err := lockAndValidateLibraryDeletionRiver(ctx, tx, guard); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("claim library deletion delivery: validate interrupted River delivery: %w", err)
			}
			if len(claims) == 1 && claims[0].State != "recovery_required" {
				result, err := tx.NewRaw(`UPDATE publication_execution_claim SET state='recovery_required'
					WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND river_attempt=? AND claim_uuid=? AND boot_uuid=? AND delivery_token=? AND state IN ('executing','quiescent')`,
					claims[0].OperationID, claims[0].ApplicationAttempt, claims[0].RiverJobID, claims[0].RiverAttempt,
					claims[0].ClaimUUID, claims[0].BootUUID, claims[0].DeliveryToken).Exec(ctx)
				if err != nil {
					return fmt.Errorf("claim library deletion delivery: mark interrupted claim: %w", err)
				}
				if err := requireRowsAffected(result, 1, "mark interrupted library deletion claim"); err != nil {
					return err
				}
			}
			const interruptedMessage = "deletion delivery was interrupted; explicitly retry to resume the retained deletion scope"
			result, err := tx.NewRaw(`UPDATE operation SET state='failed',stage='deletion_recovery_required',safe_error=?,finished_at=now(),updated_at=now()
				WHERE id=? AND kind=? AND state='running' AND attempt=? AND river_job_id=?`, interruptedMessage,
				guard.OperationID, LibraryDeletionOperationKind, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
			if err != nil {
				return fmt.Errorf("claim library deletion delivery: record interrupted delivery: %w", err)
			}
			if err := requireRowsAffected(result, 1, "record interrupted library deletion delivery"); err != nil {
				return err
			}
			outcome = LibraryDeletionDeliveryInterrupted
			return nil
		}
		if len(claims) == 1 {
			claim := claims[0]
			if libraryDeletionClaimMatches(claim, guard) && claim.State == "executing" {
				outcome = LibraryDeletionDeliveryAlreadyOwned
				return nil
			}
			if claim.ApplicationAttempt != guard.ApplicationAttempt || claim.RiverJobID != guard.RiverJobID || claim.RiverAttempt > guard.RiverAttempt || claim.Epoch != epoch {
				return nil
			}
			if claim.RiverAttempt == guard.RiverAttempt && claim.BootUUID == guard.BootUUID {
				return nil
			}
			result, err := tx.NewRaw(`UPDATE publication_execution_claim SET river_attempt=?,claim_uuid=?,boot_uuid=?,delivery_token=?,state='executing'
				WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND river_attempt=? AND claim_uuid=? AND boot_uuid=? AND delivery_token=?`,
				guard.RiverAttempt, guard.ClaimUUID, guard.BootUUID, guard.DeliveryToken,
				guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, claim.RiverAttempt, claim.ClaimUUID, claim.BootUUID, claim.DeliveryToken).Exec(ctx)
			if err != nil {
				return fmt.Errorf("claim library deletion delivery: replace claim: %w", err)
			}
			if err := requireRowsAffected(result, 1, "replace library deletion delivery claim"); err != nil {
				return err
			}
		} else {
			claim := PublicationExecutionClaim{
				OperationID: guard.OperationID, ApplicationAttempt: guard.ApplicationAttempt,
				RiverJobID: guard.RiverJobID, RiverAttempt: guard.RiverAttempt,
				ClaimUUID: guard.ClaimUUID, BootUUID: guard.BootUUID, DeliveryToken: guard.DeliveryToken,
				Epoch: epoch, State: "executing",
			}
			if _, err := tx.NewInsert().Model(&claim).Exec(ctx); err != nil {
				return fmt.Errorf("claim library deletion delivery: insert claim: %w", err)
			}
		}
		stage := "deleting_publications"
		if operation.State == "queued" {
			result, err := tx.NewRaw(`UPDATE operation SET state='running',stage=?,started_at=COALESCE(started_at,now()),updated_at=now()
				WHERE id=? AND kind=? AND state='queued' AND attempt=? AND river_job_id=?`, stage, guard.OperationID,
				LibraryDeletionOperationKind, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
			if err != nil {
				return fmt.Errorf("claim library deletion delivery: start operation: %w", err)
			}
			if err := requireRowsAffected(result, 1, "start library deletion operation"); err != nil {
				return err
			}
		} else {
			result, err := tx.NewRaw(`UPDATE operation SET stage=?,updated_at=now() WHERE id=? AND kind=? AND state='running' AND attempt=? AND river_job_id=?`,
				stage, guard.OperationID, LibraryDeletionOperationKind, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
			if err != nil {
				return fmt.Errorf("claim library deletion delivery: resume operation: %w", err)
			}
			if err := requireRowsAffected(result, 1, "resume library deletion operation"); err != nil {
				return err
			}
		}
		if operation.State == "queued" {
			if err := validateLibraryDeletionFrozenScope(ctx, tx, snapshot, epoch, positions); err != nil {
				if !errors.Is(err, ErrLibraryDeletionConflict) && !errors.Is(err, ErrLibraryDeletionBlocked) {
					return fmt.Errorf("claim library deletion delivery: verify frozen scope: %w", err)
				}
				const safeError = "library deletion scope changed after confirmation; restore the retained scope before explicitly retrying"
				claimResult, updateErr := tx.NewRaw(`UPDATE publication_execution_claim SET state='recovery_required'
					WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND river_attempt=? AND claim_uuid=? AND boot_uuid=? AND delivery_token=? AND state='executing'`,
					guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, guard.RiverAttempt,
					guard.ClaimUUID, guard.BootUUID, guard.DeliveryToken).Exec(ctx)
				if updateErr != nil {
					return fmt.Errorf("claim library deletion delivery: retain changed-scope claim: %w", updateErr)
				}
				if err := requireRowsAffected(claimResult, 1, "retain changed-scope library deletion claim"); err != nil {
					return err
				}
				operationResult, updateErr := tx.NewRaw(`UPDATE operation SET state='failed',stage='deletion_recovery_required',safe_error=?,finished_at=now(),updated_at=now()
					WHERE id=? AND kind=? AND state='running' AND attempt=? AND river_job_id=?`, safeError,
					guard.OperationID, LibraryDeletionOperationKind, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
				if updateErr != nil {
					return fmt.Errorf("claim library deletion delivery: record changed frozen scope: %w", updateErr)
				}
				if err := requireRowsAffected(operationResult, 1, "record changed library deletion scope"); err != nil {
					return err
				}
				outcome = LibraryDeletionDeliveryRecoveryRequired
				return nil
			}
		}
		outcome = LibraryDeletionDeliveryAcquired
		return nil
	})
	if err != nil {
		return LibraryDeletionDeliveryBlocked, err
	}
	return outcome, nil
}

func validateLibraryDeletionDeliveryGuard(guard LibraryDeletionDeliveryGuard) error {
	if guard.OperationID == uuid.Nil || guard.ApplicationAttempt <= 0 || guard.RiverJobID <= 0 || guard.RiverAttempt <= 0 ||
		guard.ClaimUUID == uuid.Nil || guard.BootUUID == uuid.Nil || guard.DeliveryToken == "" {
		return errors.New("invalid library deletion delivery guard")
	}
	return nil
}

func libraryDeletionClaimMatches(claim PublicationExecutionClaim, guard LibraryDeletionDeliveryGuard) bool {
	return claim.OperationID == guard.OperationID && claim.ApplicationAttempt == guard.ApplicationAttempt &&
		claim.RiverJobID == guard.RiverJobID && claim.RiverAttempt == guard.RiverAttempt &&
		claim.ClaimUUID == guard.ClaimUUID && claim.BootUUID == guard.BootUUID &&
		claim.DeliveryToken == guard.DeliveryToken
}

func lockLibraryDeletionScope(
	ctx context.Context,
	tx bun.Tx,
	guard LibraryDeletionDeliveryGuard,
	action string,
) (LibraryDeletionOperationSnapshot, *Operation, uuid.UUID, []uuid.UUID, bool, error) {
	var empty LibraryDeletionOperationSnapshot
	var zeroEpoch uuid.UUID
	var riverID int64
	var raw []byte
	if err := tx.NewRaw(`SELECT river_job_id,input_snapshot FROM operation WHERE id=?`, guard.OperationID).Scan(ctx, &riverID, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return empty, nil, zeroEpoch, nil, false, nil
		}
		return empty, nil, zeroEpoch, nil, false, fmt.Errorf("%s: read operation scope: %w", action, err)
	}
	if riverID != guard.RiverJobID {
		return empty, nil, zeroEpoch, nil, false, nil
	}
	var snapshot LibraryDeletionOperationSnapshot
	if err := decodeStrictJSON(raw, &snapshot); err != nil {
		return empty, nil, zeroEpoch, nil, false, fmt.Errorf("%s: decode operation scope: %w", action, err)
	}
	if err := validateLibraryDeletionSnapshot(snapshot); err != nil {
		return empty, nil, zeroEpoch, nil, false, fmt.Errorf("%s: validate operation scope: %w", action, err)
	}
	var epoch uuid.UUID
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return empty, nil, zeroEpoch, nil, false, nil
		}
		return empty, nil, zeroEpoch, nil, false, fmt.Errorf("%s: lock output epoch: %w", action, err)
	}
	var releaseID uuid.UUID
	if err := tx.NewRaw(`SELECT id FROM local_release WHERE id=? FOR UPDATE`, snapshot.Scope.ReleaseID).Scan(ctx, &releaseID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return empty, nil, zeroEpoch, nil, false, nil
		}
		return empty, nil, zeroEpoch, nil, false, fmt.Errorf("%s: lock release: %w", action, err)
	}
	var positions []uuid.UUID
	positionQuery := `SELECT id FROM release_track WHERE release_id=? ORDER BY id FOR UPDATE`
	positionArgs := []any{releaseID}
	if snapshot.Scope.Kind == "publication" {
		positionQuery = `SELECT id FROM release_track WHERE release_id=? AND id=? ORDER BY id FOR UPDATE`
		positionArgs = append(positionArgs, snapshot.Scope.PublicationIDs[0])
	}
	if err := tx.NewRaw(positionQuery, positionArgs...).Scan(ctx, &positions); err != nil {
		return empty, nil, zeroEpoch, nil, false, fmt.Errorf("%s: lock release positions: %w", action, err)
	}
	operation := new(Operation)
	if err := tx.NewSelect().Model(operation).Where("id=?", guard.OperationID).For("UPDATE").Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return empty, nil, zeroEpoch, nil, false, nil
		}
		return empty, nil, zeroEpoch, nil, false, fmt.Errorf("%s: lock operation: %w", action, err)
	}
	var latest LibraryDeletionOperationSnapshot
	if err := decodeStrictJSON(operation.InputSnapshot, &latest); err != nil {
		return empty, nil, zeroEpoch, nil, false, fmt.Errorf("%s: decode locked operation scope: %w", action, err)
	}
	if operation.Kind != LibraryDeletionOperationKind || operation.Attempt != guard.ApplicationAttempt || operation.RiverJobID == nil || *operation.RiverJobID != guard.RiverJobID ||
		latest.ScopeDigest != snapshot.ScopeDigest || !sameLibraryDeletionScope(latest.Scope, snapshot.Scope) {
		return empty, nil, zeroEpoch, nil, false, nil
	}
	return latest, operation, epoch, positions, true, nil
}

func validateLibraryDeletionSnapshot(snapshot LibraryDeletionOperationSnapshot) error {
	dependencyDigest, dependencyErr := libraryDeletionDependencyDigest(snapshot.Dependencies)
	scopeJSON, scopeErr := json.Marshal(snapshot.Scope)
	scopeDigest := sha256.Sum256(scopeJSON)
	if snapshot.SchemaVersion != 2 || snapshot.Scope.ReleaseID == uuid.Nil || snapshot.ScopeDigest == "" || !validSHA256(snapshot.ScopeDigest) ||
		snapshot.ConfirmationDigest == "" || !validSHA256(snapshot.ConfirmationDigest) ||
		(snapshot.Scope.Kind != "release" && snapshot.Scope.Kind != "publication") ||
		(snapshot.Scope.Kind == "publication" && len(snapshot.Scope.PublicationIDs) != 1) ||
		dependencyErr != nil || snapshot.Scope.DependencyDigest != dependencyDigest || scopeErr != nil ||
		snapshot.ScopeDigest != hex.EncodeToString(scopeDigest[:]) ||
		!slices.Equal(snapshot.Dependencies.PositionIDs, snapshot.Scope.PublicationIDs) ||
		!strictlySortedUniqueUUIDs(snapshot.Dependencies.CandidateTrackIDs) ||
		!strictlySortedUniqueUUIDs(snapshot.Dependencies.CandidateRecordingIDs) ||
		!strictlySortedUniqueUUIDs(snapshot.Dependencies.OrphanTrackIDs) ||
		!strictlySortedUniqueUUIDs(snapshot.Dependencies.OrphanRecordingIDs) {
		return ErrLibraryDeletionConflict
	}
	if snapshot.Scope.Kind == "release" {
		triples := make([]libraryDeletionPositionTrackLink, 0, len(snapshot.Dependencies.PositionTrackLinks))
		for _, raw := range snapshot.Dependencies.PositionTrackLinks {
			var triple libraryDeletionPositionTrackLink
			if !publicationJSONObject(raw) || json.Unmarshal(raw, &triple) != nil || triple.PositionID == uuid.Nil ||
				triple.ReleaseID != snapshot.Scope.ReleaseID || triple.TrackID == uuid.Nil {
				return ErrLibraryDeletionConflict
			}
			triples = append(triples, triple)
		}
		if len(triples) != len(snapshot.Scope.PublicationIDs) {
			return ErrLibraryDeletionConflict
		}
		trackSet := make(map[uuid.UUID]struct{}, len(triples))
		recordingSet := make(map[uuid.UUID]struct{}, len(triples))
		for index, triple := range triples {
			if triple.PositionID != snapshot.Scope.PublicationIDs[index] {
				return ErrLibraryDeletionConflict
			}
			trackSet[triple.TrackID] = struct{}{}
			if triple.RecordingID != nil {
				recordingSet[*triple.RecordingID] = struct{}{}
			}
		}
		if !slices.Equal(snapshot.Dependencies.CandidateTrackIDs, sortedUUIDSet(trackSet)) ||
			!slices.Equal(snapshot.Dependencies.CandidateRecordingIDs, sortedUUIDSet(recordingSet)) ||
			!uuidSubset(snapshot.Dependencies.OrphanTrackIDs, snapshot.Dependencies.CandidateTrackIDs) ||
			!uuidSubset(snapshot.Dependencies.OrphanRecordingIDs, snapshot.Dependencies.CandidateRecordingIDs) {
			return ErrLibraryDeletionConflict
		}
	} else if len(snapshot.Dependencies.PositionTrackLinks) != 0 || len(snapshot.Dependencies.CandidateTrackIDs) != 0 ||
		len(snapshot.Dependencies.CandidateRecordingIDs) != 0 || len(snapshot.Dependencies.OrphanTrackIDs) != 0 ||
		len(snapshot.Dependencies.OrphanRecordingIDs) != 0 {
		return ErrLibraryDeletionConflict
	}
	if !strictlySortedUniqueUUIDs(snapshot.Scope.PublicationIDs) || !strictlySortedUniqueUUIDs(snapshot.Scope.ManualReceiptRequestIDs) ||
		!strictlySortedUniqueUUIDs(snapshot.ResidualPublicationIDs) {
		return ErrLibraryDeletionConflict
	}
	for _, id := range snapshot.ResidualPublicationIDs {
		if !slices.Contains(snapshot.Scope.PublicationIDs, id) {
			return ErrLibraryDeletionConflict
		}
	}
	if snapshot.Scope.Kind == "publication" && len(snapshot.Scope.ManualReceiptRequestIDs) != 0 {
		return ErrLibraryDeletionConflict
	}
	witnessed := make(map[uuid.UUID]struct{}, len(snapshot.OwnershipWitnesses))
	for _, raw := range snapshot.OwnershipWitnesses {
		var witness LibraryDeletionPublicationWitness
		if !publicationJSONObject(raw) || json.Unmarshal(raw, &witness) != nil || witness.PositionID == uuid.Nil ||
			witness.Epoch == uuid.Nil || witness.Generation == uuid.Nil || witness.RelativePath == "" || !publicationJSONObject(witness.Ownership) ||
			!slices.Contains(snapshot.Scope.PublicationIDs, witness.PositionID) {
			return ErrLibraryDeletionConflict
		}
		if _, duplicate := witnessed[witness.PositionID]; duplicate {
			return ErrLibraryDeletionConflict
		}
		witnessed[witness.PositionID] = struct{}{}
	}
	for _, id := range snapshot.ResidualPublicationIDs {
		if _, ok := witnessed[id]; !ok {
			return ErrLibraryDeletionConflict
		}
	}
	return nil
}

func uuidSubset(subset, superset []uuid.UUID) bool {
	for _, value := range subset {
		if !slices.Contains(superset, value) {
			return false
		}
	}
	return true
}

func strictlySortedUniqueUUIDs(values []uuid.UUID) bool {
	for index, value := range values {
		if value == uuid.Nil || index > 0 && values[index-1].String() >= value.String() {
			return false
		}
	}
	return true
}

func sameLibraryDeletionScope(left, right LibraryDeletionScope) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func validateLibraryDeletionFrozenScope(ctx context.Context, tx bun.IDB, snapshot LibraryDeletionOperationSnapshot, epoch uuid.UUID, positions []uuid.UUID) error {
	if !slices.Equal(positions, snapshot.Scope.PublicationIDs) {
		return ErrLibraryDeletionConflict
	}
	if err := lockLibraryDeletionDependencies(ctx, tx, snapshot.Dependencies); err != nil {
		return fmt.Errorf("lock frozen library deletion dependencies: %w", err)
	}
	current, err := readLibraryDeletionPreview(ctx, tx, snapshot.Scope.Kind, snapshot.Scope.ReleaseID, func() uuid.UUID {
		if snapshot.Scope.Kind == "publication" {
			return snapshot.Scope.PublicationIDs[0]
		}
		return uuid.Nil
	}())
	if err != nil {
		return err
	}
	if !sameLibraryDeletionScope(current.Scope, snapshot.Scope) || current.ScopeDigest != snapshot.ScopeDigest {
		return ErrLibraryDeletionConflict
	}
	if len(current.Blockers) != 0 {
		return ErrLibraryDeletionBlocked
	}
	for _, publication := range current.Publications {
		if publication.Epoch != epoch {
			return ErrLibraryDeletionBlocked
		}
	}
	return nil
}

// SettleLibraryDeletionPublication invokes the caller's idempotent filesystem
// removal callback with the original frozen witness, then removes exactly that
// CURRENT row and monotonically contracts the operation residual in one tx.
func (repository *SetupManagerRepository) SettleLibraryDeletionPublication(
	ctx context.Context,
	guard LibraryDeletionDeliveryGuard,
	positionID uuid.UUID,
	removeOriginal func(context.Context, json.RawMessage) error,
) (bool, error) {
	if err := validateLibraryDeletionDeliveryGuard(guard); err != nil {
		return false, err
	}
	if positionID == uuid.Nil || removeOriginal == nil {
		return false, errors.New("library deletion settlement requires position and removal callback")
	}
	settled := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("settle library deletion publication: acquire output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("settle library deletion publication: lock source mutations: %w", err)
		}
		snapshot, operation, epoch, positions, live, err := lockLibraryDeletionScope(ctx, tx, guard, "settle library deletion publication")
		if err != nil || !live {
			return err
		}
		if operation.State != "running" {
			return nil
		}
		if err := lockAndValidateLibraryDeletionRiver(ctx, tx, guard); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if err := requireLibraryDeletionClaim(ctx, tx, guard); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if err := validateLibraryDeletionFrozenScope(ctx, tx, snapshot, epoch, positions); err != nil {
			return err
		}
		if !slices.Contains(snapshot.Scope.PublicationIDs, positionID) {
			return ErrLibraryDeletionConflict
		}
		witness, hasWitness := libraryDeletionWitness(snapshot, positionID)
		if !hasWitness {
			return nil
		}
		if !slices.Contains(snapshot.ResidualPublicationIDs, positionID) {
			var exists bool
			if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM track_publication WHERE position_id=?)`, positionID).Scan(ctx, &exists); err != nil {
				return fmt.Errorf("settle library deletion publication: verify prior settlement: %w", err)
			}
			if exists {
				return ErrLibraryDeletionConflict
			}
			settled = true
			return nil
		}
		var frozen LibraryDeletionPublicationWitness
		if !publicationJSONObject(witness) || json.Unmarshal(witness, &frozen) != nil || frozen.PositionID != positionID {
			return ErrLibraryDeletionConflict
		}
		var current TrackPublication
		if err := tx.NewSelect().Model(&current).Where("position_id=?", positionID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrLibraryDeletionConflict
			}
			return fmt.Errorf("settle library deletion publication: lock CURRENT: %w", err)
		}
		if current.Epoch != epoch || current.Epoch != frozen.Epoch || current.Generation != frozen.Generation ||
			current.RelativePath != frozen.RelativePath || !sameJSON(current.Ownership, frozen.Ownership) {
			return ErrLibraryDeletionConflict
		}
		if err := validateLibraryDeletionPositionAuthority(ctx, tx, current); err != nil {
			return err
		}
		if err := removeOriginal(ctx, append(json.RawMessage(nil), witness...)); err != nil {
			return fmt.Errorf("settle library deletion publication: remove original output: %w", err)
		}
		if _, err := tx.NewRaw(`DELETE FROM position_source_authority WHERE position_id=? AND publication_epoch=? AND publication_generation=?`, positionID, current.Epoch, current.Generation).Exec(ctx); err != nil {
			return fmt.Errorf("settle library deletion publication: remove position source authority: %w", err)
		}
		var targetClaimIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT c.id FROM publication_namespace_claim c
			JOIN publication_namespace_reservation r ON r.namespace_claim_id=c.id
			WHERE r.position_id=? AND r.generation=? AND c.epoch=? AND (
				(c.node_kind='file' AND c.owner_position_id=?)
				OR (c.node_kind='dir' AND c.owner_position_id IS NULL))
			ORDER BY c.id FOR UPDATE OF c`, positionID, current.Generation, current.Epoch, positionID).Scan(ctx, &targetClaimIDs); err != nil {
			return fmt.Errorf("settle library deletion publication: lock namespace claims: %w", err)
		}
		_, err = tx.NewRaw(`DELETE FROM publication_namespace_reservation r USING publication_namespace_claim c
			WHERE c.id=r.namespace_claim_id AND r.position_id=? AND r.generation=? AND c.epoch=? AND (
				(c.node_kind='file' AND c.owner_position_id=?)
				OR (c.node_kind='dir' AND c.owner_position_id IS NULL))`,
			positionID, current.Generation, current.Epoch, positionID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("settle library deletion publication: release namespace reservations: %w", err)
		}
		if len(targetClaimIDs) > 0 {
			if _, err := tx.NewRaw(`DELETE FROM publication_namespace_claim c WHERE c.id IN (?) AND c.epoch=?
			AND NOT EXISTS (SELECT 1 FROM publication_namespace_reservation r WHERE r.namespace_claim_id=c.id)
			AND NOT EXISTS (SELECT 1 FROM publication_root_abandonment a WHERE a.epoch=c.epoch AND c.id=ANY(a.namespace_claim_ids))
			AND NOT EXISTS (SELECT 1 FROM obsolete_publication_output_cleanup o WHERE o.namespace_claim_id=c.id)`,
				bun.List(targetClaimIDs), current.Epoch).Exec(ctx); err != nil {
				return fmt.Errorf("settle library deletion publication: retire namespace claims: %w", err)
			}
		}
		result, err := tx.NewRaw(`DELETE FROM track_publication WHERE position_id=? AND epoch=? AND generation=? AND relative_path=? AND ownership=?::jsonb`,
			positionID, current.Epoch, current.Generation, current.RelativePath, current.Ownership).Exec(ctx)
		if err != nil {
			return fmt.Errorf("settle library deletion publication: delete CURRENT: %w", err)
		}
		if err := requireRowsAffected(result, 1, "settle library deletion CURRENT"); err != nil {
			return err
		}
		snapshot.ResidualPublicationIDs = slices.DeleteFunc(snapshot.ResidualPublicationIDs, func(id uuid.UUID) bool { return id == positionID })
		if err := updateLibraryDeletionSnapshot(ctx, tx, guard, snapshot); err != nil {
			return err
		}
		settled = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return settled, nil
}

func libraryDeletionWitness(snapshot LibraryDeletionOperationSnapshot, positionID uuid.UUID) (json.RawMessage, bool) {
	for _, witness := range snapshot.OwnershipWitnesses {
		var candidate LibraryDeletionPublicationWitness
		if json.Unmarshal(witness, &candidate) == nil && candidate.PositionID == positionID {
			return witness, true
		}
	}
	return nil, false
}

func validateLibraryDeletionPositionAuthority(ctx context.Context, tx bun.IDB, current TrackPublication) error {
	var authority PositionSourceAuthority
	err := tx.NewSelect().Model(&authority).Where("position_id=?", current.PositionID).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("validate library deletion position source authority: %w", err)
	}
	if authority.PublicationEpoch != current.Epoch || authority.PublicationGeneration != current.Generation {
		return ErrLibraryDeletionBlocked
	}
	return nil
}

func lockAndValidateLibraryDeletionOrphans(ctx context.Context, tx bun.IDB, snapshot LibraryDeletionOperationSnapshot) (LibraryDeletionPreview, error) {
	preview, err := readLibraryDeletionPreview(ctx, tx, snapshot.Scope.Kind, snapshot.Scope.ReleaseID, uuid.Nil)
	if err != nil {
		return LibraryDeletionPreview{}, err
	}
	if preview.ScopeDigest != snapshot.ScopeDigest || !sameLibraryDeletionScope(preview.Scope, snapshot.Scope) {
		return LibraryDeletionPreview{}, ErrLibraryDeletionConflict
	}
	if len(preview.orphanTrackIDs) > 0 {
		var locked []uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM local_track WHERE id IN (?) ORDER BY id FOR UPDATE`, bun.List(preview.orphanTrackIDs)).Scan(ctx, &locked); err != nil {
			return LibraryDeletionPreview{}, fmt.Errorf("finalize library deletion: lock orphan tracks: %w", err)
		}
		if !slices.Equal(locked, preview.orphanTrackIDs) {
			return LibraryDeletionPreview{}, ErrLibraryDeletionConflict
		}
	}
	if len(preview.RecordingIDs) > 0 {
		var locked []uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM local_recording WHERE id IN (?) ORDER BY id FOR UPDATE`, bun.List(preview.RecordingIDs)).Scan(ctx, &locked); err != nil {
			return LibraryDeletionPreview{}, fmt.Errorf("finalize library deletion: lock orphan recordings: %w", err)
		}
		if !slices.Equal(locked, preview.RecordingIDs) {
			return LibraryDeletionPreview{}, ErrLibraryDeletionConflict
		}
	}
	current, err := readLibraryDeletionPreview(ctx, tx, snapshot.Scope.Kind, snapshot.Scope.ReleaseID, uuid.Nil)
	if err != nil {
		return LibraryDeletionPreview{}, err
	}
	if current.ScopeDigest != snapshot.ScopeDigest || !sameLibraryDeletionScope(current.Scope, snapshot.Scope) {
		return LibraryDeletionPreview{}, ErrLibraryDeletionConflict
	}
	return current, nil
}

func deleteLibraryDeletionOrphans(ctx context.Context, tx bun.IDB, preview LibraryDeletionPreview) error {
	if len(preview.orphanTrackIDs) > 0 {
		for _, statement := range []struct {
			sql  string
			name string
		}{
			{`DELETE FROM provider_local_track WHERE local_track_id IN (?)`, "orphan track provider mappings"},
			{`DELETE FROM track_artist WHERE track_id IN (?)`, "orphan track credits"},
			{`DELETE FROM track_source WHERE track_id IN (?)`, "orphan track source associations"},
			{`DELETE FROM local_track WHERE id IN (?)`, "orphan tracks"},
		} {
			if _, err := tx.NewRaw(statement.sql, bun.List(preview.orphanTrackIDs)).Exec(ctx); err != nil {
				return fmt.Errorf("finalize library deletion: delete %s: %w", statement.name, err)
			}
		}
	}
	if len(preview.RecordingIDs) > 0 {
		for _, statement := range []struct {
			sql  string
			name string
		}{
			{`DELETE FROM provider_local_recording WHERE local_recording_id IN (?)`, "orphan recording provider mappings"},
			{`DELETE FROM recording_artist WHERE recording_id IN (?)`, "orphan recording credits"},
			{`DELETE FROM local_recording WHERE id IN (?)`, "orphan recordings"},
		} {
			if _, err := tx.NewRaw(statement.sql, bun.List(preview.RecordingIDs)).Exec(ctx); err != nil {
				return fmt.Errorf("finalize library deletion: delete %s: %w", statement.name, err)
			}
		}
	}
	return nil
}

func requireLibraryDeletionClaim(ctx context.Context, tx bun.IDB, guard LibraryDeletionDeliveryGuard) error {
	var claim PublicationExecutionClaim
	err := tx.NewSelect().Model(&claim).Where("operation_id=? AND application_attempt=?", guard.OperationID, guard.ApplicationAttempt).For("UPDATE").Scan(ctx)
	if err != nil {
		return err
	}
	if claim.State != "executing" || !libraryDeletionClaimMatches(claim, guard) {
		return sql.ErrNoRows
	}
	return nil
}

func updateLibraryDeletionSnapshot(ctx context.Context, tx bun.IDB, guard LibraryDeletionDeliveryGuard, snapshot LibraryDeletionOperationSnapshot) error {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode library deletion residual: %w", err)
	}
	result, err := tx.NewRaw(`UPDATE operation SET input_snapshot=?::jsonb,updated_at=now()
		WHERE id=? AND kind=? AND state='running' AND attempt=? AND river_job_id=?`,
		string(raw), guard.OperationID, LibraryDeletionOperationKind, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
	if err != nil {
		return fmt.Errorf("update library deletion residual: %w", err)
	}
	return requireRowsAffected(result, 1, "update library deletion residual")
}

// FinalizeLibraryDeletion deletes only the frozen target rows after every
// publication residual has settled. Provider and recording entities are retained.
func (repository *SetupManagerRepository) FinalizeLibraryDeletion(ctx context.Context, guard LibraryDeletionDeliveryGuard) (bool, error) {
	if err := validateLibraryDeletionDeliveryGuard(guard); err != nil {
		return false, err
	}
	finalized := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("finalize library deletion: acquire output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("finalize library deletion: lock source mutations: %w", err)
		}
		snapshot, operation, epoch, positions, live, err := lockLibraryDeletionScope(ctx, tx, guard, "finalize library deletion")
		if err != nil || !live {
			return err
		}
		if operation.State != "running" {
			return nil
		}
		if err := lockAndValidateLibraryDeletionRiver(ctx, tx, guard); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if err := requireLibraryDeletionClaim(ctx, tx, guard); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if len(snapshot.ResidualPublicationIDs) != 0 {
			return nil
		}
		if err := validateLibraryDeletionFrozenScope(ctx, tx, snapshot, epoch, positions); err != nil {
			return err
		}
		var publications int
		if len(snapshot.Scope.PublicationIDs) > 0 {
			if err := tx.NewRaw(`SELECT count(*) FROM track_publication WHERE position_id IN (?)`, bun.List(snapshot.Scope.PublicationIDs)).Scan(ctx, &publications); err != nil {
				return fmt.Errorf("finalize library deletion: verify CURRENT rows: %w", err)
			}
		}
		if publications != 0 {
			return ErrLibraryDeletionConflict
		}
		var deletionPreview LibraryDeletionPreview
		if snapshot.Scope.Kind == "release" {
			deletionPreview, err = lockAndValidateLibraryDeletionOrphans(ctx, tx, snapshot)
			if err != nil {
				return err
			}
		}
		receipts := make([]libraryDeletionReceipt, 0)
		if snapshot.Scope.Kind == "release" {
			if err := tx.NewRaw(`SELECT request_id,original_local_release_id,local_release_id,retired_by_operation_id FROM manual_matching_receipt
				WHERE original_local_release_id=? ORDER BY request_id FOR UPDATE`, snapshot.Scope.ReleaseID).Scan(ctx, &receipts); err != nil {
				return fmt.Errorf("finalize library deletion: lock manual receipts: %w", err)
			}
		}
		if len(receipts) != len(snapshot.Scope.ManualReceiptRequestIDs) {
			return ErrLibraryDeletionConflict
		}
		for index, receipt := range receipts {
			if receipt.RequestID != snapshot.Scope.ManualReceiptRequestIDs[index] || receipt.OriginalID != snapshot.Scope.ReleaseID ||
				receipt.LocalReleaseID == nil || *receipt.LocalReleaseID != snapshot.Scope.ReleaseID || receipt.RetiredBy != nil {
				return ErrLibraryDeletionConflict
			}
		}
		if snapshot.Scope.Kind == "release" && len(receipts) > 0 {
			result, err := tx.NewRaw(`UPDATE manual_matching_receipt SET local_release_id=NULL,retired_by_operation_id=?
				WHERE original_local_release_id=? AND local_release_id=? AND retired_by_operation_id IS NULL
				AND request_id IN (?)`, guard.OperationID, snapshot.Scope.ReleaseID, snapshot.Scope.ReleaseID,
				bun.List(snapshot.Scope.ManualReceiptRequestIDs)).Exec(ctx)
			if err != nil {
				return fmt.Errorf("finalize library deletion: retire manual receipts: %w", err)
			}
			if err := requireRowsAffected(result, int64(len(receipts)), "retire library deletion manual receipts"); err != nil {
				return err
			}
		}
		if snapshot.Scope.Kind == "release" {
			if _, err := tx.NewRaw(`DELETE FROM release_publication_cover WHERE release_id=?`, snapshot.Scope.ReleaseID).Exec(ctx); err != nil {
				return fmt.Errorf("finalize library deletion: delete release cover: %w", err)
			}
			if _, err := tx.NewRaw(`DELETE FROM provider_local_release WHERE local_release_id=?`, snapshot.Scope.ReleaseID).Exec(ctx); err != nil {
				return fmt.Errorf("finalize library deletion: delete provider release links: %w", err)
			}
			if _, err := tx.NewRaw(`DELETE FROM collection_release WHERE release_id=?`, snapshot.Scope.ReleaseID).Exec(ctx); err != nil {
				return fmt.Errorf("finalize library deletion: delete collection links: %w", err)
			}
			if _, err := tx.NewRaw(`DELETE FROM release_artist WHERE release_id=?`, snapshot.Scope.ReleaseID).Exec(ctx); err != nil {
				return fmt.Errorf("finalize library deletion: delete release credits: %w", err)
			}
		}
		if snapshot.Scope.Kind == "release" && len(snapshot.Scope.PublicationIDs) > 0 {
			if _, err := tx.NewRaw(`DELETE FROM release_track WHERE release_id=? AND id IN (?)`, snapshot.Scope.ReleaseID, bun.List(snapshot.Scope.PublicationIDs)).Exec(ctx); err != nil {
				return fmt.Errorf("finalize library deletion: delete frozen positions: %w", err)
			}
		}
		if snapshot.Scope.Kind == "release" {
			if err := deleteLibraryDeletionOrphans(ctx, tx, deletionPreview); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`DELETE FROM release_medium WHERE release_id=?`, snapshot.Scope.ReleaseID).Exec(ctx); err != nil {
				return fmt.Errorf("finalize library deletion: delete release media: %w", err)
			}
			result, err := tx.NewRaw(`DELETE FROM local_release WHERE id=?`, snapshot.Scope.ReleaseID).Exec(ctx)
			if err != nil {
				return fmt.Errorf("finalize library deletion: delete release: %w", err)
			}
			if err := requireRowsAffected(result, 1, "delete frozen library release"); err != nil {
				return err
			}
		}
		if _, err := tx.NewRaw(`DELETE FROM publication_execution_claim WHERE operation_id=? AND application_attempt=? AND river_job_id=? AND river_attempt=? AND claim_uuid=? AND boot_uuid=? AND delivery_token=? AND state='executing'`,
			guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, guard.RiverAttempt, guard.ClaimUUID, guard.BootUUID, guard.DeliveryToken).Exec(ctx); err != nil {
			return fmt.Errorf("finalize library deletion: delete delivery claim: %w", err)
		}
		result, err := tx.NewRaw(`UPDATE operation SET state='succeeded',stage='finished',finished_at=now(),updated_at=now(),safe_error=NULL
			WHERE id=? AND kind=? AND state='running' AND attempt=? AND river_job_id=?`, guard.OperationID,
			LibraryDeletionOperationKind, guard.ApplicationAttempt, guard.RiverJobID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("finalize library deletion: complete operation: %w", err)
		}
		if err := requireRowsAffected(result, 1, "complete library deletion operation"); err != nil {
			return err
		}
		finalized = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return finalized, nil
}
