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

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type providerTagAdmissionRepository interface {
	ReadLibraryPublicationContext(context.Context, uuid.UUID, uuid.UUID) (*persistence.LibraryPublicationContext, error)
	ReadPublicationProviderTagContextForPosition(context.Context, uuid.UUID, uuid.UUID, string, string) (*persistence.PublicationProviderTagContext, error)
	AcquirePublicationReconciliationTools(context.Context, persistence.PublicationReconciliationDeliveryGuard, []persistence.SourceAnalysisToolSelection) (persistence.PublicationReconciliationToolsSnapshot, bool, error)
	CheckPublicationReconciliationTools(context.Context, persistence.PublicationReconciliationDeliveryGuard, uuid.UUID) (bool, error)
	ReleasePublicationReconciliationTools(context.Context, persistence.PublicationReconciliationDeliveryGuard, uuid.UUID) (bool, error)
	AdmitPublicationProviderTagsFromReconciliation(context.Context, persistence.PublicationAdmission, persistence.PublicationReconciliationDeliveryGuard, persistence.RiverInserter) (uuid.UUID, error)
}

// admitAutomaticProviderTags returns a nil operation for an evaluated provider
// no-op. That alone does not acknowledge other reconciliation obligations.
func (service *LibraryPublications) admitAutomaticProviderTags(ctx context.Context, releaseID, positionID uuid.UUID, guard persistence.PublicationReconciliationDeliveryGuard) (operationID uuid.UUID, resultErr error) {
	if service == nil || service.coordinator == nil || service.commands == nil || service.river == nil {
		return uuid.Nil, errors.New("provider-tag admission dependencies are unavailable")
	}
	repository, ok := service.repository.(providerTagAdmissionRepository)
	if !ok {
		return uuid.Nil, errors.New("provider-tag admission repository is unavailable")
	}
	configurationReader, ok := service.settings.(matchingConfigurationReader)
	if !ok {
		return uuid.Nil, errors.New("provider-tag configuration reader is unavailable")
	}
	planningReader, ok := service.settings.(firstPublicationFormatSettings)
	if !ok {
		return uuid.Nil, errors.New("provider-tag publication settings are unavailable")
	}
	unlock, err := service.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
	if err != nil {
		return uuid.Nil, err
	}
	defer unlock()
	snapshot, err := repository.ReadLibraryPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("read provider-tag CURRENT: %w", err)
	}
	if snapshot == nil || snapshot.Current == nil || snapshot.ActiveAttempt != nil || snapshot.ActiveEpoch != guard.OutputEpoch || guard.PositionID != positionID || snapshot.Current.Record.Epoch != guard.OutputEpoch {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	configuration, err := configurationReader.GetMusicBrainzConfig(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("read provider-tag configuration: %w", err)
	}
	providerContext, err := repository.ReadPublicationProviderTagContextForPosition(ctx, releaseID, positionID, musicBrainzEndpoint(configuration), configuration.Identity)
	if err != nil {
		return uuid.Nil, err
	}
	protection, err := persistence.BuildPublicationProviderTagProtection(providerContext)
	if err != nil {
		return uuid.Nil, err
	}
	if len(protection.ManualAbsentFields) != 0 {
		return uuid.Nil, errors.New("manual-absent tag protection requires an owner decision")
	}
	patch, err := persistence.BuildPublicationProviderCanonicalTagPatch(*providerContext)
	if err != nil {
		return uuid.Nil, err
	}
	current := snapshot.Current
	desired, err := publication.ApplyProviderTagPatch(current.Metadata.Tags, patch, protection.Keys)
	if err != nil {
		return uuid.Nil, err
	}
	if reflect.DeepEqual(desired, current.Metadata.Tags) {
		return uuid.Nil, nil
	}
	runtime, err := planningReader.ReadPublicationPlanningSettings(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	selections, err := service.selectedFFmpegPairForSettings(ctx, runtime)
	if err != nil {
		return uuid.Nil, err
	}
	claimID := currentPublicationNamespaceClaim(snapshot, current.Record.RelativePath)
	if claimID == uuid.Nil {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	root, err := sourcefs.NewOutputOpener().OpenRoot(ctx, runtime.OutputDirectory)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	before, err := captureObsoletePublicationOutput(ctx, root, runtime.OutputDirectory, current.Record, claimID)
	if err != nil {
		return uuid.Nil, err
	}
	private, err := os.MkdirTemp(filepath.Dir(before.NativePath), ".publication-provider-capture-")
	if err != nil {
		return uuid.Nil, err
	}
	commands := &publicationProducerCommands{delegate: service.commands, quiescent: true}
	defer func() {
		if commands.quiescent {
			resultErr = errors.Join(resultErr, os.RemoveAll(private))
		}
	}()
	privateCurrent := filepath.Join(private, filepath.Base(before.NativePath))
	if err := copyCurrentPublication(before.NativePath, privateCurrent); err != nil {
		return uuid.Nil, err
	}
	copyDigest, err := privatePublicationSHA256(privateCurrent)
	if err != nil || copyDigest != current.Ownership.Identity.SHA256 {
		return uuid.Nil, errors.Join(err, errors.New("private provider-tag CURRENT does not match owned bytes"))
	}
	capture, err := (metadata.Reader{}).Read(ctx, privateCurrent, metadata.ReadRequest{CoverInspection: true, LimitSourceCover: false})
	if err != nil {
		return uuid.Nil, err
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil || !reflect.DeepEqual(capture.Tags, current.Metadata.Tags) || !reflect.DeepEqual(assets, current.Assets) {
		return uuid.Nil, errors.Join(err, errors.New("provider-tag CURRENT metadata or assets changed"))
	}
	artwork, err := metadata.ReadArtworkSnapshot(ctx, privateCurrent)
	if err != nil {
		return uuid.Nil, err
	}
	held, valid, err := repository.AcquirePublicationReconciliationTools(ctx, guard, selections)
	if err != nil || !valid || held.Installation == nil {
		return uuid.Nil, errors.Join(err, errors.New("provider-tag capture tool authority is unavailable"))
	}
	heldID := held.Installation.ID
	heldLive := true
	release := func() error {
		if !heldLive {
			return nil
		}
		if !commands.quiescent {
			return errors.New("provider-tag capture tool execution remains unresolved")
		}
		released, err := repository.ReleasePublicationReconciliationTools(context.WithoutCancel(ctx), guard, heldID)
		if err != nil || !released {
			return errors.Join(err, errors.New("provider-tag capture tool hold was not released"))
		}
		heldLive = false
		return nil
	}
	defer func() { resultErr = errors.Join(resultErr, release()) }()
	resolver := NewPublicationPinnedToolsResolver(nil, simplePublicationToolCommands{commands}, service.platform)
	pair, valid, err := resolver.resolveHeldSnapshot(ctx, held.Tools, held.Installation, held.Root, func(ctx context.Context) (bool, error) {
		return repository.CheckPublicationReconciliationTools(ctx, guard, heldID)
	})
	if err != nil || !valid {
		return uuid.Nil, errors.Join(err, errors.New("provider-tag capture tools failed verification"))
	}
	audioHash, err := publicationEncodedAudioSHA256(ctx, commands, pair.FFmpeg, privateCurrent, current.Audio.Facts.Container)
	if err != nil {
		return uuid.Nil, err
	}
	if err := release(); err != nil {
		return uuid.Nil, err
	}
	if _, err := captureObsoletePublicationOutput(ctx, root, runtime.OutputDirectory, current.Record, claimID); err != nil {
		return uuid.Nil, err
	}
	input := persistence.PublicationInputs{Version: persistence.PublicationProviderTagInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind: "rewrite", Format: publication.AudioFormatSource, DesiredTags: desired, Tools: selections,
		Rewrite: &persistence.PublicationRewriteInput{CurrentGeneration: current.Record.Generation, RelativePath: current.Record.RelativePath,
			Ownership: cloneFileIdentity(current.Ownership.Identity), AudioFacts: current.Audio.Facts},
		ProviderTags: &persistence.PublicationProviderTagInput{Current: persistence.PublicationCurrentWitness{Generation: current.Record.Generation,
			RelativePath: current.Record.RelativePath, Ownership: cloneFileIdentity(current.Ownership.Identity), AudioFacts: current.Audio.Facts,
			Assets: current.Assets.Clone(), Tags: cloneDesiredPublicationTags(current.Metadata.Tags), ArtworkInventory: audioUpgradeArtworkWitnesses(artwork.ArtworkInventory())},
			AudioPayloadSHA256: audioHash, ProviderSourceID: providerContext.Source.ID, Endpoint: providerContext.Source.Endpoint,
			ConfigurationIdentity: providerContext.Source.ConfigurationIdentity, AuthorityRevision: providerContext.Digest, Patch: patch, ProtectedKeys: protection.Keys},
	}
	if err := input.Validate(); err != nil {
		return uuid.Nil, err
	}
	frozen, err := json.Marshal(input)
	if err != nil {
		return uuid.Nil, err
	}
	nodes := make([]persistence.PublicationNamespaceNode, 0, len(snapshot.NamespaceManifest))
	for _, node := range snapshot.NamespaceManifest {
		if node.PositionID == nil || *node.PositionID != positionID || node.Generation == nil || *node.Generation != current.Record.Generation {
			continue
		}
		claim := node.Claim
		kind := claim.NodeKind
		if kind == "directory" {
			kind = "dir"
		}
		nodes = append(nodes, persistence.PublicationNamespaceNode{CanonicalKey: claim.CanonicalKey, RelativePath: claim.RelativePath, NodeKind: kind, DirectoryScope: optionalString(claim.DirectoryScopeKey)})
	}
	generation := current.Record.Generation
	return repository.AdmitPublicationProviderTagsFromReconciliation(ctx, persistence.PublicationAdmission{
		PositionID: positionID, ExpectedReleaseID: releaseID, ExpectedEpoch: guard.OutputEpoch,
		ExpectedGeneration: &generation, CandidateGeneration: uuid.New(), IntentKind: "rewrite", ToolsReadRequired: true,
		Tools: selections, FrozenInputs: frozen, NamespaceManifest: nodes,
	}, guard, service.river)
}

type publicationProducerCommands struct {
	delegate  PublicationCommandRunner
	quiescent bool
}

func (commands *publicationProducerCommands) Run(ctx context.Context, executable string, args ...string) (PublicationCommandResult, error) {
	result, err := commands.delegate.Run(ctx, executable, args...)
	if !result.QuiescenceEstablished {
		commands.quiescent = false
	}
	return result, err
}

func privatePublicationSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
