package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"strings"

	"github.com/google/uuid"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type automaticAudioUpgradeAdmissionRepository interface {
	firstPublicationContextReader
	ReadPositionSourceAuthority(context.Context, uuid.UUID) (*persistence.PositionSourceAuthority, error)
	AdmitAutomaticAudioUpgradeFromReconciliation(context.Context, persistence.PublicationAdmission, persistence.PublicationReconciliationDeliveryGuard, persistence.RiverInserter) (uuid.UUID, error)
}

// admitAutomaticAudioUpgrade freezes a source-authorized audio improvement
// while excluding all in-process publication mutations. Reconciliation remains
// responsible for atomically acknowledging this producer's delivery.
func (service *LibraryPublications) admitAutomaticAudioUpgrade(
	ctx context.Context,
	releaseID, positionID uuid.UUID,
	guard persistence.PublicationReconciliationDeliveryGuard,
) (uuid.UUID, error) {
	if service == nil || service.repository == nil || service.settings == nil || service.river == nil ||
		service.coordinator == nil || releaseID == uuid.Nil || positionID == uuid.Nil ||
		guard.PositionID != positionID || guard.OutputEpoch == uuid.Nil {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	repository, ok := service.repository.(automaticAudioUpgradeAdmissionRepository)
	if !ok {
		return uuid.Nil, errors.New("automatic audio-upgrade admission is unavailable")
	}
	planningSettings, ok := service.settings.(firstPublicationFormatSettings)
	if !ok {
		return uuid.Nil, errors.New("automatic audio-upgrade settings are unavailable")
	}
	unlock, err := service.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
	if err != nil {
		return uuid.Nil, fmt.Errorf("acquire automatic audio-upgrade publication lock: %w", err)
	}
	defer unlock()

	firstSnapshot, err := repository.ReadLibraryFirstPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("read automatic audio-upgrade sources: %w", err)
	}
	if firstSnapshot == nil || firstSnapshot.Library == nil || firstSnapshot.Library.Current == nil ||
		firstSnapshot.Library.ActiveEpoch != guard.OutputEpoch || firstSnapshot.Library.ActiveAttempt != nil ||
		firstSnapshot.Library.Current.Record.Epoch != guard.OutputEpoch || firstSnapshot.Library.ReleaseID != releaseID || firstSnapshot.Library.PositionID != positionID {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	currentSnapshot := firstSnapshot.Library
	current := currentSnapshot.Current
	authority, err := repository.ReadPositionSourceAuthority(ctx, positionID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("read automatic audio-upgrade source authority: %w", err)
	}
	if authority == nil || authority.Mode != "automatic" || authority.Revision == uuid.Nil ||
		authority.PublicationEpoch != current.Record.Epoch || authority.PublicationGeneration != current.Record.Generation {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	runtimeSettings, err := planningSettings.ReadPublicationPlanningSettings(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("read automatic audio-upgrade planning settings: %w", err)
	}
	format := publication.AudioPublicationFormat(runtimeSettings.PublicationFormat)
	choice, err := automaticAudioUpgradeSourceChoice(firstSnapshot, current.Audio.Facts, format)
	if err != nil {
		return uuid.Nil, err
	}
	var selected *persistence.FirstPublicationSource
	for i := range firstSnapshot.Sources {
		source := &firstSnapshot.Sources[i]
		if source.Selection.LocationID == choice.LocationID && source.Selection.MediaVariantID == choice.MediaVariantID && source.Quality.StreamIndex == choice.StreamIndex {
			selected = source
			break
		}
	}
	if selected == nil {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	plan, err := publication.PlanAudioTransform(format, selected.Quality.AudioSourceFacts())
	if err != nil {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	targetRelative, err := audioUpgradeTargetRelativePath(current.Record.RelativePath, format, selected.Quality.AudioSourceFacts())
	if err != nil {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	if path.Dir(targetRelative) != path.Dir(current.Record.RelativePath) ||
		path.Ext(targetRelative) != "."+plan.Extension ||
		strings.TrimSuffix(path.Base(targetRelative), path.Ext(targetRelative)) != strings.TrimSuffix(path.Base(current.Record.RelativePath), path.Ext(current.Record.RelativePath)) {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	selectedTools, err := service.selectedFFmpegPairForSettings(ctx, runtimeSettings)
	if err != nil {
		return uuid.Nil, fmt.Errorf("select verified automatic audio-upgrade tools: %w", err)
	}

	claimID := currentPublicationNamespaceClaim(currentSnapshot, current.Record.RelativePath)
	if claimID == uuid.Nil {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	root, err := sourcefs.NewOutputOpener().OpenRoot(ctx, runtimeSettings.OutputDirectory)
	if err != nil {
		return uuid.Nil, fmt.Errorf("open automatic audio-upgrade output root: %w", err)
	}
	defer func() { _ = root.Close() }()
	before, err := captureObsoletePublicationOutput(ctx, root, runtimeSettings.OutputDirectory, current.Record, claimID)
	if err != nil {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	observed, err := (metadata.Reader{}).Read(ctx, before.NativePath, metadata.ReadRequest{CoverInspection: true, LimitSourceCover: false})
	if err != nil {
		return uuid.Nil, fmt.Errorf("inspect automatic audio-upgrade CURRENT metadata: %w", err)
	}
	artwork, err := metadata.ReadArtworkSnapshot(ctx, before.NativePath)
	if err != nil {
		return uuid.Nil, fmt.Errorf("inspect automatic audio-upgrade CURRENT artwork: %w", err)
	}
	observedAssets, err := ObservePublicationAssets(observed)
	if err != nil || !reflect.DeepEqual(observed.Tags, current.Metadata.Tags) || !reflect.DeepEqual(observedAssets, current.Assets) {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	after, err := captureObsoletePublicationOutput(ctx, root, runtimeSettings.OutputDirectory, current.Record, claimID)
	if err != nil || before.FileSize != after.FileSize || before.ModifiedAt != after.ModifiedAt || !reflect.DeepEqual(before.SHA256, after.SHA256) || before.NativePath != after.NativePath {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}

	manifest, err := automaticAudioUpgradeNamespace(ctx, runtimeSettings.OutputDirectory, positionID, targetRelative, firstSnapshot.NamespaceClaims, currentSnapshot.NamespaceManifest)
	if err != nil {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	input := persistence.PublicationInputs{
		Version:              persistence.PublicationAudioUpgradeInputsVersion,
		QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind:           "audio", SourceAuthorityMode: "automatic", Format: format,
		Audio: audioInputCopy(&selected.Input),
		AudioUpgrade: &persistence.PublicationAudioUpgradeInput{
			Current: persistence.PublicationCurrentWitness{
				Generation: current.Record.Generation, RelativePath: current.Record.RelativePath,
				Ownership: cloneFileIdentity(current.Ownership.Identity), AudioFacts: current.Audio.Facts,
				Assets: current.Assets.Clone(), Tags: cloneDesiredPublicationTags(current.Metadata.Tags),
				ArtworkInventory: audioUpgradeArtworkWitnesses(artwork.ArtworkInventory()),
			},
			SourceAuthorityRevision: authority.Revision,
		},
		DesiredTags: cloneDesiredPublicationTags(current.Metadata.Tags),
		Tools:       append([]persistence.SourceAnalysisToolSelection(nil), selectedTools...),
	}
	if err := input.Validate(); err != nil {
		return uuid.Nil, fmt.Errorf("validate automatic audio-upgrade inputs: %w", err)
	}
	frozen, err := json.Marshal(input)
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode automatic audio-upgrade inputs: %w", err)
	}
	holdID, err := uuid.NewRandom()
	if err != nil {
		return uuid.Nil, fmt.Errorf("create automatic audio-upgrade source hold: %w", err)
	}
	generation := current.Record.Generation
	operationID, err := repository.AdmitAutomaticAudioUpgradeFromReconciliation(ctx, persistence.PublicationAdmission{
		PositionID: positionID, ExpectedReleaseID: releaseID, ExpectedEpoch: guard.OutputEpoch,
		ExpectedGeneration: &generation, CandidateGeneration: uuid.New(), IntentKind: "audio",
		ToolsReadRequired: true, Tools: selectedTools, FrozenInputs: frozen, NamespaceManifest: manifest,
		Source: &persistence.PublicationSourceSelection{
			RootID: selected.Input.RootID, LocationID: selected.Input.LocationID, WorkID: selected.Input.WorkID,
			MediaVariantID: selected.Input.MediaVariantID, ConfiguredPath: selected.Input.ConfiguredRootPath,
			InventoryPath: selected.Input.InventoryRootPath, RelativePath: selected.Input.RelativePath,
			SizeBytes: selected.Input.SizeBytes, MTime: selected.Input.MTime, HoldUUID: holdID,
		},
	}, guard, service.river)
	if err != nil {
		return uuid.Nil, fmt.Errorf("admit automatic audio upgrade: %w", err)
	}
	return operationID, nil
}

func currentPublicationNamespaceClaim(snapshot *persistence.LibraryPublicationContext, relativePath string) uuid.UUID {
	if snapshot == nil || snapshot.Current == nil {
		return uuid.Nil
	}
	for _, node := range snapshot.NamespaceManifest {
		claim := node.Claim
		if claim.RelativePath == relativePath && claim.NodeKind == "file" &&
			claim.OwnerPositionID != nil && *claim.OwnerPositionID == snapshot.PositionID &&
			node.PositionID != nil && *node.PositionID == snapshot.PositionID &&
			node.Generation != nil && *node.Generation == snapshot.Current.Record.Generation {
			return claim.ID
		}
	}
	return uuid.Nil
}

func audioUpgradeArtworkWitnesses(entries []metadata.ArtworkInventoryEntry) []persistence.PublicationArtworkWitness {
	witnesses := make([]persistence.PublicationArtworkWitness, len(entries))
	for index, entry := range entries {
		witnesses[index] = persistence.PublicationArtworkWitness{
			Index: entry.Index, Type: entry.Type, Description: entry.Description, MIME: entry.MIME,
			Size: int64(entry.Size), SHA256: hex.EncodeToString(entry.SHA256[:]),
		}
	}
	return witnesses
}

func cloneFileIdentity(identity persistence.PublicationFileIdentity) persistence.PublicationFileIdentity {
	if identity.SizeBytes != nil {
		size := *identity.SizeBytes
		identity.SizeBytes = &size
	}
	if identity.MTime != nil {
		mtime := *identity.MTime
		identity.MTime = &mtime
	}
	return identity
}

func automaticAudioUpgradeNamespace(
	ctx context.Context,
	outputRoot string,
	positionID uuid.UUID,
	targetRelative string,
	claims []persistence.PublicationNamespaceClaim,
	_ []persistence.LibraryPublicationNamespaceNode,
) ([]persistence.PublicationNamespaceNode, error) {
	if outputRoot == "" || positionID == uuid.Nil || targetRelative == "" {
		return nil, persistence.ErrPublicationAdmissionConflict
	}
	components := strings.Split(targetRelative, "/")
	candidates := make([]publication.NamespaceCandidate, 0, len(components))
	for index := 1; index < len(components); index++ {
		directory := strings.Join(components[:index], "/")
		var scope string
		found := false
		for _, claim := range claims {
			kind := claim.NodeKind
			if kind == "dir" {
				kind = "directory"
			}
			if claim.RelativePath != directory || kind != "directory" || claim.DirectoryScopeKey == nil || *claim.DirectoryScopeKey == "" {
				continue
			}
			scope, found = *claim.DirectoryScopeKey, true
			break
		}
		if !found {
			return nil, persistence.ErrPublicationAdmissionConflict
		}
		candidates = append(candidates, publication.NamespaceCandidate{
			RelativePath: directory, NodeKind: "directory", DirectoryScope: scope, PositionID: positionID,
		})
	}
	candidates = append(candidates, publication.NamespaceCandidate{RelativePath: targetRelative, NodeKind: "file", PositionID: positionID})
	manifest, err := planExactTargetPublicationNamespace(ctx, outputRoot, positionID, candidates, claims)
	if err != nil {
		return nil, err
	}
	result := make([]persistence.PublicationNamespaceNode, 0, len(manifest))
	for _, node := range manifest {
		kind := node.NodeKind
		if kind == "directory" {
			kind = "dir"
		}
		result = append(result, persistence.PublicationNamespaceNode{
			CanonicalKey: node.CanonicalKey, RelativePath: node.RelativePath,
			NodeKind: kind, DirectoryScope: node.DirectoryScope,
		})
	}
	return result, nil
}

// automaticAudioUpgradeSourceChoice chooses a compatible, single-audio source
// that is strictly better than the audio actually published in CURRENT.
// It intentionally does not infer CURRENT facts from the original source or
// make a stream choice for a multi-audio source.
func automaticAudioUpgradeSourceChoice(
	snapshot *persistence.LibraryFirstPublicationContext,
	current persistence.PersistedAudioFacts,
	format publication.AudioPublicationFormat,
) (FirstPublicationSourceChoice, error) {
	if snapshot == nil {
		return FirstPublicationSourceChoice{}, persistence.ErrPublicationAdmissionConflict
	}

	candidates := make([]publication.QualityCandidate, 0, len(snapshot.Sources))
	for _, source := range snapshot.Sources {
		if source.RequiresExplicitStreamSelection || source.AudioStreamCount != 1 {
			continue
		}
		facts := source.Quality.AudioSourceFacts()
		if _, err := publication.PlanAudioTransform(format, facts); err != nil {
			continue
		}
		candidates = append(candidates, publication.QualityCandidate{
			VariantUUID:  source.Selection.MediaVariantID.String(),
			LocationUUID: source.Selection.LocationID.String(),
			StreamIndex:  source.Quality.StreamIndex,
			Lossless:     source.Quality.Lossless,
			Codec:        source.Quality.Codec,
			Channels:     source.Quality.Channels,
			Layout:       source.ChannelLayout,
			BitDepth:     source.Quality.BitDepth,
			SampleRate:   source.Quality.SampleRate,
			Bitrate:      source.Quality.Bitrate,
		})
	}

	currentCandidate := audioUpgradeQualityCandidate(current)
	selection := publication.SelectQuality(candidates, &currentCandidate)
	if selection.RequiresManual || !selection.ReplacesCurrent || selection.Candidate == nil {
		return FirstPublicationSourceChoice{}, persistence.ErrPublicationAdmissionConflict
	}
	for _, source := range snapshot.Sources {
		if !source.RequiresExplicitStreamSelection && source.AudioStreamCount == 1 &&
			source.Selection.MediaVariantID.String() == selection.Candidate.VariantUUID &&
			source.Selection.LocationID.String() == selection.Candidate.LocationUUID &&
			source.Quality.StreamIndex == selection.Candidate.StreamIndex {
			if source.Selection.MediaVariantID == uuid.Nil || source.Selection.LocationID == uuid.Nil {
				break
			}
			return FirstPublicationSourceChoice{
				LocationID:     source.Selection.LocationID,
				MediaVariantID: source.Selection.MediaVariantID,
				StreamIndex:    source.Quality.StreamIndex,
			}, nil
		}
	}
	return FirstPublicationSourceChoice{}, persistence.ErrPublicationAdmissionConflict
}
