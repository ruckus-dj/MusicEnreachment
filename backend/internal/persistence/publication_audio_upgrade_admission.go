package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

// MatchingReceiptAudioUpgradeAdmission is the private-capability admission for a
// version-2 audio upgrade bound to one saved matching confirmation. The caller
// freezes every fact it compared outside the transaction; admission re-proves
// them under the output, source-mutation, tools and epoch gates.
type MatchingReceiptAudioUpgradeAdmission struct {
	PublicationAdmission
	// Receipt is the exact saved confirmation and the association it bound.
	Receipt MatchingReceiptPublicationSource
	// ExpectedAuthority is the live source authority the caller observed.
	ExpectedAuthority PositionSourceAuthority
	// CurrentClaimID is the namespace claim of the CURRENT artifact.
	CurrentClaimID uuid.UUID
	// OutputRoot is the configured native output directory the caller opened.
	OutputRoot string
	// SourceSHA256 is the canonical source digest read from the selected
	// FirstPublicationSource. It may be nil when no digest was established.
	SourceSHA256 []byte
}

type matchingReceiptAudioUpgradeHook struct {
	receipt           MatchingReceiptPublicationSource
	expectedAuthority PositionSourceAuthority
	currentClaimID    uuid.UUID
	outputRoot        string
	sourceSHA256      []byte
}

// AdmitMatchingReceiptAudioUpgrade is the only admission path allowed to create
// a version-2 attempt with a receipt marker. It preserves the live source
// authority mode and never updates track_source selection.
func (repository *SetupManagerRepository) AdmitMatchingReceiptAudioUpgrade(ctx context.Context, upgrade MatchingReceiptAudioUpgradeAdmission, client RiverInserter) (uuid.UUID, error) {
	request := upgrade.PublicationAdmission
	input, err := DecodePublicationInputs(request.FrozenInputs)
	if err != nil {
		return uuid.Nil, fmt.Errorf("admit matching receipt audio upgrade: %w", err)
	}
	var marker *uuid.UUID
	if input.AudioUpgrade != nil {
		marker = input.AudioUpgrade.ReceiptTrackSourceID
	}
	authority := upgrade.ExpectedAuthority
	if input.Version != PublicationAudioUpgradeInputsVersion || input.AudioUpgrade == nil || input.Audio == nil ||
		marker == nil || *marker != upgrade.Receipt.TrackSourceID || !validMatchingReceiptPublicationSource(upgrade.Receipt) ||
		len(upgrade.SourceSHA256) != 0 && len(upgrade.SourceSHA256) != sha256.Size ||
		request.IntentKind != "audio" || request.ExpectedGeneration == nil || request.Source == nil ||
		request.ExpectedReleaseID == uuid.Nil || request.ExpectedEpoch == uuid.Nil || request.CandidateGeneration == uuid.Nil ||
		!request.ToolsReadRequired || len(request.Tools) != 2 || upgrade.CurrentClaimID == uuid.Nil || upgrade.OutputRoot == "" ||
		request.reconciliationGuard != nil || request.firstAdmission != nil || request.sourceRepublishAdmission != nil ||
		request.matchingRecheckCoverInput != nil || request.namespaceAdoption != nil || request.namespacePredecessor != nil ||
		(input.SourceAuthorityMode != "manual" && input.SourceAuthorityMode != "automatic") ||
		authority.PositionID != request.PositionID || authority.Revision == uuid.Nil ||
		authority.Mode != input.SourceAuthorityMode || authority.PublicationEpoch != request.ExpectedEpoch ||
		authority.PublicationGeneration != input.AudioUpgrade.Current.Generation ||
		authority.Revision != input.AudioUpgrade.SourceAuthorityRevision ||
		authority.SourceLocationID == uuid.Nil || authority.MediaVariantID == uuid.Nil ||
		*request.ExpectedGeneration != input.AudioUpgrade.Current.Generation ||
		!publicationSourceMatchesInput(*request.Source, *input.Audio) {
		return uuid.Nil, fmt.Errorf("admit matching receipt audio upgrade: invalid frozen request: %w", ErrPublicationAdmissionConflict)
	}
	request.validatedAudioUpgrade = true
	request.matchingReceiptAudioUpgrade = &matchingReceiptAudioUpgradeHook{
		receipt:           upgrade.Receipt,
		expectedAuthority: authority,
		currentClaimID:    upgrade.CurrentClaimID,
		outputRoot:        upgrade.OutputRoot,
		sourceSHA256:      append([]byte(nil), upgrade.SourceSHA256...),
	}
	return repository.AdmitPublication(ctx, request, client)
}

