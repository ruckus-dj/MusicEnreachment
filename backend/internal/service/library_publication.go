package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

var (
	ErrLibraryPublicationConflict    = errors.New("library publication state conflicts with the request")
	ErrLibraryPublicationUnavailable = errors.New("manual lyrics publication is unavailable for the current publication")
)

type libraryPublicationRepository interface {
	ReadLibraryPublicationContext(context.Context, uuid.UUID, uuid.UUID) (*persistence.LibraryPublicationContext, error)
	GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error)
	AdmitManualLyricsRewrite(context.Context, persistence.ManualLyricsAdmission, persistence.RiverInserter) (uuid.UUID, error)
}

type libraryPublicationSettings interface {
	ReadRuntimeSettings(context.Context) (settings.RuntimeSettings, error)
}

// LibraryPublicationManualLyrics contains only the desired manual value and
// its receipt status. It intentionally does not expose publication internals.
type LibraryPublicationManualLyrics struct {
	State   string `json:"state"`
	Value   string `json:"value,omitempty"`
	Pending bool   `json:"pending"`
}

// LibraryPublicationManualLyricsReceipt exposes bounded confirmation facts,
// independently of whether manual lyrics remain saved as desired metadata.
type LibraryPublicationManualLyricsReceipt struct {
	Epoch               uuid.UUID `json:"epoch"`
	DesiredRevision     int64     `json:"desired_revision"`
	ConfirmedGeneration uuid.UUID `json:"confirmed_generation"`
	PublishedGeneration uuid.UUID `json:"published_generation"`
	DesiredState        string    `json:"desired_state"`
	DesiredSHA256       *string   `json:"desired_sha256,omitempty"`
	WarningCode         *string   `json:"warning_code,omitempty"`
	RecordedAt          time.Time `json:"recorded_at"`
}

// LibraryPublicationInitialLyrics is the bounded status of optional lyrics
// resolved for the current publication generation.
type LibraryPublicationInitialLyrics struct {
	Source                string `json:"source"`
	Outcome               string `json:"outcome"`
	ResolutionWarningCode string `json:"resolution_warning_code,omitempty"`
	OutcomeWarningCode    string `json:"outcome_warning_code,omitempty"`
}

// LibraryPublicationView is a bounded read projection for a library position.
type LibraryPublicationView struct {
	ReleaseID                   uuid.UUID                                `json:"release_id"`
	PositionID                  uuid.UUID                                `json:"position_id"`
	ActiveEpoch                 uuid.UUID                                `json:"active_epoch"`
	MetadataRevision            int64                                    `json:"metadata_revision"`
	CurrentGeneration           *uuid.UUID                               `json:"current_generation,omitempty"`
	CurrentEpoch                *uuid.UUID                               `json:"current_epoch,omitempty"`
	CurrentPath                 string                                   `json:"current_path,omitempty"`
	CurrentOwnership            *persistence.PublicationFileIdentity     `json:"current_ownership,omitempty"`
	CurrentAudio                *persistence.PersistedAudioFacts         `json:"current_audio,omitempty"`
	CurrentMetadata             *persistence.PublicationCommitMetadata   `json:"current_metadata,omitempty"`
	CurrentAssets               *persistence.PublicationAssetOutcomes    `json:"current_assets,omitempty"`
	CurrentInitialLyrics        *LibraryPublicationInitialLyrics         `json:"initial_lyrics,omitempty"`
	CurrentCoverEmbedding       *LibraryPublicationCoverEmbedding        `json:"cover_embedding,omitempty"`
	CurrentQualityPolicyVersion *int                                     `json:"current_quality_policy_version,omitempty"`
	CurrentValidatedAt          *time.Time                               `json:"current_validated_at,omitempty"`
	ActiveAttempt               *persistence.LibraryPublicationOperation `json:"active_attempt,omitempty"`
	ActiveAttemptCandidate      *uuid.UUID                               `json:"active_attempt_candidate,omitempty"`
	ActiveAttemptPhase          *string                                  `json:"active_attempt_phase,omitempty"`
	ManualLyrics                *LibraryPublicationManualLyrics          `json:"manual_lyrics,omitempty"`
	ManualLyricsReceipt         *LibraryPublicationManualLyricsReceipt   `json:"manual_lyrics_receipt,omitempty"`
	CanApplyManualLyrics        bool                                     `json:"can_apply_manual_lyrics"`
	SourceCleanupWarnings       []LibraryPublicationSourceCleanupWarning `json:"source_cleanup_warnings,omitempty"`
}

