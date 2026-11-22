package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// matchingRecheckPublicationRepository is the read-only evidence a
// same-position Local-to-MusicBrainz recheck needs. It deliberately has no
// admission or mutation method: callers receive a frozen request and decide
// when to bind it to a durable owner.
type matchingRecheckPublicationRepository interface {
	ReadLibraryPublicationContext(context.Context, uuid.UUID, uuid.UUID) (*persistence.LibraryPublicationContext, error)
	ReadPublicationProviderTagContextForPosition(context.Context, uuid.UUID, uuid.UUID, string, string) (*persistence.PublicationProviderTagContext, error)
	GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error)
}

// PrepareMatchingRecheckPublication freezes a source-free provider-tag rewrite
// for one already-published position whose Local release is bound to an
// accepted MusicBrainz position. It is a read-only preparation: it neither
// admits an operation nor acknowledges reconciliation.
//
// The position and release identify the same local owner. The provider reader
// derives the MusicBrainz edge from the existing Local bindings for that exact
// position, so a cross-position publication cannot be prepared here. When the
// accepted provider projection changes no CURRENT tag, an exact no-op proof is
// returned instead of an admission; it is never nil, so an unproved no-op is
// impossible.
func (service *LibraryPublications) PrepareMatchingRecheckPublication(
	ctx context.Context, localReleaseID, positionID, currentGeneration uuid.UUID,
) (*persistence.MatchingRecheckPublicationPreparation, error) {
	if service == nil || service.repository == nil || service.settings == nil || service.commands == nil || service.coordinator == nil ||
		localReleaseID == uuid.Nil || positionID == uuid.Nil || currentGeneration == uuid.Nil {
		return nil, errors.New("matching recheck publication dependencies or identity are incomplete")
	}
	repository, ok := service.repository.(matchingRecheckPublicationRepository)
	if !ok {
		return nil, errors.New("matching recheck publication repository is unavailable")
	}
	configurationReader, ok := service.settings.(matchingConfigurationReader)
	if !ok {
		return nil, errors.New("matching recheck MusicBrainz configuration is unavailable")
	}
	planningReader, ok := service.settings.(firstPublicationFormatSettings)
	if !ok {
		return nil, errors.New("matching recheck publication settings are unavailable")
	}
	unlock, err := service.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
	if err != nil {
		return nil, fmt.Errorf("acquire matching recheck publication lock: %w", err)
	}
	defer unlock()

	snapshot, err := repository.ReadLibraryPublicationContext(ctx, localReleaseID, positionID)
	if err != nil {
		return nil, fmt.Errorf("read matching recheck CURRENT: %w", err)
	}
	if snapshot == nil || snapshot.ReleaseID != localReleaseID || snapshot.PositionID != positionID ||
		snapshot.ActiveAttempt != nil || snapshot.Current == nil ||
		snapshot.Current.Record.Generation != currentGeneration || snapshot.Current.Record.Epoch != snapshot.ActiveEpoch {
		return nil, persistence.ErrPublicationAdmissionConflict
	}
	configuration, err := configurationReader.GetMusicBrainzConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("read matching recheck MusicBrainz configuration: %w", err)
	}
	providerContext, err := repository.ReadPublicationProviderTagContextForPosition(ctx, localReleaseID, positionID, musicBrainzEndpoint(configuration), configuration.Identity)
	if err != nil {
		return nil, err
	}
	// The reader already scopes both keys by release-track id; repeat the owner
	// identity here so a future reader change cannot silently widen the scope.
	if providerContext == nil || providerContext.ReleaseOwner.ID != localReleaseID ||
		providerContext.PositionOwner.Owner.ID != positionID || providerContext.Source.ID == uuid.Nil || providerContext.Digest == "" {
		return nil, persistence.ErrPublicationAdmissionConflict
	}
	protection, err := persistence.BuildPublicationProviderTagProtection(providerContext)
	if err != nil {
		return nil, fmt.Errorf("build matching recheck protection: %w", err)
	}
	patch, err := persistence.BuildPublicationProviderCanonicalTagProjection(*providerContext)
	if err != nil {
		return nil, fmt.Errorf("build matching recheck provider patch: %w", err)
	}
	current := snapshot.Current
	desired, err := publication.ApplyProviderTagPatch(current.Metadata.Tags, patch.Tags, protection.Keys)
	if err != nil {
		return nil, fmt.Errorf("apply matching recheck provider patch: %w", err)
	}
	if reflect.DeepEqual(desired, current.Metadata.Tags) {
		return &persistence.MatchingRecheckPublicationPreparation{NoOp: &persistence.MatchingRecheckNoOpProof{
			PositionID: positionID, ExpectedReleaseID: localReleaseID, ExpectedEpoch: snapshot.ActiveEpoch,
			Current: persistence.PublicationCurrentWitness{
				Generation: current.Record.Generation, RelativePath: current.Record.RelativePath,
				Ownership: cloneFileIdentity(current.Ownership.Identity), AudioFacts: current.Audio.Facts,
				Assets: current.Assets.Clone(), Tags: cloneTagMap(current.Metadata.Tags),
			},
			ProviderSourceID:      providerContext.Source.ID,
			Endpoint:              providerContext.Source.Endpoint,
			ConfigurationIdentity: providerContext.Source.ConfigurationIdentity,
			AuthorityRevision:     providerContext.Digest,
			Patch:                 patch.Tags,
			Notices:               append([]string{}, patch.Notices...),
			ProtectedKeys:         protection.Keys,
		}}, nil
	}
	runtimeSettings, err := planningReader.ReadPublicationPlanningSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("read matching recheck planning settings: %w", err)
	}
	selections, err := service.selectedFFmpegPairForSettings(ctx, runtimeSettings)
	if err != nil {
		return nil, err
	}
	installationID, err := uuid.Parse(runtimeSettings.ActiveFFmpegInstallation)
	if err != nil || installationID == uuid.Nil {
		return nil, errors.New("active managed ffmpeg installation is unavailable")
	}
	installation, err := repository.GetInstallation(ctx, installationID)
	if err != nil {
		return nil, fmt.Errorf("read matching recheck ffmpeg installation: %w", err)
	}
	ffmpegPath, err := matchingRecheckFFmpegPath(runtimeSettings.ToolsDirectory, installation, installationID, service.platform)
	if err != nil {
		return nil, err
	}
	claimID := currentPublicationNamespaceClaim(snapshot, current.Record.RelativePath)
	if claimID == uuid.Nil {
		return nil, persistence.ErrPublicationAdmissionConflict
	}
	root, err := sourcefs.NewOutputOpener().OpenRoot(ctx, runtimeSettings.OutputDirectory)
	if err != nil {
		return nil, fmt.Errorf("open matching recheck output root: %w", err)
	}
	defer func() { _ = root.Close() }()
	before, err := captureObsoletePublicationOutput(ctx, root, runtimeSettings.OutputDirectory, current.Record, claimID)
	if err != nil {
		return nil, fmt.Errorf("capture matching recheck CURRENT: %w", err)
	}
	private, err := os.MkdirTemp(filepath.Dir(before.NativePath), ".publication-matching-recheck-")
	if err != nil {
		return nil, fmt.Errorf("create matching recheck private directory: %w", err)
	}
	commands := &matchingRecheckCommands{delegate: service.commands, quiescent: true}
	defer func() {
		if commands.quiescent {
			_ = os.RemoveAll(private)
		}
	}()
	privateCurrent := filepath.Join(private, filepath.Base(before.NativePath))
	if err := copyCurrentPublication(before.NativePath, privateCurrent); err != nil {
		return nil, err
	}
	copyDigest, err := matchingRecheckFileSHA256(privateCurrent)
	if err != nil {
		return nil, err
	}
	if copyDigest != current.Ownership.Identity.SHA256 {
		return nil, errors.New("matching recheck CURRENT copy does not match owned bytes")
	}
	request := metadata.ReadRequest{CoverInspection: true, LimitSourceCover: false}
	capture, err := (metadata.Reader{}).Read(ctx, privateCurrent, request)
	if err != nil {
		return nil, fmt.Errorf("read matching recheck CURRENT metadata: %w", err)
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		return nil, fmt.Errorf("observe matching recheck CURRENT assets: %w", err)
	}
	if !reflect.DeepEqual(capture.Tags, current.Metadata.Tags) || !reflect.DeepEqual(assets, current.Assets) {
		return nil, persistence.ErrPublicationAdmissionConflict
	}
	artwork, err := metadata.ReadArtworkSnapshot(ctx, privateCurrent)
	if err != nil {
		return nil, fmt.Errorf("read matching recheck CURRENT artwork: %w", err)
	}
	audioHash, err := publicationEncodedAudioSHA256(ctx, commands, ffmpegPath, privateCurrent, current.Audio.Facts.Container)
	if err != nil {
		return nil, err
	}
	if !commands.quiescent {
		return nil, errors.New("matching recheck command quiescence is unresolved")
	}
	input := persistence.PublicationInputs{
		Version:              persistence.PublicationProviderTagInputsVersion,
		QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind:           "rewrite",
		Format:               publication.AudioFormatSource,
		DesiredTags:          desired,
		Tools:                selections,
		Rewrite: &persistence.PublicationRewriteInput{
			CurrentGeneration: current.Record.Generation,
			RelativePath:      current.Record.RelativePath,
			Ownership:         cloneFileIdentity(current.Ownership.Identity),
			AudioFacts:        current.Audio.Facts,
		},
		ProviderTags: &persistence.PublicationProviderTagInput{
			Current: persistence.PublicationCurrentWitness{
				Generation:       current.Record.Generation,
				RelativePath:     current.Record.RelativePath,
				Ownership:        cloneFileIdentity(current.Ownership.Identity),
				AudioFacts:       current.Audio.Facts,
				Assets:           current.Assets.Clone(),
				Tags:             cloneTagMap(current.Metadata.Tags),
				ArtworkInventory: audioUpgradeArtworkWitnesses(artwork.ArtworkInventory()),
			},
			AudioPayloadSHA256:    audioHash,
			ProviderSourceID:      providerContext.Source.ID,
			Endpoint:              providerContext.Source.Endpoint,
			ConfigurationIdentity: providerContext.Source.ConfigurationIdentity,
			AuthorityRevision:     providerContext.Digest,
			Patch:                 patch.Tags,
			Notices:               append([]string{}, patch.Notices...),
			ProtectedKeys:         protection.Keys,
		},
	}
	if err := input.Validate(); err != nil {
		return nil, fmt.Errorf("validate matching recheck publication inputs: %w", err)
	}
	frozen, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode matching recheck publication inputs: %w", err)
	}
	nodes := make([]persistence.PublicationNamespaceNode, 0, len(snapshot.NamespaceManifest))
	for _, node := range snapshot.NamespaceManifest {
		if node.PositionID == nil || *node.PositionID != positionID || node.Generation == nil || *node.Generation != current.Record.Generation {
			continue
		}
		kind := node.Claim.NodeKind
		if kind == "directory" {
			kind = "dir"
		}
		nodes = append(nodes, persistence.PublicationNamespaceNode{
			CanonicalKey: node.Claim.CanonicalKey, RelativePath: node.Claim.RelativePath,
			NodeKind: kind, DirectoryScope: optionalString(node.Claim.DirectoryScopeKey),
		})
	}
	generation := current.Record.Generation
	return &persistence.MatchingRecheckPublicationPreparation{Admission: &persistence.PublicationAdmission{
		PositionID: positionID, ExpectedReleaseID: localReleaseID, ExpectedEpoch: snapshot.ActiveEpoch,
		ExpectedGeneration: &generation, CandidateGeneration: uuid.New(), IntentKind: "rewrite",
		ToolsReadRequired: true, Tools: selections, FrozenInputs: frozen, NamespaceManifest: nodes,
	}}, nil
}