func (repository *SetupManagerRepository) validateAudioUpgradeAdmission(ctx context.Context, tx bun.Tx, request PublicationAdmission, input PublicationInputs, epoch uuid.UUID) error {
	frozen := input.AudioUpgrade
	if frozen == nil || request.Source == nil || request.ExpectedGeneration == nil || input.Audio == nil {
		return ErrPublicationAdmissionConflict
	}
	for _, table := range []string{"app_setting", "local_artist", "local_recording", "local_release", "local_track", "media_variant", "provider", "provider_artist", "provider_local_artist", "release_artist", "release_track", "source_analysis_step", "source_analysis_work", "source_location", "source_root", "source_work_variant_identity", "tool_installation", "track_artist", "track_source"} {
		if _, err := tx.ExecContext(ctx, "LOCK TABLE "+table+" IN SHARE MODE"); err != nil {
			return fmt.Errorf("admit audio upgrade: guard %s: %w", table, err)
		}
	}
	var configuredFormat, configuredInstallation string
	if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='publication_format'`).Scan(ctx, &configuredFormat); err != nil {
		return fmt.Errorf("admit audio upgrade: read publication format: %w", err)
	}
	if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='active_ffmpeg_installation_id'`).Scan(ctx, &configuredInstallation); err != nil {
		return fmt.Errorf("admit audio upgrade: read active ffmpeg: %w", err)
	}
	if configuredFormat != string(input.Format) || len(input.Tools) == 0 || input.Tools[0].InstallationID.String() != configuredInstallation {
		return fmt.Errorf("admit audio upgrade: planning settings changed: %w", ErrPublicationAdmissionConflict)
	}
	plan, err := publication.PlanAudioTransform(input.Format, input.Audio.AudioFacts.AudioSourceFacts())
	if err != nil {
		return err
	}
	expectedPath := strings.TrimSuffix(frozen.Current.RelativePath, path.Ext(frozen.Current.RelativePath)) + "." + plan.Extension
	var target PublicationNamespaceNode
	fileTargets := 0
	for _, node := range request.NamespaceManifest {
		if node.NodeKind == "file" {
			fileTargets++
			target = node
			if node.RelativePath != expectedPath {
				return ErrPublicationAdmissionConflict
			}
		}
	}
	if fileTargets != 1 {
		return ErrPublicationAdmissionConflict
	}
	var current TrackPublication
	if err := tx.NewSelect().Model(&current).Where("position_id = ?", request.PositionID).For("UPDATE").Scan(ctx); err != nil {
		return fmt.Errorf("admit audio upgrade: lock current publication: %w", ErrPublicationAdmissionConflict)
	}
	if current.Epoch != epoch || current.Generation != frozen.Current.Generation || current.RelativePath != frozen.Current.RelativePath {
		return fmt.Errorf("admit audio upgrade: current publication changed: %w", ErrPublicationAdmissionConflict)
	}
	var ownership PublicationCurrentOwnership
	var audio PublicationCurrentAudio
	if decodePublicationCurrentContract(current.Ownership, &ownership) != nil || decodePublicationCurrentContract(current.ActualAudio, &audio) != nil ||
		!reflect.DeepEqual(ownership.Identity, frozen.Current.Ownership) || !reflect.DeepEqual(audio.Facts, frozen.Current.AudioFacts) {
		return fmt.Errorf("admit audio upgrade: frozen current facts changed: %w", ErrPublicationAdmissionConflict)
	}
	assets, err := DecodePublicationAssetOutcomes(current.ActualAssets)
	if err != nil || !reflect.DeepEqual(assets, frozen.Current.Assets) {
		return fmt.Errorf("admit audio upgrade: frozen current assets changed: %w", ErrPublicationAdmissionConflict)
	}
	var tagWitness struct {
		Tags map[string][]string `json:"tags"`
	}
	if err := json.Unmarshal(current.ActualMetadata, &tagWitness); err != nil || !reflect.DeepEqual(tagWitness.Tags, frozen.Current.Tags) {
		return fmt.Errorf("admit audio upgrade: frozen current tags changed: %w", ErrPublicationAdmissionConflict)
	}
	if request.matchingReceiptAudioUpgrade != nil {
		return validateMatchingReceiptAudioUpgradeAdmission(ctx, tx, request, input, epoch, current, target, plan)
	}
	var authority PositionSourceAuthority
	if err := tx.NewSelect().Model(&authority).Where("position_id = ?", request.PositionID).For("UPDATE").Scan(ctx); err != nil || authority.Mode != "automatic" || authority.Revision != frozen.SourceAuthorityRevision || authority.PublicationEpoch != epoch || authority.PublicationGeneration != current.Generation {
		return fmt.Errorf("admit audio upgrade: automatic source authority changed: %w", ErrPublicationAdmissionConflict)
	}
	var contextSnapshot *LibraryFirstPublicationContext
	if err := readLibraryFirstPublicationContextTx(ctx, tx, request.ExpectedReleaseID, request.PositionID, &contextSnapshot); err != nil {
		return fmt.Errorf("admit audio upgrade: read source candidate universe: %w", ErrPublicationAdmissionConflict)
	}
	if contextSnapshot.Library == nil || contextSnapshot.Library.Current == nil {
		return ErrPublicationAdmissionConflict
	}
	receiptBound, err := receiptBoundTrackSourceIDs(ctx, tx, request.PositionID)
	if err != nil {
		return err
	}
	candidates := make([]publication.QualityCandidate, 0, len(contextSnapshot.Sources))
	for _, source := range contextSnapshot.Sources {
		if _, heldForConfirmation := receiptBound[source.AssociationID]; heldForConfirmation {
			continue
		}
		if source.RequiresExplicitStreamSelection || source.AudioStreamCount != 1 {
			continue
		}
		if _, err := publication.PlanAudioTransform(input.Format, source.Quality.AudioSourceFacts()); err != nil {
			continue
		}
		facts := source.Quality
		candidates = append(candidates, publication.QualityCandidate{VariantUUID: source.Selection.MediaVariantID.String(), LocationUUID: source.Selection.LocationID.String(), StreamIndex: facts.StreamIndex,
			Lossless: facts.Lossless, Codec: facts.Codec, Channels: facts.Channels, Layout: source.ChannelLayout, BitDepth: facts.BitDepth, SampleRate: facts.SampleRate, Bitrate: facts.Bitrate})
	}
	currentCandidate := publication.QualityCandidate{VariantUUID: uuid.Nil.String(), LocationUUID: uuid.Nil.String(), StreamIndex: frozen.Current.AudioFacts.StreamIndex,
		Lossless: frozen.Current.AudioFacts.Lossless, Codec: frozen.Current.AudioFacts.Codec, Channels: frozen.Current.AudioFacts.Channels,
		Layout: frozen.Current.AudioFacts.ChannelLayout, BitDepth: frozen.Current.AudioFacts.BitDepth, SampleRate: frozen.Current.AudioFacts.SampleRate, Bitrate: frozen.Current.AudioFacts.Bitrate}
	selection := publication.SelectQuality(candidates, &currentCandidate)
	if !selection.ReplacesCurrent || selection.Candidate == nil || selection.Candidate.VariantUUID != input.Audio.MediaVariantID.String() ||
		selection.Candidate.LocationUUID != input.Audio.LocationID.String() || selection.Candidate.StreamIndex != input.Audio.AudioFacts.StreamIndex || !reflect.DeepEqual(input.Audio.AudioFacts, sourceFactsForUpgrade(contextSnapshot.Sources, *request.Source, input.Audio.AudioFacts.StreamIndex)) {
		return fmt.Errorf("admit audio upgrade: selected source is not a strictly better candidate: %w", ErrPublicationAdmissionConflict)
	}
	return nil
}