// LibraryPublicationCoverEmbedding exposes only the current cover outcome and
// its bounded warning code, never the frozen receipt or image data.
type LibraryPublicationCoverEmbedding struct {
	Outcome     string `json:"outcome"`
	WarningCode string `json:"warning_code,omitempty"`
}

type LibraryPublicationSourceCleanupWarning struct {
	OriginalOperationID    uuid.UUID  `json:"original_operation_id"`
	SafeError              string     `json:"safe_error"`
	CanRetry               bool       `json:"can_retry"`
	ActiveRetryOperationID *uuid.UUID `json:"active_retry_operation_id,omitempty"`
}

type LibraryPublicationApplyManualLyricsRequest struct {
	ExpectedEpoch            uuid.UUID
	ExpectedGeneration       uuid.UUID
	ExpectedMetadataRevision int64
}

type LibraryPublications struct {
	repository       libraryPublicationRepository
	settings         libraryPublicationSettings
	river            persistence.RiverInserter
	platform         tools.Platform
	resolveNamespace func(context.Context, *persistence.LibraryFirstPublicationContext, string, string, *FirstPublicationPreview) ([]persistence.PublicationNamespaceNode, error)
}

func NewLibraryPublications(repository *persistence.SetupManagerRepository, runtime *settings.Registry, client persistence.RiverInserter, platform tools.Platform) *LibraryPublications {
	service := newLibraryPublications(repository, runtime, client, platform)
	service.resolveNamespace = finalizeFirstPublicationNamespace
	return service
}

func newLibraryPublications(repository libraryPublicationRepository, runtime libraryPublicationSettings, client persistence.RiverInserter, platform tools.Platform) *LibraryPublications {
	return &LibraryPublications{repository: repository, settings: runtime, river: client, platform: platform}
}

func (service *LibraryPublications) Read(ctx context.Context, releaseID, positionID uuid.UUID) (LibraryPublicationView, error) {
	if service == nil || service.repository == nil {
		return LibraryPublicationView{}, errors.New("read library publication: repository is unavailable")
	}
	if releaseID == uuid.Nil || positionID == uuid.Nil {
		return LibraryPublicationView{}, fmt.Errorf("read library publication: %w", ErrLibraryPublicationConflict)
	}
	projection, err := service.repository.ReadLibraryPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		return LibraryPublicationView{}, fmt.Errorf("read library publication: %w", err)
	}
	view, _, _ := libraryPublicationView(projection)
	for _, warning := range projection.SourceCleanupWarnings {
		view.SourceCleanupWarnings = append(view.SourceCleanupWarnings, LibraryPublicationSourceCleanupWarning{OriginalOperationID: warning.OriginalOperationID, SafeError: warning.SafeError, CanRetry: warning.CanRetry, ActiveRetryOperationID: warning.ActiveRetryOperationID})
	}
	return view, nil
}

