package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// matchingReceiptAudioUpgradeReadRepository is the read contract for a
// confirmed matching receipt plan. Preview only needs these readers.
type matchingReceiptAudioUpgradeReadRepository interface {
	firstPublicationContextReader
	matchingReceiptPublicationSourceReader
	ReadPositionSourceAuthority(context.Context, uuid.UUID) (*persistence.PositionSourceAuthority, error)
}

type matchingReceiptAudioUpgradeAdmissionRepository interface {
	matchingReceiptAudioUpgradeReadRepository
	AdmitMatchingReceiptAudioUpgrade(context.Context, persistence.MatchingReceiptAudioUpgradeAdmission, persistence.RiverInserter) (uuid.UUID, error)
}

// PreviewFromMatchingReceipt is the public entry point after a saved manual or
// Local matching confirmation. It dispatches to the existing exact FIRST
// machinery when the position has no CURRENT publication and to the CURRENT
// audio-upgrade preview when it does. A receipt never authorizes a fallback
// source outside its exact association.
func (service *LibraryPublications) PreviewFromMatchingReceipt(ctx context.Context, releaseID, positionID uuid.UUID, receipt persistence.MatchingReceiptPublicationSource, choice *FirstPublicationSourceChoice) (FirstPublicationPreview, error) {
	if !validMatchingReceiptPublicationSource(receipt) {
		return FirstPublicationPreview{}, persistence.ErrPublicationAdmissionConflict
	}
	if service == nil || service.repository == nil || releaseID == uuid.Nil || positionID == uuid.Nil {
		return FirstPublicationPreview{}, errors.New("matching receipt publication preview is unavailable")
	}
	repository, ok := service.repository.(firstPublicationContextReader)
	if !ok {
		return FirstPublicationPreview{}, errors.New("first publication context is unavailable")
	}
	snapshot, err := repository.ReadLibraryFirstPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		return FirstPublicationPreview{}, fmt.Errorf("read matching receipt publication context: %w", err)
	}
	if snapshot == nil || snapshot.Library == nil {
		return FirstPublicationPreview{}, errors.New("first publication context is incomplete")
	}
	if snapshot.Library.Current == nil {
		return service.PreviewFirstFromMatchingReceipt(ctx, releaseID, positionID, receipt, choice)
	}
	return service.previewCurrentAudioUpgradeFromMatchingReceipt(ctx, releaseID, positionID, receipt, choice)
}