// ReadReceiptBoundTrackSourceIDs reports live associations that were saved for
// separate, explicit publication. Automatic reconciliation cannot consume them.
func (repository *SetupManagerRepository) ReadReceiptBoundTrackSourceIDs(ctx context.Context, positionID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	return receiptBoundTrackSourceIDs(ctx, repository.db, positionID)
}

func receiptBoundTrackSourceIDs(ctx context.Context, db bun.IDB, positionID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	var ids []uuid.UUID
	err := db.NewRaw(`SELECT source.id FROM release_track position
		JOIN track_source source ON source.track_id=position.track_id
		WHERE position.id=? AND source.decision_method='manual' AND (
			EXISTS (SELECT 1 FROM manual_matching_receipt saved
				WHERE saved.response->'confirmed_sources' @>
					jsonb_build_array(jsonb_build_object('track_source_id', source.id)))
			OR EXISTS (SELECT 1 FROM local_matching_receipt saved
				WHERE saved.response->'confirmed_sources' @>
					jsonb_build_array(jsonb_build_object('track_source_id', source.id)))
		)`, positionID).Scan(ctx, &ids)
	if err != nil {
		return nil, fmt.Errorf("read receipt-bound track sources: %w", err)
	}
	result := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		result[id] = struct{}{}
	}
	return result, nil
}