func (service *LibraryPublications) ApplyManualLyrics(ctx context.Context, releaseID, positionID uuid.UUID, request LibraryPublicationApplyManualLyricsRequest) (uuid.UUID, error) {
	if service == nil || service.repository == nil || service.settings == nil || service.river == nil {
		return uuid.Nil, errors.New("apply manual lyrics: required dependencies are unavailable")
	}
	if releaseID == uuid.Nil || positionID == uuid.Nil || request.ExpectedEpoch == uuid.Nil || request.ExpectedGeneration == uuid.Nil || request.ExpectedMetadataRevision < 0 {
		return uuid.Nil, fmt.Errorf("apply manual lyrics: %w", ErrLibraryPublicationConflict)
	}
	projection, err := service.repository.ReadLibraryPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("read library publication for manual lyrics: %w", err)
	}
	view, field, eligible := libraryPublicationView(projection)
	if !eligible || view.ReleaseID != releaseID || view.PositionID != positionID ||
		projection.ActiveEpoch != request.ExpectedEpoch || projection.Metadata.Revision != request.ExpectedMetadataRevision ||
		projection.Current == nil || projection.Current.Record.Generation != request.ExpectedGeneration ||
		projection.Current.Record.Epoch != request.ExpectedEpoch || projection.ActiveEpoch != projection.Current.Record.Epoch {
		return uuid.Nil, fmt.Errorf("apply manual lyrics: %w", ErrLibraryPublicationConflict)
	}
	if projection.ActiveAttempt != nil {
		return uuid.Nil, fmt.Errorf("apply manual lyrics: %w", ErrLibraryPublicationConflict)
	}
	lyrics, state, err := desiredManualLyrics(field)
	if err != nil {
		return uuid.Nil, fmt.Errorf("apply manual lyrics: %w", err)
	}
	if !projection.ManualLyrics.Pending {
		return uuid.Nil, fmt.Errorf("apply manual lyrics is already current: %w", ErrLibraryPublicationConflict)
	}
	if !manualLyricsCurrentSupported(projection.Current) {
		return uuid.Nil, fmt.Errorf("apply manual lyrics: %w", ErrLibraryPublicationUnavailable)
	}
	selections, err := service.selectedFFmpegPair(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("select managed publication tools: %w", err)
	}
	inputs := buildManualLyricsPublicationInputs(projection, lyrics, state, selections)
	if err := inputs.Validate(); err != nil {
		return uuid.Nil, fmt.Errorf("build manual lyrics publication inputs: %w", err)
	}
	frozen, err := json.Marshal(inputs)
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode manual lyrics publication inputs: %w", err)
	}
	current := projection.Current
	generation := current.Record.Generation
	manifest := make([]persistence.PublicationNamespaceNode, 0, len(projection.NamespaceManifest))
	for _, node := range projection.NamespaceManifest {
		manifest = append(manifest, persistence.PublicationNamespaceNode{
			CanonicalKey:   node.Claim.CanonicalKey,
			RelativePath:   node.Claim.RelativePath,
			NodeKind:       node.Claim.NodeKind,
			DirectoryScope: optionalString(node.Claim.DirectoryScopeKey),
		})
	}
	operationID, err := service.repository.AdmitManualLyricsRewrite(ctx, persistence.ManualLyricsAdmission{
		PublicationAdmission: persistence.PublicationAdmission{
			PositionID: positionID, ExpectedEpoch: request.ExpectedEpoch, ExpectedGeneration: &generation,
			CandidateGeneration: uuid.New(), IntentKind: "rewrite", ToolsReadRequired: true,
			Tools: selections, FrozenInputs: frozen, NamespaceManifest: manifest,
		},
		ExpectedReleaseID: releaseID, ExpectedMetadataRevision: request.ExpectedMetadataRevision,
	}, service.river)
	if err != nil {
		if errors.Is(err, persistence.ErrPublicationAdmissionConflict) {
			return uuid.Nil, fmt.Errorf("admit manual lyrics rewrite: %w", ErrLibraryPublicationConflict)
		}
		return uuid.Nil, fmt.Errorf("admit manual lyrics rewrite: %w", err)
	}
	return operationID, nil
}

