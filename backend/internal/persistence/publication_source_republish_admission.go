package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/uptrace/bun"
)

// SourceRepublishRetainAdmission authorizes a source republish while retaining
// the currently selected source association.
type SourceRepublishRetainAdmission struct {
	PublicationAdmission
	Prepare PrepareFirstPublicationAdmission
}

type sourceRepublishAdmissionHook struct {
	prepare  PrepareFirstPublicationAdmission
	manifest []PublicationNamespaceNode
}

type admittedSourceRepublishDestination struct {
	positionID   uuid.UUID
	generation   uuid.UUID
	canonicalKey string
	relativePath string
}

// AdmitSourceRepublishRetainingSelection admits a v4 source republish only
// through its typed capability, preserving the existing association selection.
func (repository *SetupManagerRepository) AdmitSourceRepublishRetainingSelection(ctx context.Context, admission SourceRepublishRetainAdmission, client RiverInserter) (uuid.UUID, error) {
	request := admission.PublicationAdmission
	input, err := DecodePublicationInputs(request.FrozenInputs)
	if err != nil || input.Version != PublicationSourceRepublishInputsVersion || input.SourceRepublish == nil || input.Audio == nil ||
		request.IntentKind != "audio" || request.Source == nil || !request.ToolsReadRequired ||
		request.ExpectedGeneration == nil || request.ExpectedReleaseID == uuid.Nil ||
		input.SourceRepublish.Current.Generation != *request.ExpectedGeneration ||
		input.SourceRepublish.SourceAuthority.PublicationEpoch != request.ExpectedEpoch ||
		input.SourceRepublish.SourceAuthority.PositionID != request.PositionID || admission.Prepare == nil {
		return uuid.Nil, fmt.Errorf("admit source republish retaining selection: invalid frozen request: %w", ErrPublicationAdmissionConflict)
	}
	if !input.SourceRepublish.SourceAuthority.Present || input.SourceRepublish.Association.ID == uuid.Nil ||
		input.SourceRepublish.SourceAuthority.SourceLocationID != input.SourceRepublish.Association.LocationID ||
		input.SourceRepublish.SourceAuthority.MediaVariantID != input.SourceRepublish.Association.MediaVariantID ||
		input.SourceRepublish.Mode != "ordinary" && input.SourceRepublish.Mode != "forced" {
		return uuid.Nil, fmt.Errorf("admit source republish retaining selection: source authority is required: %w", ErrPublicationAdmissionConflict)
	}
	if err := input.SourceRepublish.Validate(*input.Audio); err != nil {
		return uuid.Nil, fmt.Errorf("admit source republish retaining selection: invalid frozen inputs: %w", err)
	}
	request.sourceRepublishAdmission = &sourceRepublishAdmissionHook{prepare: admission.Prepare}
	return repository.AdmitPublication(ctx, request, client)
}