func validateMatchingReceiptAudioUpgradeAdmission(ctx context.Context, tx bun.Tx, request PublicationAdmission, input PublicationInputs, epoch uuid.UUID, current TrackPublication, target PublicationNamespaceNode, plan publication.AudioTransformPlan) error {
	hook := request.matchingReceiptAudioUpgrade
	previous := hook.expectedAuthority
	var authority PositionSourceAuthority
	if err := tx.NewSelect().Model(&authority).Where("position_id=?", request.PositionID).For("UPDATE").Scan(ctx); err != nil ||
		authority.Mode != previous.Mode || authority.Revision != previous.Revision ||
		authority.PublicationEpoch != epoch || authority.PublicationGeneration != current.Generation ||
		authority.SourceLocationID != previous.SourceLocationID || authority.MediaVariantID != previous.MediaVariantID ||
		authority.MediaVariantID == request.Source.MediaVariantID {
		return fmt.Errorf("admit matching receipt audio upgrade: current source authority changed or source is unchanged: %w", ErrPublicationAdmissionConflict)
	}
	if err := readMatchingReceiptPublicationSource(ctx, tx, request.ExpectedReleaseID, request.PositionID, request.Source.MediaVariantID, hook.receipt); err != nil {
		return err
	}
	var outputDirectory string
	if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='output_directory'`).Scan(ctx, &outputDirectory); err != nil {
		return fmt.Errorf("admit matching receipt audio upgrade: read output directory: %w", err)
	}
	if !filepath.IsAbs(outputDirectory) || filepath.Clean(outputDirectory) != filepath.Clean(hook.outputRoot) {
		return fmt.Errorf("admit matching receipt audio upgrade: output directory changed: %w", ErrPublicationAdmissionConflict)
	}
	var trackID uuid.UUID
	if err := tx.NewRaw(`SELECT track_id FROM release_track WHERE id=? AND release_id=?`, request.PositionID, request.ExpectedReleaseID).Scan(ctx, &trackID); err != nil {
		return fmt.Errorf("admit matching receipt audio upgrade: read position track: %w", err)
	}
	var sources []FirstPublicationSource
	var associations []PublicationSourceAssociation
	var blockers []string
	if err := readFirstPublicationSources(ctx, tx, trackID, &sources, &associations, &blockers); err != nil {
		return fmt.Errorf("admit matching receipt audio upgrade: read confirmed source: %w", err)
	}
	var selected *FirstPublicationSource
	for i := range sources {
		source := &sources[i]
		if source.AssociationID == hook.receipt.TrackSourceID && publicationSourceMatchesInput(*request.Source, source.Input) &&
			source.Quality.StreamIndex == input.Audio.AudioFacts.StreamIndex {
			selected = source
			break
		}
	}
	if selected == nil || !reflect.DeepEqual(selected.Quality, input.Audio.AudioFacts) ||
		!bytes.Equal(selected.CanonicalSourceSHA256, hook.sourceSHA256) {
		return fmt.Errorf("admit matching receipt audio upgrade: confirmed source or successful probe changed: %w", ErrPublicationAdmissionConflict)
	}
	if len(hook.sourceSHA256) != 0 {
		var same bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_variant WHERE id=? AND source_sha256=?)`, authority.MediaVariantID, hook.sourceSHA256).Scan(ctx, &same); err != nil {
			return fmt.Errorf("admit matching receipt audio upgrade: compare source digests: %w", err)
		}
		if same {
			return fmt.Errorf("admit matching receipt audio upgrade: source audio is unchanged: %w", ErrPublicationAdmissionConflict)
		}
	}
	better, err := ReceiptAudioUpgradeStrictlyBetter(selected.Quality, input.AudioUpgrade.Current.AudioFacts, input.Format)
	if err != nil || !better {
		return fmt.Errorf("admit matching receipt audio upgrade: source and planned output are not strictly better: %w", ErrPublicationAdmissionConflict)
	}
	var sourceTags map[string][]string
	if selected.Input.AnalysedMetadata != nil {
		sourceTags = selected.Input.AnalysedMetadata.Tags
	}
	if !reflect.DeepEqual(publication.ProjectSourceEncoderTags(input.AudioUpgrade.Current.Tags, sourceTags, plan.Action), input.DesiredTags) {
		return fmt.Errorf("admit matching receipt audio upgrade: invalid preservation of current tags: %w", ErrPublicationAdmissionConflict)
	}
	return validateMatchingReceiptAudioUpgradeNamespace(ctx, tx, request, current, target)
}