func libraryPublicationView(projection *persistence.LibraryPublicationContext) (LibraryPublicationView, librarymetadata.FieldValue, bool) {
	if projection == nil {
		return LibraryPublicationView{}, librarymetadata.FieldValue{}, false
	}
	view := LibraryPublicationView{
		ReleaseID: projection.ReleaseID, PositionID: projection.PositionID,
		ActiveEpoch: projection.ActiveEpoch, MetadataRevision: projection.Metadata.Revision,
	}
	field, hasField := projection.Metadata.Envelope.Desired["synced_lyrics"]
	if projection.Current != nil {
		current := projection.Current
		generation, epoch := current.Record.Generation, current.Record.Epoch
		view.CurrentGeneration, view.CurrentEpoch = &generation, &epoch
		view.CurrentPath = current.Record.RelativePath
		identity := current.Ownership.Identity
		view.CurrentOwnership = &identity
		facts := current.Audio.Facts
		view.CurrentAudio = &facts
		metadata := persistence.PublicationCommitMetadata{Version: current.Metadata.Version, Tags: make(map[string][]string, len(current.Metadata.Tags))}
		for key, values := range current.Metadata.Tags {
			metadata.Tags[key] = append([]string(nil), values...)
		}
		view.CurrentMetadata = &metadata
		assets := current.Assets.Clone()
		view.CurrentAssets = &assets
		if initialLyrics := current.InitialLyrics; initialLyrics != nil && initialLyrics.Generation == current.Record.Generation {
			view.CurrentInitialLyrics = &LibraryPublicationInitialLyrics{
				Source: initialLyrics.Source, Outcome: initialLyrics.Outcome,
				ResolutionWarningCode: initialLyrics.ResolutionWarningCode,
				OutcomeWarningCode:    initialLyrics.OutcomeWarningCode,
			}
		}
		if embedding := current.CoverEmbedding; embedding != nil && embedding.Generation == current.Record.Generation {
			view.CurrentCoverEmbedding = &LibraryPublicationCoverEmbedding{Outcome: embedding.Outcome, WarningCode: embedding.WarningCode}
		}
		qualityPolicyVersion, validatedAt := current.Record.QualityPolicyVersion, current.Record.ValidatedAt
		view.CurrentQualityPolicyVersion = &qualityPolicyVersion
		view.CurrentValidatedAt = &validatedAt
	}
	if projection.ActiveAttempt != nil {
		attempt := *projection.ActiveAttempt
		view.ActiveAttempt = &attempt
	}
	if projection.AttemptCandidate != nil {
		candidate := *projection.AttemptCandidate
		view.ActiveAttemptCandidate = &candidate
	}
	if projection.AttemptPhase != nil {
		phase := *projection.AttemptPhase
		view.ActiveAttemptPhase = &phase
	}
	if receipt := projection.ManualLyrics.Receipt; receipt != nil {
		view.ManualLyricsReceipt = &LibraryPublicationManualLyricsReceipt{
			Epoch: receipt.Epoch, DesiredRevision: receipt.DesiredRevision,
			ConfirmedGeneration: receipt.ConfirmedGeneration, PublishedGeneration: receipt.PublishedGeneration,
			DesiredState: string(receipt.DesiredState), DesiredSHA256: clonePublicationString(receipt.DesiredSHA256),
			WarningCode: clonePublicationString(receipt.WarningCode), RecordedAt: receipt.RecordedAt,
		}
	}
	if hasField && isManualLyricsField(field) {
		value, state, err := desiredManualLyrics(field)
		if err == nil {
			view.ManualLyrics = &LibraryPublicationManualLyrics{State: state, Value: value, Pending: projection.ManualLyrics.Pending}
		}
	}
	view.CanApplyManualLyrics = hasField && view.ManualLyrics != nil && view.CurrentGeneration != nil &&
		projection.ActiveAttempt == nil && projection.ManualLyrics.Pending &&
		projection.ActiveEpoch == projection.Current.Record.Epoch && manualLyricsCurrentSupported(projection.Current)
	return view, field, view.ManualLyrics != nil
}

func clonePublicationString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func isManualLyricsField(field librarymetadata.FieldValue) bool {
	return field.Authority == librarymetadata.AuthorityManual && field.Provenance.Kind == librarymetadata.ProvenanceManual &&
		(field.State == librarymetadata.StateValue || field.State == librarymetadata.StateExplicitEmpty)
}

func desiredManualLyrics(field librarymetadata.FieldValue) (string, string, error) {
	if !isManualLyricsField(field) {
		return "", "", errors.New("synced_lyrics must be an explicit manual value or explicit empty")
	}
	if field.State == librarymetadata.StateExplicitEmpty {
		return "", "explicit_empty", nil
	}
	value, ok := field.Value.(string)
	if !ok || value == "" {
		return "", "", errors.New("synced_lyrics must be non-empty valid synced LRC")
	}
	validated, err := lrclib.ValidateSyncedLyrics(value)
	if err != nil || validated != value {
		return "", "", errors.New("synced_lyrics must be non-empty valid synced LRC")
	}
	return value, "value", nil
}

func manualLyricsCurrentSupported(current *persistence.LibraryPublicationCurrent) bool {
	if current == nil || current.Record.Generation == uuid.Nil || current.Record.Epoch == uuid.Nil ||
		current.Record.QualityPolicyVersion != publication.QualityPolicyVersion ||
		!manualLyricsAudioSupported(current.Audio.Facts) {
		return false
	}
	identity := current.Ownership.Identity
	if identity.SHA256 == "" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(identity.SHA256) {
		return false
	}
	if current.Audio.Facts.Container == publication.AudioContainerMKA && current.Assets.Cover.State != persistence.PublicationAssetAbsent {
		return false
	}
	return true
}