// prepareSourceRepublishAdmission plans from a fresh snapshot after the epoch
// row and the same read guards used by first-publication admission are held.
func (repository *SetupManagerRepository) prepareSourceRepublishAdmission(ctx context.Context, tx bun.Tx, request PublicationAdmission) error {
	hook := request.sourceRepublishAdmission
	if hook == nil || hook.prepare == nil {
		return fmt.Errorf("admit source republish: missing planner: %w", ErrPublicationAdmissionConflict)
	}
	if err := lockFirstPublicationAdmissionReadGuards(ctx, tx, false, "admit source republish"); err != nil {
		return err
	}
	var snapshot FirstPublicationAdmissionSnapshot
	if err := readLibraryFirstPublicationContextTx(ctx, tx, request.ExpectedReleaseID, request.PositionID, &snapshot.Context); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("admit source republish: context changed: %w", ErrPublicationAdmissionConflict)
		}
		return fmt.Errorf("admit source republish: read context: %w", err)
	}
	snapshot.Settings = make(map[string]string)
	var settings []AppSetting
	if err := tx.NewSelect().Model(&settings).Scan(ctx); err != nil {
		return fmt.Errorf("admit source republish: read settings: %w", err)
	}
	for _, setting := range settings {
		snapshot.Settings[setting.Name] = setting.Value
	}
	snapshot.Installations = make([]ToolInstallation, 0)
	if err := tx.NewSelect().Model(&snapshot.Installations).Where("package_kind = ? AND state = ?", "ffmpeg", "ready").Scan(ctx); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("admit source republish: read ffmpeg installations: %w", err)
	}
	claims, err := readPublicationNamespaceClaims(ctx, tx, request.ExpectedEpoch)
	if err != nil {
		return fmt.Errorf("admit source republish: read namespace claims: %w", err)
	}
	snapshot.Claims = claims
	input, err := DecodePublicationInputs(request.FrozenInputs)
	if err != nil || input.SourceRepublish == nil {
		return fmt.Errorf("admit source republish: invalid frozen inputs: %w", ErrPublicationAdmissionConflict)
	}
	cover, err := readReleaseCover(ctx, tx, input.SourceRepublish.Cover.ReleaseID)
	if err == sql.ErrNoRows {
		cover = ReleaseCover{ReleaseID: input.SourceRepublish.Cover.ReleaseID, Revision: 1, Mode: "automatic"}
	} else if err != nil {
		return fmt.Errorf("admit source republish: read cover authority: %w", err)
	}
	if !sourceRepublishCoverAuthorityMatches(input.SourceRepublish.Cover, cover) {
		return fmt.Errorf("admit source republish: selected cover authority changed: %w", ErrPublicationAdmissionConflict)
	}
	currentClaim, err := readSourceRepublishCurrentNamespaceClaim(ctx, tx, request, input)
	if err != nil {
		return err
	}
	plannerRequest := request
	plannerRequest.sourceRepublishAdmission = nil
	plannerRequest.FrozenInputs = append(json.RawMessage(nil), request.FrozenInputs...)
	plannerRequest.Tools = append([]SourceAnalysisToolSelection(nil), request.Tools...)
	plannerRequest.NamespaceManifest = append([]PublicationNamespaceNode(nil), request.NamespaceManifest...)
	if request.Source != nil {
		source := *request.Source
		plannerRequest.Source = &source
	}
	manifest, err := hook.prepare(ctx, snapshot, plannerRequest)
	if err != nil {
		return fmt.Errorf("admit source republish: prepare namespace: %w", err)
	}
	if err := validatePublicationManifest(manifest); err != nil {
		return err
	}
	if err := validateSourceRepublishNamespacePlan(ctx, tx, request, input, manifest, currentClaim, nil); err != nil {
		return err
	}
	hook.manifest = manifest
	return nil
}

func readSourceRepublishCurrentNamespaceClaim(ctx context.Context, tx bun.Tx, request PublicationAdmission, input PublicationInputs) (PublicationNamespaceClaim, error) {
	var claims []PublicationNamespaceClaim
	err := tx.NewRaw(`SELECT c.id,c.epoch,c.key_digest,c.canonical_key,c.relative_path,c.node_kind,c.owner_position_id,c.directory_scope_key,
		false AS abandoned
		FROM publication_namespace_claim c
		JOIN publication_namespace_reservation r ON r.namespace_claim_id=c.id
		WHERE c.epoch=? AND c.owner_position_id=? AND c.node_kind='file'
			AND c.relative_path=? AND r.position_id=? AND r.generation=?
			AND NOT EXISTS (SELECT 1 FROM publication_root_abandonment a WHERE a.epoch=c.epoch AND c.id=ANY(a.namespace_claim_ids))`,
		request.ExpectedEpoch, request.PositionID, input.SourceRepublish.Current.RelativePath,
		request.PositionID, input.SourceRepublish.Current.Generation).Scan(ctx, &claims)
	if errors.Is(err, sql.ErrNoRows) {
		return PublicationNamespaceClaim{}, fmt.Errorf("admit source republish: read exact CURRENT namespace claim: %w", ErrPublicationAdmissionConflict)
	}
	if err != nil {
		return PublicationNamespaceClaim{}, fmt.Errorf("admit source republish: read exact CURRENT namespace claim: %w", err)
	}
	if len(claims) != 1 {
		return PublicationNamespaceClaim{}, fmt.Errorf("admit source republish: CURRENT namespace identity is missing or ambiguous: %w", ErrPublicationAdmissionConflict)
	}
	claim := claims[0]
	if claim.ID == uuid.Nil || claim.Abandoned || claim.RelativePath != input.SourceRepublish.Current.RelativePath ||
		claim.OwnerPositionID == nil || *claim.OwnerPositionID != request.PositionID || claim.Epoch != request.ExpectedEpoch || claim.NodeKind != "file" {
		return PublicationNamespaceClaim{}, fmt.Errorf("admit source republish: exact CURRENT namespace claim is not active: %w", ErrPublicationAdmissionConflict)
	}
	return claim, nil
}