// AdmitFromMatchingReceipt is the public confirmation entry point for both
// branches. ExpectedGeneration selects the branch: nil is ordinary exact FIRST,
// non-nil must equal the exact CURRENT generation. Admission never falls back to
// the other branch. Both branches re-check their authoritative state under their
// own transaction, and a digest for one branch never matches the other's
// domain-separated plan.
func (service *LibraryPublications) AdmitFromMatchingReceipt(ctx context.Context, releaseID, positionID uuid.UUID, confirmation FirstPublicationConfirmation) (uuid.UUID, error) {
	if confirmation.Receipt == nil || confirmation.SourceChoice == nil {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	receipt := *confirmation.Receipt
	if !validMatchingReceiptPublicationSource(receipt) {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	if service == nil || service.repository == nil {
		return uuid.Nil, errors.New("matching receipt publication admission is unavailable")
	}
	if confirmation.ExpectedGeneration == nil {
		// Ordinary exact FIRST. The existing FIRST receipt machinery rejects a
		// position that already has CURRENT, so this cannot silently replace it.
		return service.AdmitFirst(ctx, releaseID, positionID, confirmation)
	}
	return service.admitCurrentAudioUpgradeFromMatchingReceipt(ctx, releaseID, positionID, receipt, confirmation)
}

func (service *LibraryPublications) previewCurrentAudioUpgradeFromMatchingReceipt(ctx context.Context, releaseID, positionID uuid.UUID, receipt persistence.MatchingReceiptPublicationSource, choice *FirstPublicationSourceChoice) (FirstPublicationPreview, error) {
	if service.coordinator == nil {
		return FirstPublicationPreview{}, errors.New("publication coordinator is unavailable")
	}
	unlock, err := service.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Shared}})
	if err != nil {
		return FirstPublicationPreview{}, fmt.Errorf("acquire matching receipt CURRENT preview lock: %w", err)
	}
	defer unlock()

	repository, ok := service.repository.(matchingReceiptAudioUpgradeReadRepository)
	if !ok {
		return FirstPublicationPreview{}, errors.New("matching receipt CURRENT publication context is unavailable")
	}
	planningSettings, ok := service.settings.(firstPublicationFormatSettings)
	if !ok {
		return FirstPublicationPreview{}, errors.New("first publication settings are unavailable")
	}
	snapshot, err := repository.ReadLibraryFirstPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		return FirstPublicationPreview{}, fmt.Errorf("read matching receipt CURRENT context: %w", err)
	}
	if snapshot == nil || snapshot.Library == nil || snapshot.Library.Current == nil {
		// The branch changed between the dispatcher read and the locked read.
		return FirstPublicationPreview{}, persistence.ErrPublicationAdmissionConflict
	}
	runtimeSettings, err := planningSettings.ReadPublicationPlanningSettings(ctx)
	if err != nil {
		return FirstPublicationPreview{}, fmt.Errorf("read publication planning settings: %w", err)
	}
	selectedTools, _ := service.selectedFFmpegPairForSettings(ctx, runtimeSettings)
	plan, err := service.buildMatchingReceiptAudioUpgradePlan(ctx, repository, snapshot, runtimeSettings, selectedTools, releaseID, positionID, receipt, choice)
	if err != nil {
		return FirstPublicationPreview{}, err
	}
	return matchingReceiptAudioUpgradePreview(plan)
}