func validateMatchingReceiptAudioUpgradeNamespace(ctx context.Context, tx bun.Tx, request PublicationAdmission, current TrackPublication, target PublicationNamespaceNode) error {
	var canonicalCurrent string
	if err := tx.NewRaw(`SELECT c.canonical_key
		FROM publication_namespace_claim c JOIN publication_namespace_reservation r ON r.namespace_claim_id=c.id
		WHERE c.id=? AND c.epoch=? AND c.node_kind='file' AND c.owner_position_id=? AND c.relative_path=?
		  AND r.position_id=? AND r.generation=?
		  AND NOT EXISTS (SELECT 1 FROM publication_root_abandonment a WHERE a.epoch=c.epoch AND c.id=ANY(a.namespace_claim_ids))
		  AND NOT EXISTS (SELECT 1 FROM obsolete_publication_output_cleanup cleanup WHERE cleanup.namespace_claim_id=c.id)`,
		request.matchingReceiptAudioUpgrade.currentClaimID, current.Epoch, current.PositionID, current.RelativePath,
		current.PositionID, current.Generation).Scan(ctx, &canonicalCurrent); err != nil {
		return fmt.Errorf("admit matching receipt audio upgrade: exact current file claim changed: %w", ErrPublicationAdmissionConflict)
	}
	if target.RelativePath == current.RelativePath {
		if target.CanonicalKey != canonicalCurrent {
			return ErrPublicationAdmissionConflict
		}
	} else {
		var occupied bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM publication_namespace_claim
			WHERE epoch=? AND (canonical_key=? OR relative_path=?))`,
			current.Epoch, target.CanonicalKey, target.RelativePath).Scan(ctx, &occupied); err != nil {
			return fmt.Errorf("admit matching receipt audio upgrade: read destination claim: %w", err)
		}
		if occupied || target.CanonicalKey == canonicalCurrent {
			return ErrPublicationAdmissionConflict
		}
	}
	for _, node := range request.NamespaceManifest {
		if node.NodeKind != "dir" {
			continue
		}
		if !strings.HasPrefix(current.RelativePath, node.RelativePath+"/") ||
			!strings.HasPrefix(canonicalCurrent, node.CanonicalKey+"/") {
			return ErrPublicationAdmissionConflict
		}
		var parent bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1
			FROM publication_namespace_claim c JOIN publication_namespace_reservation r ON r.namespace_claim_id=c.id
			WHERE c.epoch=? AND c.node_kind='dir' AND c.canonical_key=? AND c.relative_path=? AND c.directory_scope_key=?
			  AND r.position_id=? AND r.generation=?
			  AND NOT EXISTS (SELECT 1 FROM publication_root_abandonment a WHERE a.epoch=c.epoch AND c.id=ANY(a.namespace_claim_ids)))`,
			current.Epoch, node.CanonicalKey, node.RelativePath, node.DirectoryScope, current.PositionID, current.Generation).Scan(ctx, &parent); err != nil {
			return fmt.Errorf("admit matching receipt audio upgrade: read current directory scope: %w", err)
		}
		if !parent {
			return ErrPublicationAdmissionConflict
		}
	}
	return nil
}