func validateSourceRepublishNamespacePlan(ctx context.Context, tx bun.Tx, request PublicationAdmission, input PublicationInputs, manifest []PublicationNamespaceNode, current PublicationNamespaceClaim, admittedDestination *admittedSourceRepublishDestination) error {
	files := 0
	var destination PublicationNamespaceNode
	for _, node := range manifest {
		if node.NodeKind != "file" {
			continue
		}
		files++
		destination = node
	}
	if files != 1 {
		return fmt.Errorf("admit source republish: namespace manifest must contain exactly one destination file: %w", ErrPublicationAdmissionConflict)
	}
	sameCurrentPath := destination.RelativePath == input.SourceRepublish.Current.RelativePath
	if sameCurrentPath && (destination.CanonicalKey != current.CanonicalKey || destination.RelativePath != current.RelativePath) {
		return fmt.Errorf("admit source republish: same-path namespace must retain the exact CURRENT claim: %w", ErrPublicationAdmissionConflict)
	}
	if !sameCurrentPath {
		if destination.CanonicalKey == current.CanonicalKey {
			return fmt.Errorf("admit source republish: changed destination aliases the CURRENT claim: %w", ErrPublicationAdmissionConflict)
		}
		if admittedDestination != nil && (admittedDestination.positionID != request.PositionID ||
			admittedDestination.generation != request.CandidateGeneration ||
			admittedDestination.canonicalKey != destination.CanonicalKey ||
			admittedDestination.relativePath != destination.RelativePath) {
			return fmt.Errorf("admit source republish: admitted destination does not match manifest: %w", ErrPublicationAdmissionConflict)
		}
		var retainedDestination bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM publication_namespace_claim c
			JOIN publication_namespace_reservation r ON r.namespace_claim_id=c.id
			WHERE c.epoch=? AND c.owner_position_id=? AND c.node_kind='file'
				AND (c.canonical_key=? OR c.relative_path=?)
				AND NOT (? AND r.position_id=? AND r.generation=? AND c.canonical_key=? AND c.relative_path=?)
				AND c.id<>? AND NOT EXISTS (
					SELECT 1 FROM publication_root_abandonment a
					WHERE a.epoch=c.epoch AND c.id=ANY(a.namespace_claim_ids)
				)
		)`, request.ExpectedEpoch, request.PositionID, destination.CanonicalKey, destination.RelativePath,
			admittedDestination != nil, request.PositionID, request.CandidateGeneration, destination.CanonicalKey, destination.RelativePath,
			current.ID).Scan(ctx, &retainedDestination); err != nil {
			return fmt.Errorf("admit source republish: check retained destination claim: %w", err)
		}
		if retainedDestination {
			return fmt.Errorf("admit source republish: destination conflicts with another retained position claim: %w", ErrPublicationAdmissionConflict)
		}
	}
	// Reusing the old path requires its exact active directory scopes. Changed
	// paths may use fresh/shared scopes; manifest validation checks completeness.
	for _, node := range manifest {
		if node.NodeKind != "dir" {
			continue
		}
		if !sameCurrentPath {
			continue
		}
		if !strings.HasPrefix(input.SourceRepublish.Current.RelativePath, node.RelativePath+"/") {
			return fmt.Errorf("admit source republish: namespace directory is not a CURRENT path parent: %w", ErrPublicationAdmissionConflict)
		}
		var active bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM publication_namespace_claim c
			JOIN publication_namespace_reservation r ON r.namespace_claim_id=c.id
			WHERE c.epoch=? AND c.canonical_key=? AND c.relative_path=? AND c.node_kind='dir'
				AND c.directory_scope_key=? AND r.position_id=? AND r.generation=?
				AND NOT EXISTS (SELECT 1 FROM publication_root_abandonment a WHERE a.epoch=c.epoch AND c.id=ANY(a.namespace_claim_ids))
		)`, request.ExpectedEpoch, node.CanonicalKey, node.RelativePath, node.DirectoryScope, request.PositionID,
			input.SourceRepublish.Current.Generation).Scan(ctx, &active); err != nil {
			return fmt.Errorf("admit source republish: verify CURRENT namespace parent scope: %w", err)
		}
		if !active {
			return fmt.Errorf("admit source republish: namespace parent is not an active CURRENT scope: %w", ErrPublicationAdmissionConflict)
		}
	}
	return nil
}