func (service *LibraryPublications) admitCurrentAudioUpgradeFromMatchingReceipt(ctx context.Context, releaseID, positionID uuid.UUID, receipt persistence.MatchingReceiptPublicationSource, confirmation FirstPublicationConfirmation) (uuid.UUID, error) {
	if service == nil || service.repository == nil || service.settings == nil || service.river == nil || service.coordinator == nil ||
		releaseID == uuid.Nil || positionID == uuid.Nil || confirmation.ExpectedEpoch == uuid.Nil ||
		confirmation.ExpectedGeneration == nil || confirmation.SourceChoice == nil ||
		!validFirstPublicationPreviewDigest(confirmation.PreviewDigest) {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	repository, ok := service.repository.(matchingReceiptAudioUpgradeAdmissionRepository)
	if !ok {
		return uuid.Nil, errors.New("matching receipt CURRENT publication admission is unavailable")
	}
	planningSettings, ok := service.settings.(firstPublicationFormatSettings)
	if !ok {
		return uuid.Nil, errors.New("first publication settings are unavailable")
	}
	unlock, err := service.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
	if err != nil {
		return uuid.Nil, fmt.Errorf("acquire matching receipt CURRENT admission lock: %w", err)
	}
	defer unlock()

	snapshot, err := repository.ReadLibraryFirstPublicationContext(ctx, releaseID, positionID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("read matching receipt CURRENT admission context: %w", err)
	}
	if snapshot == nil || snapshot.Library == nil || snapshot.Library.Current == nil || snapshot.Library.ActiveEpoch != confirmation.ExpectedEpoch ||
		snapshot.Library.Current.Record.Generation != *confirmation.ExpectedGeneration {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	runtimeSettings, err := planningSettings.ReadPublicationPlanningSettings(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("read publication planning settings: %w", err)
	}
	selectedTools, err := service.selectedFFmpegPairForSettings(ctx, runtimeSettings)
	if err != nil {
		return uuid.Nil, fmt.Errorf("select verified publication tools: %w", err)
	}
	plan, err := service.buildMatchingReceiptAudioUpgradePlan(ctx, repository, snapshot, runtimeSettings, selectedTools, releaseID, positionID, receipt, confirmation.SourceChoice)
	if err != nil {
		return uuid.Nil, err
	}
	if len(plan.Blockers) != 0 || plan.Digest != confirmation.PreviewDigest || plan.QualityStatus != "replace" ||
		plan.Source == nil || plan.Transform == nil || plan.CurrentWitness == nil || plan.Authority == nil ||
		plan.CurrentWitness.Generation != *confirmation.ExpectedGeneration || plan.Frozen.Validate() != nil {
		return uuid.Nil, persistence.ErrPublicationAdmissionConflict
	}
	rawFrozen, err := json.Marshal(plan.Frozen)
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode matching receipt audio-upgrade inputs: %w", err)
	}
	sourceSelection := plan.Source.Selection
	sourceSelection.HoldUUID = uuid.New()
	request := persistence.PublicationAdmission{
		PositionID: positionID, ExpectedReleaseID: releaseID, ExpectedEpoch: confirmation.ExpectedEpoch,
		ExpectedGeneration: confirmation.ExpectedGeneration, CandidateGeneration: uuid.New(),
		IntentKind: "audio", ToolsReadRequired: true,
		Tools:             append([]persistence.SourceAnalysisToolSelection(nil), plan.Tools...),
		Source:            &sourceSelection,
		FrozenInputs:      rawFrozen,
		NamespaceManifest: append([]persistence.PublicationNamespaceNode(nil), plan.NamespaceManifest...),
	}
	return repository.AdmitMatchingReceiptAudioUpgrade(ctx, persistence.MatchingReceiptAudioUpgradeAdmission{
		PublicationAdmission: request,
		Receipt:              receipt,
		ExpectedAuthority:    *plan.Authority,
		CurrentClaimID:       plan.CurrentClaimID,
		OutputRoot:           plan.OutputRoot,
		SourceSHA256:         plan.SourceSHA256,
	}, service.river)
}

// matchingReceiptAudioUpgradePlan is the frozen CURRENT replacement plan shared
// by preview and admission. It deliberately contains no random attempt identity:
// CandidateGeneration and holds are created only after the digest matches.
type matchingReceiptAudioUpgradePlan struct {
	ReleaseID          uuid.UUID
	PositionID         uuid.UUID
	Epoch              uuid.UUID
	Receipt            persistence.MatchingReceiptPublicationSource
	ReceiptVariantID   uuid.UUID
	Snapshot           *persistence.LibraryFirstPublicationContext
	Scoped             *persistence.LibraryFirstPublicationContext
	Source             *persistence.FirstPublicationSource
	Choice             FirstPublicationSourceChoice
	Authority          *persistence.PositionSourceAuthority
	CurrentClaimID     uuid.UUID
	TargetRelativePath string
	Transform          *publication.AudioTransformPlan
	NamespaceManifest  []persistence.PublicationNamespaceNode
	CurrentWitness     *persistence.PublicationCurrentWitness
	DesiredTags        map[string][]string
	Frozen             persistence.PublicationInputs
	OutputRoot         string
	Tools              []persistence.SourceAnalysisToolSelection
	SourceSHA256       []byte
	AudioStatus        string
	QualityStatus      string
	ComparisonReason   string
	Blockers           []string
	Digest             string
}

func (*LibraryPublications) buildMatchingReceiptAudioUpgradePlan(ctx context.Context, repository matchingReceiptAudioUpgradeReadRepository, snapshot *persistence.LibraryFirstPublicationContext, runtimeSettings settings.RuntimeSettings, selectedTools []persistence.SourceAnalysisToolSelection, releaseID, positionID uuid.UUID, receipt persistence.MatchingReceiptPublicationSource, choice *FirstPublicationSourceChoice) (*matchingReceiptAudioUpgradePlan, error) {
	if snapshot == nil || snapshot.Library == nil || snapshot.Library.Current == nil || !validMatchingReceiptPublicationSource(receipt) {
		return nil, persistence.ErrPublicationAdmissionConflict
	}
	current := snapshot.Library.Current
	plan := &matchingReceiptAudioUpgradePlan{
		ReleaseID: releaseID, PositionID: positionID, Epoch: snapshot.Library.ActiveEpoch,
		Receipt: receipt, Snapshot: snapshot, OutputRoot: runtimeSettings.OutputDirectory,
		Tools:       append([]persistence.SourceAnalysisToolSelection(nil), selectedTools...),
		AudioStatus: "unavailable", QualityStatus: "blocked",
		Blockers: []string{},
	}
	if snapshot.Library.ActiveAttempt != nil {
		plan.Blockers = append(plan.Blockers, "a publication attempt is already active")
	}
	variantID, err := matchingReceiptVariantID(snapshot, receipt.TrackSourceID)
	if err != nil {
		return nil, err
	}
	plan.ReceiptVariantID = variantID
	if err := repository.ReadMatchingReceiptPublicationSource(ctx, releaseID, positionID, variantID, receipt); err != nil {
		return nil, err
	}
	scoped, _, err := scopeFirstPublicationSnapshotToMatchingReceipt(snapshot, receipt)
	if err != nil {
		return nil, err
	}
	plan.Scoped = scoped
	authority, err := repository.ReadPositionSourceAuthority(ctx, positionID)
	if err != nil {
		return nil, fmt.Errorf("read matching receipt CURRENT source authority: %w", err)
	}
	plan.Authority = authority
	if authority == nil || (authority.Mode != "manual" && authority.Mode != "automatic") || authority.Revision == uuid.Nil ||
		authority.PublicationEpoch != current.Record.Epoch || authority.PublicationGeneration != current.Record.Generation ||
		current.SourceAuthority == nil || current.SourceAuthority.Status != "present" ||
		authority.Revision != current.SourceAuthority.Revision || authority.SourceLocationID != current.SourceAuthority.SourceLocationID ||
		authority.MediaVariantID != current.SourceAuthority.MediaVariantID {
		plan.Blockers = append(plan.Blockers, "the live source authority does not match CURRENT")
	}

	source, choiceValue, blocked := matchingReceiptAudioUpgradeSource(scoped, choice, publication.AudioPublicationFormat(runtimeSettings.PublicationFormat))
	if blocked != "" {
		plan.Blockers = append(plan.Blockers, blocked)
	}
	if source == nil {
		return plan, nil
	}
	plan.Source, plan.Choice = source, choiceValue
	plan.SourceSHA256 = append([]byte(nil), source.CanonicalSourceSHA256...)
	format := publication.AudioPublicationFormat(runtimeSettings.PublicationFormat)
	plan.AudioStatus, plan.QualityStatus, plan.ComparisonReason = matchingReceiptAudioUpgradeQuality(current, source, format)
	if plan.QualityStatus != "replace" {
		plan.TargetRelativePath = current.Record.RelativePath
		plan.DesiredTags = cloneDesiredPublicationTags(current.Metadata.Tags)
		return plan, nil
	}
	if len(selectedTools) != 2 {
		plan.Blockers = append(plan.Blockers, "verified managed FFmpeg executables are unavailable")
	}
	transform, err := publication.PlanAudioTransform(format, source.Quality.AudioSourceFacts())
	if err != nil {
		return nil, err
	}
	target, err := audioUpgradeTargetRelativePath(current.Record.RelativePath, format, source.Quality.AudioSourceFacts())
	if err != nil || path.Dir(target) != path.Dir(current.Record.RelativePath) {
		plan.Blockers = append(plan.Blockers, "the confirmed source cannot replace CURRENT at an equivalent published path")
		return plan, nil
	}
	plan.TargetRelativePath, plan.Transform = target, &transform
	plan.CurrentClaimID = currentPublicationNamespaceClaim(snapshot.Library, current.Record.RelativePath)
	if plan.CurrentClaimID == uuid.Nil {
		plan.Blockers = append(plan.Blockers, "the CURRENT namespace claim is unavailable")
	} else if plan.TargetRelativePath != "" {
		manifest, namespaceErr := planAudioUpgradeNamespace(ctx, runtimeSettings.OutputDirectory, positionID, plan.TargetRelativePath, snapshot.NamespaceClaims, snapshot.Library.NamespaceManifest, current.Record.RelativePath, &plan.CurrentClaimID)
		if namespaceErr != nil {
			plan.Blockers = append(plan.Blockers, "the CURRENT replacement target conflicts with an existing namespace claim")
		} else {
			plan.NamespaceManifest = manifest
		}
	}

	var sourceTags map[string][]string
	if source.Input.AnalysedMetadata != nil {
		sourceTags = source.Input.AnalysedMetadata.Tags
	}
	plan.DesiredTags = publication.ProjectSourceEncoderTags(current.Metadata.Tags, sourceTags, transform.Action)

	if len(plan.Blockers) == 0 {
		if blocked := plan.freezeNativeCurrentWitness(ctx, current, runtimeSettings); blocked != "" {
			plan.Blockers = append(plan.Blockers, blocked)
		}
	}

	plan.Digest = matchingReceiptAudioUpgradeDigest(plan)
	return plan, nil
}

func matchingReceiptAudioUpgradeSource(snapshot *persistence.LibraryFirstPublicationContext, choice *FirstPublicationSourceChoice, format publication.AudioPublicationFormat) (*persistence.FirstPublicationSource, FirstPublicationSourceChoice, string) {
	if snapshot == nil || len(snapshot.Sources) == 0 {
		return nil, FirstPublicationSourceChoice{}, "the confirmed source is unavailable"
	}
	if choice != nil {
		for i := range snapshot.Sources {
			source := &snapshot.Sources[i]
			if source.Selection.LocationID == choice.LocationID && source.Selection.MediaVariantID == choice.MediaVariantID && source.Quality.StreamIndex == choice.StreamIndex {
				if _, err := publication.PlanAudioTransform(format, source.Quality.AudioSourceFacts()); err != nil {
					return nil, FirstPublicationSourceChoice{}, "the selected source cannot satisfy the configured publication format"
				}
				return source, *choice, ""
			}
		}
		// An explicit choice outside the confirmed association is a conflict, not
		// a reason to fall back to another source.
		return nil, FirstPublicationSourceChoice{}, "the selected source is outside the confirmed association"
	}
	choice = firstPublicationSoleReceiptChoice(snapshot, format)
	if choice == nil {
		return nil, FirstPublicationSourceChoice{}, "explicit source and audio stream selection is required"
	}
	return matchingReceiptAudioUpgradeSource(snapshot, choice, format)
}

// matchingReceiptAudioUpgradeQuality is the shared strict-quality seam with the
// persistence admission. Copy uses measured candidate facts; ALAC keeps the
// measured lossless depth and sample rate. A target AAC bitrate is an encoder
// setting, not proof of improvement, so it is incomparable and never a write.
func matchingReceiptAudioUpgradeQuality(current *persistence.LibraryPublicationCurrent, source *persistence.FirstPublicationSource, format publication.AudioPublicationFormat) (string, string, string) {
	if current == nil || source == nil {
		return "unavailable", "blocked", "CURRENT audio facts or the confirmed source are unavailable"
	}
	if authority := current.SourceAuthority; authority != nil {
		sameVariant := authority.MediaVariantID != uuid.Nil && authority.MediaVariantID == source.Selection.MediaVariantID
		sameSHA := len(authority.CanonicalSourceSHA256) == sha256.Size && len(source.CanonicalSourceSHA256) == sha256.Size && bytes.Equal(authority.CanonicalSourceSHA256, source.CanonicalSourceSHA256)
		if sameVariant || sameSHA {
			return "matches", "unchanged", "the confirmed source is already published as CURRENT"
		}
	}
	// Shared strict-quality proof with the persistence typed admission. It
	// conservatively rejects an AAC lossy recode and supports copy/ALAC.
	better, err := persistence.ReceiptAudioUpgradeStrictlyBetter(source.Quality, current.Audio.Facts, format)
	if err != nil {
		return "unavailable", "unchanged", "the confirmed source quality cannot be proven strictly better than CURRENT"
	}
	if !better {
		return "unavailable", "unchanged", "the confirmed source does not improve CURRENT audio quality"
	}
	return "differs", "replace", "the confirmed source is strictly better than CURRENT audio"
}

func (plan *matchingReceiptAudioUpgradePlan) freezeNativeCurrentWitness(ctx context.Context, current *persistence.LibraryPublicationCurrent, runtimeSettings settings.RuntimeSettings) string {
	root, err := sourcefs.NewOutputOpener().OpenRoot(ctx, runtimeSettings.OutputDirectory)
	if err != nil {
		return "native CURRENT output root is unavailable"
	}
	defer func() { _ = root.Close() }()
	relative := current.Record.RelativePath
	parent, chain, err := openExistingPublicationParent(ctx, root, relative)
	if err != nil {
		return "native CURRENT ownership could not be frozen"
	}
	defer closeOutputChain(chain)
	mutation, ok := parent.(sourcefs.OutputMutation)
	if !ok {
		return "native CURRENT ownership could not be frozen"
	}
	name := path.Base(relative)
	file, err := mutation.OpenExistingRegular(ctx, name)
	if err != nil {
		return "native CURRENT ownership could not be frozen"
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat(ctx)
	if err != nil {
		return "native CURRENT ownership could not be frozen"
	}
	tempDir, err := os.MkdirTemp("", "publication-preview-")
	if err != nil {
		return "native CURRENT ownership could not be frozen"
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	inspection := filepath.Join(tempDir, "current"+path.Ext(relative))
	if err := copyFrozenAudioUpgradeInspection(ctx, file, current.Ownership.Identity, inspection); err != nil {
		return "native CURRENT ownership could not be frozen"
	}
	if _, err := verifyInterruptedSourceRepublishTarget(ctx, root, chain, mutation, name, file, before); err != nil {
		return "native CURRENT changed while its witness was captured"
	}
	observed, err := (metadata.Reader{}).Read(ctx, inspection, metadata.ReadRequest{CoverInspection: true, LimitSourceCover: false})
	if err != nil || !reflect.DeepEqual(observed.Tags, current.Metadata.Tags) {
		return "native CURRENT tags do not match the frozen CURRENT witness"
	}
	observedAssets, err := ObservePublicationAssets(observed)
	if err != nil || !reflect.DeepEqual(observedAssets, current.Assets) {
		return "native CURRENT cover or lyrics do not match the frozen CURRENT witness"
	}
	artwork, err := metadata.ReadArtworkSnapshot(ctx, inspection)
	if err != nil {
		return "native CURRENT artwork inventory is unavailable"
	}
	witness := persistence.PublicationCurrentWitness{
		Generation: current.Record.Generation, RelativePath: current.Record.RelativePath,
		Ownership: cloneFileIdentity(current.Ownership.Identity), AudioFacts: current.Audio.Facts,
		Assets: current.Assets.Clone(), Tags: cloneDesiredPublicationTags(current.Metadata.Tags),
		ArtworkInventory: audioUpgradeArtworkWitnesses(artwork.ArtworkInventory()),
	}
	plan.CurrentWitness = &witness
	if plan.Source == nil || plan.Transform == nil || plan.Authority == nil || plan.DesiredTags == nil {
		return ""
	}
	receiptTrackSourceID := plan.Receipt.TrackSourceID
	plan.Frozen = persistence.PublicationInputs{
		Version:              persistence.PublicationAudioUpgradeInputsVersion,
		QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind:           "audio", SourceAuthorityMode: plan.Authority.Mode,
		Format: publication.AudioPublicationFormat(runtimeSettings.PublicationFormat),
		Audio:  audioInputCopy(&plan.Source.Input), DesiredTags: cloneDesiredPublicationTags(plan.DesiredTags),
		Tools: append([]persistence.SourceAnalysisToolSelection(nil), plan.Tools...),
		AudioUpgrade: &persistence.PublicationAudioUpgradeInput{
			Current: witness, SourceAuthorityRevision: plan.Authority.Revision,
			ReceiptTrackSourceID: &receiptTrackSourceID,
		},
	}
	if err := plan.Frozen.Validate(); err != nil {
		plan.Frozen = persistence.PublicationInputs{}
		return "the confirmed source cannot be used for CURRENT audio replacement"
	}
	return ""
}

func matchingReceiptAudioUpgradeDigest(plan *matchingReceiptAudioUpgradePlan) string {
	authority := struct {
		PositionID            uuid.UUID `json:"position_id"`
		Mode                  string    `json:"mode"`
		Revision              uuid.UUID `json:"revision"`
		PublicationEpoch      uuid.UUID `json:"publication_epoch"`
		PublicationGeneration uuid.UUID `json:"publication_generation"`
		SourceLocationID      uuid.UUID `json:"source_location_id"`
		MediaVariantID        uuid.UUID `json:"media_variant_id"`
	}{}
	if plan.Authority != nil {
		authority.PositionID = plan.Authority.PositionID
		authority.Mode = plan.Authority.Mode
		authority.Revision = plan.Authority.Revision
		authority.PublicationEpoch = plan.Authority.PublicationEpoch
		authority.PublicationGeneration = plan.Authority.PublicationGeneration
		authority.SourceLocationID = plan.Authority.SourceLocationID
		authority.MediaVariantID = plan.Authority.MediaVariantID
	}
	encoded, err := json.Marshal(struct {
		Version            string                                       `json:"version"`
		ReleaseID          uuid.UUID                                    `json:"release_id"`
		PositionID         uuid.UUID                                    `json:"position_id"`
		Epoch              uuid.UUID                                    `json:"epoch"`
		Receipt            persistence.MatchingReceiptPublicationSource `json:"receipt"`
		ReceiptVariantID   uuid.UUID                                    `json:"receipt_variant_id"`
		SourceAssociation  uuid.UUID                                    `json:"source_association_id"`
		SourceSHA256       []byte                                       `json:"source_sha256"`
		SourceQuality      persistence.PersistedAudioFacts              `json:"source_quality"`
		Choice             FirstPublicationSourceChoice                 `json:"choice"`
		Authority          any                                          `json:"authority"`
		CurrentClaimID     uuid.UUID                                    `json:"current_claim_id"`
		TargetRelativePath string                                       `json:"target_relative_path"`
		OutputRoot         string                                       `json:"output_root"`
		NamespaceManifest  []persistence.PublicationNamespaceNode       `json:"namespace_manifest"`
		Tools              []persistence.SourceAnalysisToolSelection    `json:"tools"`
		Frozen             persistence.PublicationInputs                `json:"frozen"`
		AudioStatus        string                                       `json:"audio_status"`
		QualityStatus      string                                       `json:"quality_status"`
		Blockers           []string                                     `json:"blockers"`
	}{"melo:matching-receipt-audio-upgrade:v1", plan.ReleaseID, plan.PositionID, plan.Epoch, plan.Receipt, plan.ReceiptVariantID,
		sourceAssociationID(plan.Source), plan.SourceSHA256, sourceQuality(plan.Source), plan.Choice, authority, plan.CurrentClaimID,
		plan.TargetRelativePath, plan.OutputRoot, plan.NamespaceManifest, plan.Tools, plan.Frozen, plan.AudioStatus, plan.QualityStatus, plan.Blockers})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func sourceAssociationID(source *persistence.FirstPublicationSource) uuid.UUID {
	if source == nil {
		return uuid.Nil
	}
	return source.AssociationID
}

func sourceQuality(source *persistence.FirstPublicationSource) persistence.PersistedAudioFacts {
	if source == nil {
		return persistence.PersistedAudioFacts{}
	}
	return source.Quality
}

func matchingReceiptAudioUpgradePreview(plan *matchingReceiptAudioUpgradePlan) (FirstPublicationPreview, error) {
	preview := FirstPublicationPreview{
		ReleaseID: plan.ReleaseID, PositionID: plan.PositionID, Epoch: plan.Epoch,
		Current: plan.Snapshot.Library.Current, ActiveAttempt: plan.Snapshot.Library.ActiveAttempt,
		MetadataRevisions: map[string]int64{
			"release":   plan.Snapshot.Metadata.Release.Revision,
			"track":     plan.Snapshot.Metadata.Track.Revision,
			"recording": plan.Snapshot.Metadata.Recording.Revision,
			"position":  plan.Snapshot.Metadata.Position.Revision,
		},
		Warnings: []string{}, Blockers: append([]string{}, plan.Blockers...),
		NativeStatus: "native CURRENT ownership and artwork verified", Mode: firstPublicationPreviewModeOrdinary,
		ContextDigest: plan.Digest,
	}
	if plan.CurrentWitness == nil {
		preview.NativeStatus = "native CURRENT verification unavailable"
	}
	addArtistMetadataRevisions(preview.MetadataRevisions, plan.Snapshot.ArtistMetadataRevisions)
	preview.SourceCandidates = make([]FirstPublicationPreviewSource, 0, len(plan.Scoped.Sources))
	for _, source := range plan.Scoped.Sources {
		preview.SourceCandidates = append(preview.SourceCandidates, firstPublicationPreviewSource(source))
	}
	if plan.Source != nil {
		selected := firstPublicationPreviewSource(*plan.Source)
		preview.SelectedSource = &selected
		preview.Transform = plan.Transform
		if plan.TargetRelativePath != "" {
			preview.PathComponents = strings.Split(plan.TargetRelativePath, "/")
			preview.ResolvedPathComponents = append([]string(nil), preview.PathComponents...)
		}
		preview.DesiredTags = publication.TagProjection{Tags: cloneDesiredPublicationTags(plan.DesiredTags)}
	}
	if plan.Snapshot.Library.Current != nil {
		preview.CurrentComparison = compareFirstPublicationCurrent(preview, plan.Snapshot.Library.Current, plan.Scoped)
		preview.ConfirmationCapability = &SourceRepublishConfirmationCapability{
			Kind: "matching_receipt_audio_upgrade", Status: "ready",
			ReasonCodes: []string{}, CurrentGeneration: plan.Snapshot.Library.Current.Record.Generation,
		}
		switch {
		case len(plan.Blockers) != 0:
			preview.ConfirmationCapability.Status = "blocked"
			preview.ConfirmationCapability.ReasonCodes = []string{"matching_receipt_plan_blocked"}
		case plan.QualityStatus == "unchanged":
			preview.ConfirmationCapability.Status = "unchanged"
			preview.ConfirmationCapability.ReasonCodes = []string{"matching_receipt_audio_not_better"}
		case plan.QualityStatus != "replace" || plan.Digest == "":
			preview.ConfirmationCapability.Status = "blocked"
		}
		preview.CurrentComparison.Status = preview.ConfirmationCapability.Status
		preview.CurrentComparison.Reason = plan.ComparisonReason
		preview.CurrentComparison.AudioStatus = plan.AudioStatus
		preview.CurrentComparison.CoverStatus = "matches"
		preview.CurrentComparison.LyricsStatus = "matches"
	}
	return preview, nil
}

func firstPublicationPreviewSource(source persistence.FirstPublicationSource) FirstPublicationPreviewSource {
	return FirstPublicationPreviewSource{
		RootID: source.Selection.RootID, LocationID: source.Selection.LocationID, WorkID: source.Selection.WorkID,
		AssociationID: source.AssociationID, Selected: source.Selected, MediaVariantID: source.Selection.MediaVariantID,
		StreamIndex: source.Quality.StreamIndex, Codec: source.Quality.Codec, Quality: source.Quality,
		Title: source.StreamTitle, Language: source.StreamLanguage,
		RequiresExplicitStreamSelection: source.RequiresExplicitStreamSelection,
		AudioStreamCount:                source.AudioStreamCount, UnsupportedAudioStreamCount: source.UnsupportedAudioStreamCount,
	}
}
