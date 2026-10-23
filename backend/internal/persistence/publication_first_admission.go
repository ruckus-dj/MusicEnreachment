package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// FirstPublicationAdmissionSnapshot is the authoritative database view supplied
// to the service planner while the admission transaction holds its read guards.
type FirstPublicationAdmissionSnapshot struct {
	Context       *LibraryFirstPublicationContext
	Settings      map[string]string
	Installations []ToolInstallation
	Claims        []PublicationNamespaceClaim
	PreviewDigest string
	CoverInput    PublicationAttemptCoverInput
}

// PrepareFirstPublicationAdmission plans the final native namespace from the
// transactionally consistent snapshot. It must reject stale or mismatched
// preview/frozen inputs rather than silently replanning them.
type PrepareFirstPublicationAdmission func(context.Context, FirstPublicationAdmissionSnapshot, PublicationAdmission) ([]PublicationNamespaceNode, error)

// PublicationSourceAssociation is the transactionally observed decision row
// needed to authorize a manual first-publication source change.
type PublicationSourceAssociation struct {
	ID                     uuid.UUID
	TrackID                uuid.UUID
	MediaVariantID         uuid.UUID
	SourceMetadataResultID *uuid.UUID
	Selected               bool
	Compatible             bool
	DecisionMethod         string
	PolicyVersion          *string
	Metadata               json.RawMessage
	UpdatedAt              time.Time
}

// FirstPublicationSourceSelection is a complete witness for the current
// association set, not a selector that can be detached from its preview.
type FirstPublicationSourceSelection struct {
	AssociationID uuid.UUID
	Associations  []PublicationSourceAssociation
}

type FirstPublicationAdmission struct {
	PublicationAdmission
	PreviewDigest   string
	PrepareFirst    PrepareFirstPublicationAdmission
	SourceSelection *FirstPublicationSourceSelection
}

type firstPublicationAdmissionHook struct {
	digest          string
	prepare         PrepareFirstPublicationAdmission
	sourceSelection *FirstPublicationSourceSelection
	coverInput      *PublicationAttemptCoverInput
}

// AdmitFirstPublicationFromPreview binds native namespace planning to this same
// transaction's first-publication snapshot and existing admission/reservation path.
func (repository *SetupManagerRepository) AdmitFirstPublicationFromPreview(ctx context.Context, first FirstPublicationAdmission, client RiverInserter) (uuid.UUID, error) {
	request := first.PublicationAdmission
	if client == nil || first.PrepareFirst == nil || !validFirstPublicationPreviewDigest(first.PreviewDigest) ||
		request.ExpectedReleaseID == uuid.Nil || request.PositionID == uuid.Nil || request.ExpectedEpoch == uuid.Nil ||
		request.CandidateGeneration == uuid.Nil || request.IntentKind != "audio" || request.ExpectedGeneration != nil {
		return uuid.Nil, fmt.Errorf("admit first publication: invalid preview admission")
	}
	if first.SourceSelection != nil && first.reconciliationGuard != nil {
		return uuid.Nil, fmt.Errorf("admit first publication: automatic admission cannot select a source association")
	}
	var selection *FirstPublicationSourceSelection
	if first.SourceSelection != nil {
		copySelection := cloneFirstPublicationSourceSelection(*first.SourceSelection)
		selection = &copySelection
	}
	request.firstAdmission = &firstPublicationAdmissionHook{digest: first.PreviewDigest, prepare: first.PrepareFirst, sourceSelection: selection}
	return repository.AdmitPublication(ctx, request, client)
}

func cloneFirstPublicationSourceSelection(selection FirstPublicationSourceSelection) FirstPublicationSourceSelection {
	result := FirstPublicationSourceSelection{AssociationID: selection.AssociationID, Associations: make([]PublicationSourceAssociation, len(selection.Associations))}
	for i, association := range selection.Associations {
		result.Associations[i] = association
		result.Associations[i].Metadata = append(json.RawMessage(nil), association.Metadata...)
		if association.SourceMetadataResultID != nil {
			id := *association.SourceMetadataResultID
			result.Associations[i].SourceMetadataResultID = &id
		}
		if association.PolicyVersion != nil {
			policy := *association.PolicyVersion
			result.Associations[i].PolicyVersion = &policy
		}
	}
	return result
}