func validateSourceRepublishAttemptNamespace(
	ctx context.Context,
	tx bun.Tx,
	attempt *TrackPublicationAttempt,
	input PublicationInputs,
) (PublicationNamespaceClaim, error) {
	if attempt == nil || !validSourceRepublishPreparation(input) || input.SourceRepublish == nil || attempt.ExpectedGeneration == nil ||
		*attempt.ExpectedGeneration != input.SourceRepublish.Current.Generation || attempt.Epoch == uuid.Nil {
		return PublicationNamespaceClaim{}, fmt.Errorf("validate source republish namespace: invalid attempt baseline: %w", ErrPublicationAdmissionConflict)
	}
	var manifest struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}
	if decodeStrictJSON(attempt.NamespaceManifest, &manifest) != nil || manifest.Version != 1 || validatePublicationManifest(manifest.Nodes) != nil {
		return PublicationNamespaceClaim{}, fmt.Errorf("validate source republish namespace: invalid candidate manifest: %w", ErrPublicationAdmissionConflict)
	}
	request := PublicationAdmission{
		PositionID: attempt.PositionID, ExpectedEpoch: attempt.Epoch, CandidateGeneration: attempt.CandidateGeneration,
	}
	currentClaim, err := readSourceRepublishCurrentNamespaceClaim(ctx, tx, request, input)
	if err != nil {
		return PublicationNamespaceClaim{}, err
	}
	var destination PublicationNamespaceNode
	files := 0
	for _, node := range manifest.Nodes {
		if node.NodeKind == "file" {
			files++
			destination = node
		}
	}
	if files != 1 {
		return PublicationNamespaceClaim{}, fmt.Errorf("validate source republish namespace: manifest must contain exactly one destination file: %w", ErrPublicationAdmissionConflict)
	}
	if err := validatePublicationTargetNamespace(ctx, tx, attempt, destination.CanonicalKey, destination.RelativePath); err != nil {
		return PublicationNamespaceClaim{}, fmt.Errorf("validate source republish namespace: candidate does not reserve exact manifest destination: %w", err)
	}
	admittedDestination := &admittedSourceRepublishDestination{
		positionID: attempt.PositionID, generation: attempt.CandidateGeneration,
		canonicalKey: destination.CanonicalKey, relativePath: destination.RelativePath,
	}
	if err := validateSourceRepublishNamespacePlan(ctx, tx, request, input, manifest.Nodes, currentClaim, admittedDestination); err != nil {
		return PublicationNamespaceClaim{}, err
	}
	var reservationCount int
	if err := tx.NewRaw(`SELECT count(*) FROM publication_namespace_reservation WHERE position_id=? AND generation=?`,
		attempt.PositionID, attempt.CandidateGeneration).Scan(ctx, &reservationCount); err != nil {
		return PublicationNamespaceClaim{}, fmt.Errorf("validate source republish namespace: count candidate reservations: %w", err)
	}
	if reservationCount != len(manifest.Nodes) {
		return PublicationNamespaceClaim{}, fmt.Errorf("validate source republish namespace: candidate reservations differ from manifest: %w", ErrPublicationAdmissionConflict)
	}
	return currentClaim, nil
}

