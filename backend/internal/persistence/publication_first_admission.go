package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

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
}

// PrepareFirstPublicationAdmission plans the final native namespace from the
// transactionally consistent snapshot. It must reject stale or mismatched
// preview/frozen inputs rather than silently replanning them.
type PrepareFirstPublicationAdmission func(context.Context, FirstPublicationAdmissionSnapshot, PublicationAdmission) ([]PublicationNamespaceNode, error)

type FirstPublicationAdmission struct {
	PublicationAdmission
	PreviewDigest string
	PrepareFirst  PrepareFirstPublicationAdmission
}

type firstPublicationAdmissionHook struct {
	digest  string
	prepare PrepareFirstPublicationAdmission
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
	request.firstAdmission = &firstPublicationAdmissionHook{digest: first.PreviewDigest, prepare: first.PrepareFirst}
	return repository.AdmitPublication(ctx, request, client)
}

func (repository *SetupManagerRepository) prepareFirstPublicationAdmission(ctx context.Context, tx bun.Tx, request PublicationAdmission) ([]PublicationNamespaceNode, error) {
	hook := request.firstAdmission
	if hook == nil || hook.prepare == nil {
		return nil, fmt.Errorf("admit first publication: missing planner")
	}
	tables := []string{
		"app_setting", "local_artist", "local_recording", "local_release", "local_track", "media_variant",
		"provider", "provider_artist", "provider_local_artist", "publication_namespace_claim", "publication_namespace_reservation", "release_artist", "release_track",
		"source_analysis_step", "source_analysis_work", "source_location", "source_root", "source_work_variant_identity",
		"tool_installation", "track_artist", "track_source",
	}
	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, "LOCK TABLE "+table+" IN SHARE MODE"); err != nil {
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
	// Detach every mutable request component before handing it to service code.
	plannerRequest := request
	plannerRequest.firstAdmission = nil
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