func manualLyricsAudioSupported(facts persistence.PersistedAudioFacts) bool {
	if !facts.SupportedSourceContainer {
		return false
	}
	switch {
	case facts.Container == publication.AudioContainerFLAC && facts.Codec == "flac":
		return true
	case facts.Container == publication.AudioContainerMKA:
		return facts.Codec == "mp3"
	default:
		return false
	}
}

func buildManualLyricsPublicationInputs(projection *persistence.LibraryPublicationContext, value, state string, tools []persistence.SourceAnalysisToolSelection) persistence.PublicationInputs {
	current := projection.Current
	mandatory := make(map[string][]string, len(current.Metadata.Tags))
	for name, values := range current.Metadata.Tags {
		if strings.EqualFold(strings.TrimSpace(name), "LYRICS") {
			continue
		}
		mandatory[name] = append(make([]string, 0, len(values)), values...)
	}
	return persistence.PublicationInputs{
		Version:              persistence.PublicationInputsVersion,
		QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind:           "rewrite",
		Rewrite: &persistence.PublicationRewriteInput{
			CurrentGeneration: current.Record.Generation,
			RelativePath:      current.Record.RelativePath,
			Ownership:         current.Ownership.Identity,
			AudioFacts:        current.Audio.Facts,
		},
		ManualLyrics: &persistence.PublicationManualLyricsInput{
			ExpectedMetadataRevision:   projection.Metadata.Revision,
			ConfirmedCurrentGeneration: current.Record.Generation,
			State:                      state, Value: value,
		},
		Format:      publication.AudioFormatSource,
		DesiredTags: mandatory,
		Tools:       append([]persistence.SourceAnalysisToolSelection(nil), tools...),
	}
}

func (service *LibraryPublications) selectedFFmpegPair(ctx context.Context) ([]persistence.SourceAnalysisToolSelection, error) {
	runtimeSettings, err := service.settings.ReadRuntimeSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("read active ffmpeg installation: %w", err)
	}
	return service.selectedFFmpegPairForSettings(ctx, runtimeSettings)
}

func (service *LibraryPublications) selectedFFmpegPairForSettings(ctx context.Context, runtimeSettings settings.RuntimeSettings) ([]persistence.SourceAnalysisToolSelection, error) {
	id, err := uuid.Parse(runtimeSettings.ActiveFFmpegInstallation)
	if err != nil || id == uuid.Nil {
		return nil, errors.New("active managed ffmpeg installation is unavailable")
	}
	installation, err := service.repository.GetInstallation(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("read active ffmpeg installation: %w", err)
	}
	if installation == nil || installation.ID != id {
		return nil, errors.New("active managed ffmpeg installation is not verified for this platform")
	}
	return selectedFFmpegPairFromInstallation(installation, service.platform)
}

func selectedFFmpegPairFromInstallation(installation *persistence.ToolInstallation, platform tools.Platform) ([]persistence.SourceAnalysisToolSelection, error) {
	if installation == nil || installation.PackageKind != string(tools.PackageFFmpeg) || installation.State != "ready" ||
		installation.VerifiedAt == nil || installation.PlatformGOOS != platform.GOOS || installation.PlatformGOARCH != platform.GOARCH || !platform.Supported() {
		return nil, errors.New("active managed ffmpeg installation is not verified for this platform")
	}
	var versions map[string]string
	if err := json.Unmarshal(installation.ExecutableVersions, &versions); err != nil {
		return nil, errors.New("active managed ffmpeg installation has invalid verified versions")
	}
	selections := make([]persistence.SourceAnalysisToolSelection, 0, 2)
	for _, executable := range []string{"ffmpeg", "ffprobe"} {
		banner, ok := tools.VerifiedExecutableVersion(versions, tools.PackageFFmpeg, executable, installation.PlatformGOOS)
		if !ok {
			return nil, fmt.Errorf("active managed %s executable has no verified version", executable)
		}
		line, _, _ := strings.Cut(banner, "\n")
		parts := strings.Fields(line)
		if len(parts) < 3 || parts[0] != executable || parts[1] != "version" {
			return nil, fmt.Errorf("active managed %s executable has an invalid verified version", executable)
		}
		selections = append(selections, persistence.SourceAnalysisToolSelection{
			PackageKind: string(tools.PackageFFmpeg), InstallationID: installation.ID,
			RelativePath: filepath.ToSlash(installation.RelativePath), Executable: executable,
			Version: parts[2], VersionBanner: banner,
		})
	}
	return selections, nil
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