func validateSourceRepublishAdmissionTx(ctx context.Context, tx bun.Tx, request PublicationAdmission, input PublicationInputs, epoch uuid.UUID) error {
	if input.SourceRepublish == nil || !validSourceRepublishPreparation(input) {
		return fmt.Errorf("admit source republish: invalid source-republish inputs: %w", ErrPublicationAdmissionConflict)
	}
	var current TrackPublication
	if err := tx.NewSelect().Model(&current).Where("position_id = ?", request.PositionID).For("UPDATE").Scan(ctx); err != nil {
		return fmt.Errorf("admit source republish: read current publication: %w", err)
	}
	var authority PositionSourceAuthority
	if err := tx.NewSelect().Model(&authority).Where("position_id = ?", request.PositionID).For("UPDATE").Scan(ctx); err != nil {
		return fmt.Errorf("admit source republish: read source authority: %w", err)
	}
	if !sourceRepublishCurrent(current, authority, input, epoch) {
		return fmt.Errorf("admit source republish: current publication or source authority changed: %w", ErrPublicationAdmissionConflict)
	}
	var positionMetadata struct {
		Metadata json.RawMessage `bun:"metadata,type:jsonb"`
		Revision int64           `bun:"metadata_revision"`
	}
	if err := tx.NewRaw(`SELECT metadata, metadata_revision FROM release_track WHERE id=?`, request.PositionID).Scan(ctx, &positionMetadata); err != nil {
		return fmt.Errorf("admit source republish: read lyrics authority: %w", err)
	}
	if positionMetadata.Revision != input.SourceRepublish.LyricsAuthority.ExpectedMetadataRevision {
		return fmt.Errorf("admit source republish: lyrics authority revision changed: %w", ErrPublicationAdmissionConflict)
	}
	envelope, err := decodeFirstPublicationEnvelopeValue(positionMetadata.Metadata, librarymetadata.ScopePosition)
	if err != nil {
		return fmt.Errorf("admit source republish: decode lyrics authority: %w", err)
	}
	actualLyrics, present := envelope.Desired["synced_lyrics"]
	wantedLyrics := input.SourceRepublish.LyricsAuthority.Desired
	if wantedLyrics == nil && present || wantedLyrics != nil && (!present || !sameJSONValue(mustPublicationJSON(actualLyrics), mustPublicationJSON(*wantedLyrics))) {
		return fmt.Errorf("admit source republish: lyrics authority changed: %w", ErrPublicationAdmissionConflict)
	}
	var association struct {
		ID             uuid.UUID `bun:"id,type:uuid"`
		TrackID        uuid.UUID `bun:"track_id,type:uuid"`
		LocationID     uuid.UUID `bun:"source_location_id,type:uuid"`
		MediaVariantID uuid.UUID `bun:"media_variant_id,type:uuid"`
		Selected       bool      `bun:"selected"`
		Compatible     bool      `bun:"compatible"`
	}
	err = tx.NewRaw(`SELECT association.id, association.track_id, work.current_location_id AS source_location_id,
		association.media_variant_id, association.selected, association.compatible
		FROM track_source association
		JOIN source_work_variant_identity identity ON identity.media_variant_id=association.media_variant_id
		JOIN source_analysis_work work ON work.id=identity.work_id
		WHERE association.id=? AND association.track_id=(SELECT track_id FROM release_track WHERE id=?)`, input.SourceRepublish.Association.ID, request.PositionID).Scan(ctx, &association)
	if err != nil || !association.Selected || !association.Compatible || association.LocationID != input.SourceRepublish.Association.LocationID ||
		association.MediaVariantID != input.SourceRepublish.Association.MediaVariantID || request.Source == nil ||
		request.Source.LocationID != association.LocationID || request.Source.MediaVariantID != association.MediaVariantID {
		return fmt.Errorf("admit source republish: selected source association changed: %w", ErrPublicationAdmissionConflict)
	}
	return nil
}