// ReceiptAudioUpgradeStrictlyBetter requires both the measured source and the
// qualities preserved by its transform to strictly improve measured CURRENT.
// A lossy encoder's requested rate is not evidence of higher audio quality.
func ReceiptAudioUpgradeStrictlyBetter(source, current PersistedAudioFacts, format publication.AudioPublicationFormat) (bool, error) {
	plan, err := publication.PlanAudioTransform(format, source.AudioSourceFacts())
	if err != nil {
		return false, err
	}
	candidate := qualityCandidateFromPersisted(source)
	baseline := qualityCandidateFromPersisted(current)
	if publication.CompareQuality(candidate, baseline) != publication.QualityBetter {
		return false, nil
	}
	switch plan.Action {
	case publication.AudioCodecCopy:
	case publication.AudioCodecALAC:
		candidate.Codec = "alac"
		candidate.Bitrate = nil
	default:
		return false, nil
	}
	return publication.CompareQuality(candidate, baseline) == publication.QualityBetter, nil
}

func sourceFactsForUpgrade(sources []FirstPublicationSource, selected PublicationSourceSelection, stream int) PersistedAudioFacts {
	for _, source := range sources {
		if source.Selection.LocationID == selected.LocationID && source.Selection.MediaVariantID == selected.MediaVariantID && source.Input.WorkID == selected.WorkID && source.Quality.StreamIndex == stream {
			return source.Quality
		}
	}
	return PersistedAudioFacts{}
}

func (repository *SetupManagerRepository) applyAutomaticAudioUpgradeSourceSelection(ctx context.Context, tx bun.Tx, request PublicationAdmission, input PublicationInputs) (uuid.UUID, bool, error) {
	var snapshot *LibraryFirstPublicationContext
	if err := readLibraryFirstPublicationContextTx(ctx, tx, request.ExpectedReleaseID, request.PositionID, &snapshot); err != nil {
		return uuid.Nil, false, fmt.Errorf("admit audio upgrade: read source association witness: %w", ErrPublicationAdmissionConflict)
	}
	if snapshot == nil || input.Audio == nil || request.Source == nil {
		return uuid.Nil, false, ErrPublicationAdmissionConflict
	}
	var associationID uuid.UUID
	for _, source := range snapshot.Sources {
		if source.Input.WorkID == request.Source.WorkID && source.Selection.LocationID == request.Source.LocationID &&
			source.Selection.MediaVariantID == request.Source.MediaVariantID && source.Quality.StreamIndex == input.Audio.AudioFacts.StreamIndex &&
			!source.RequiresExplicitStreamSelection && source.AudioStreamCount == 1 {
			associationID = source.AssociationID
			break
		}
	}
	if associationID == uuid.Nil {
		return uuid.Nil, false, fmt.Errorf("admit audio upgrade: winning source association changed: %w", ErrPublicationAdmissionConflict)
	}
	trackID, changed, err := applyTrackSourceSelection(ctx, tx, request.PositionID, *request.Source, *input.Audio,
		FirstPublicationSourceSelection{AssociationID: associationID, Associations: snapshot.SourceAssociations}, "admit audio upgrade")
	if err != nil {
		return uuid.Nil, false, err
	}
	if err := validateAndLockPublicationSource(ctx, tx, request.PositionID, *request.Source, *input.Audio); err != nil {
		return uuid.Nil, false, err
	}
	return trackID, changed, nil
}