// matchingRecheckFFmpegPath resolves the verified managed ffmpeg executable used
// only to hash the private CURRENT copy. The selection remains the frozen
// admission tool pair; this path is never persisted.
func matchingRecheckFFmpegPath(toolsRoot string, installation *persistence.ToolInstallation, installationID uuid.UUID, platform tools.Platform) (string, error) {
	if installation == nil || installation.ID != installationID || installation.PackageKind != string(tools.PackageFFmpeg) ||
		installation.State != "ready" || installation.VerifiedAt == nil || !platform.Supported() ||
		installation.PlatformGOOS != platform.GOOS || installation.PlatformGOARCH != platform.GOARCH ||
		toolsRoot == "" || !filepath.IsAbs(toolsRoot) || filepath.Clean(toolsRoot) != toolsRoot ||
		installation.RelativePath == "" || filepath.IsAbs(installation.RelativePath) {
		return "", errors.New("matching recheck ffmpeg installation is not verified for this platform")
	}
	directory := filepath.Join(toolsRoot, filepath.FromSlash(installation.RelativePath))
	if relative, err := filepath.Rel(toolsRoot, directory); err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("matching recheck ffmpeg installation path is outside the tools root")
	}
	if err := tools.HasSymlinkAncestors(toolsRoot, directory); err != nil {
		return "", fmt.Errorf("inspect matching recheck ffmpeg directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("matching recheck ffmpeg directory is not a managed directory")
	}
	path := filepath.Join(directory, publicationExecutableFileName("ffmpeg", platform.GOOS))
	info, err = os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("matching recheck ffmpeg executable is not a managed regular file")
	}
	return path, nil
}

type matchingRecheckCommands struct {
	delegate  PublicationCommandRunner
	quiescent bool
}

func (commands *matchingRecheckCommands) Run(ctx context.Context, executable string, args ...string) (PublicationCommandResult, error) {
	result, err := commands.delegate.Run(ctx, executable, args...)
	if !result.QuiescenceEstablished {
		commands.quiescent = false
	}
	return result, err
}

func matchingRecheckFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open matching recheck CURRENT copy: %w", err)
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return "", fmt.Errorf("hash matching recheck CURRENT copy: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