func (repository *SetupManagerRepository) applyFirstPublicationSourceSelection(ctx context.Context, tx bun.Tx, position, epoch uuid.UUID, source PublicationSourceSelection, frozen PublicationAudioInput, selection FirstPublicationSourceSelection) error {
	var trackID uuid.UUID
	if err := tx.NewRaw(`SELECT track_id FROM release_track WHERE id=?`, position).Scan(ctx, &trackID); err != nil {
		return fmt.Errorf("admit first publication: read selected track: %w", err)
	}
	var current []TrackSource
	if err := tx.NewSelect().Model(&current).Where("track_id=?", trackID).Order("id ASC").For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("admit first publication: lock source associations: %w", err)
	}
	if len(current) != len(selection.Associations) || selection.AssociationID == uuid.Nil {
		return fmt.Errorf("admit first publication: source association witness changed: %w", ErrPublicationAdmissionConflict)
	}
	selectedFound := false
	for i, actual := range current {
		witness := selection.Associations[i]
		if actual.ID != witness.ID || actual.TrackID != witness.TrackID || actual.MediaVariantID != witness.MediaVariantID || !sameOptionalUUID(actual.SourceMetadataResultID, witness.SourceMetadataResultID) || actual.Selected != witness.Selected || actual.Compatible != witness.Compatible || actual.DecisionMethod != witness.DecisionMethod || !sameOptionalString(actual.PolicyVersion, witness.PolicyVersion) || !bytes.Equal(actual.Metadata, witness.Metadata) || !actual.UpdatedAt.Equal(witness.UpdatedAt) {
			return fmt.Errorf("admit first publication: source association witness changed: %w", ErrPublicationAdmissionConflict)
		}
		if actual.ID == selection.AssociationID {
			if actual.TrackID != trackID || actual.MediaVariantID != frozen.MediaVariantID || !actual.Compatible {
				return fmt.Errorf("admit first publication: selected source association is incompatible: %w", ErrPublicationAdmissionConflict)
			}
			selectedFound = true
		}
	}
	if !selectedFound || source.MediaVariantID != frozen.MediaVariantID {
		return fmt.Errorf("admit first publication: selected source association does not match source input: %w", ErrPublicationAdmissionConflict)
	}
	changed := false
	for _, actual := range current {
		chosen := actual.ID == selection.AssociationID
		if actual.Selected == chosen {
			continue
		}
		changed = true
		if err := rejectCleanupOwnedSourceVariant(ctx, tx, actual.MediaVariantID); err != nil {
			if errors.Is(err, ErrSourceCleanupOwned) {
				return fmt.Errorf("admit first publication: changed source association is cleanup-owned: %w: %w", ErrPublicationAdmissionConflict, err)
			}
			return fmt.Errorf("admit first publication: changed source association is cleanup-owned: %w", err)
		}
		var held bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM publication_source_hold WHERE media_variant_id=?)`, actual.MediaVariantID).Scan(ctx, &held); err != nil {
			return fmt.Errorf("admit first publication: check changed source variant hold: %w", err)
		}
		if held {
			return fmt.Errorf("admit first publication: changed source association is held: %w", ErrPublicationAdmissionConflict)
		}
		type sourceWorkIdentity struct {
			WorkID            uuid.UUID  `bun:"work_id"`
			SourceRootID      uuid.UUID  `bun:"source_root_id"`
			CurrentLocationID *uuid.UUID `bun:"current_location_id"`
		}
		var identities []sourceWorkIdentity
		if err := tx.NewRaw(`SELECT identity.work_id, work.source_root_id, work.current_location_id FROM source_work_variant_identity identity JOIN source_analysis_work work ON work.id=identity.work_id WHERE identity.media_variant_id=? ORDER BY identity.work_id`, actual.MediaVariantID).Scan(ctx, &identities); err != nil {
			return fmt.Errorf("admit first publication: read changed source identities: %w", err)
		}
		for _, identity := range identities {
			var locationID uuid.UUID
			if identity.CurrentLocationID != nil {
				locationID = *identity.CurrentLocationID
			}
			held, err := sourceHoldExists(ctx, tx, identity.SourceRootID, locationID, identity.WorkID)
			if err != nil {
				return fmt.Errorf("admit first publication: check changed source hold: %w", err)
			}
			if held {
				return fmt.Errorf("admit first publication: changed source association is held: %w", ErrPublicationAdmissionConflict)
			}
		}
	}
	if !changed {
		return nil
	}
	if _, err := tx.NewRaw(`UPDATE track_source SET selected=false, updated_at=now() WHERE track_id=? AND id<>? AND selected=true`, trackID, selection.AssociationID).Exec(ctx); err != nil {
		return fmt.Errorf("admit first publication: deselect prior source associations: %w", err)
	}
	if _, err := tx.NewRaw(`UPDATE track_source SET selected=true, updated_at=now() WHERE id=? AND selected=false`, selection.AssociationID).Exec(ctx); err != nil {
		return fmt.Errorf("admit first publication: select source association: %w", err)
	}
	var positions []uuid.UUID
	if err := tx.NewRaw(`SELECT id FROM release_track WHERE track_id=? ORDER BY id`, trackID).Scan(ctx, &positions); err != nil {
		return fmt.Errorf("admit first publication: list source-borrowing positions: %w", err)
	}
	if err := repository.RequestPublicationReconciliationWith(ctx, tx, epoch, positions, []string{"source_selection"}); err != nil {
		return fmt.Errorf("admit first publication: request source-selection reconciliation: %w", err)
	}
	return nil
}

// AdmitAutomaticFirstPublicationFromPreview binds an internal automatic source
// choice and reconciliation delivery to the same first-publication transaction.
func (repository *SetupManagerRepository) AdmitAutomaticFirstPublicationFromPreview(ctx context.Context, first FirstPublicationAdmission, guard PublicationReconciliationDeliveryGuard, client RiverInserter) (uuid.UUID, error) {
	if guard.OutputEpoch != first.ExpectedEpoch || guard.PositionID != first.PositionID ||
		guard.OperationID == uuid.Nil || guard.ClaimToken == uuid.Nil {
		return uuid.Nil, fmt.Errorf("admit automatic first publication: invalid reconciliation guard: %w", ErrPublicationAdmissionConflict)
	}
	guardCopy := guard
	first.reconciliationGuard = &guardCopy
	return repository.AdmitFirstPublicationFromPreview(ctx, first, client)
}

func (repository *SetupManagerRepository) prepareFirstPublicationAdmission(ctx context.Context, tx bun.Tx, request PublicationAdmission) ([]PublicationNamespaceNode, error) {
	hook := request.firstAdmission
	if hook == nil || hook.prepare == nil {
		return nil, fmt.Errorf("admit first publication: missing planner")
	}
	tables := []string{
		"app_setting", "local_artist", "local_recording", "local_release", "local_track", "media_variant", "release_publication_cover",
		"provider", "provider_artist", "provider_local_artist", "publication_namespace_claim", "publication_namespace_reservation", "release_artist", "release_track",
		"source_analysis_step", "source_analysis_work", "source_location", "source_root", "source_work_variant_identity",
		"tool_installation", "track_artist", "track_source",
	}
	for _, table := range tables {
		mode := "SHARE"
		if table == "track_source" && hook.sourceSelection != nil {
			mode = "SHARE ROW EXCLUSIVE"
		}
		if _, err := tx.ExecContext(ctx, "LOCK TABLE "+table+" IN "+mode+" MODE"); err != nil {
			return nil, fmt.Errorf("admit first publication: guard %s: %w", table, err)
		}
	}
	var snapshot FirstPublicationAdmissionSnapshot
	if err := readLibraryFirstPublicationContextTx(ctx, tx, request.ExpectedReleaseID, request.PositionID, &snapshot.Context); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("admit first publication: context changed: %w", ErrPublicationAdmissionConflict)
		}
		return nil, fmt.Errorf("admit first publication: read context: %w", err)
	}
	snapshot.Settings = make(map[string]string)
	var settings []AppSetting
	if err := tx.NewSelect().Model(&settings).Scan(ctx); err != nil {
		return nil, fmt.Errorf("admit first publication: read settings: %w", err)
	}
	for _, setting := range settings {
		snapshot.Settings[setting.Name] = setting.Value
	}
	snapshot.Installations = make([]ToolInstallation, 0)
	if err := tx.NewSelect().Model(&snapshot.Installations).Where("package_kind = ? AND state = ?", "ffmpeg", "ready").Scan(ctx); err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("admit first publication: read ffmpeg installations: %w", err)
	}
	snapshot.Claims = make([]PublicationNamespaceClaim, 0)
	if err := tx.NewSelect().Model(&snapshot.Claims).Where("epoch = ?", request.ExpectedEpoch).Scan(ctx); err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("admit first publication: read namespace claims: %w", err)
	}
	snapshot.PreviewDigest = hook.digest
	authoritativeCoverInput, err := readEffectiveReleaseCoverInput(ctx, tx, request.ExpectedReleaseID, snapshot.Context.EffectiveCover)
	if err != nil {
		return nil, fmt.Errorf("admit first publication: read effective cover receipt: %w", err)
	}
	authoritativeCoverInput.PositionID = request.PositionID
	snapshot.CoverInput = clonePublicationAttemptCoverInput(authoritativeCoverInput)
	// Detach every mutable request component before handing it to service code.
	plannerRequest := request
	plannerRequest.firstAdmission = nil
	plannerRequest.reconciliationGuard = nil
	plannerRequest.FrozenInputs = append(json.RawMessage(nil), request.FrozenInputs...)
	plannerRequest.Tools = append([]SourceAnalysisToolSelection(nil), request.Tools...)
	plannerRequest.NamespaceManifest = append([]PublicationNamespaceNode(nil), request.NamespaceManifest...)
	if request.ExpectedGeneration != nil {
		generation := *request.ExpectedGeneration
		plannerRequest.ExpectedGeneration = &generation
	}
	if request.Source != nil {
		source := *request.Source
		plannerRequest.Source = &source
	}
	manifest, err := hook.prepare(ctx, snapshot, plannerRequest)
	if err != nil {
		return nil, fmt.Errorf("admit first publication: prepare native namespace: %w", err)
	}
	if err := validatePublicationManifest(manifest); err != nil {
		return nil, err
	}
	coverInput := authoritativeCoverInput
	hook.coverInput = &coverInput
	return manifest, nil
}

func validFirstPublicationPreviewDigest(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	for _, char := range digest {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') && (char < 'A' || char > 'F') {
			return false
		}
	}
	return true
}
