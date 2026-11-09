package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// recoverInterruptedTagRewrite adopts a validated same-path native rewrite left
// by an interrupted delivery. The output is copied to a private inspection file
// and never used as a candidate or transformed again.
func (executor *PublicationExecutor) recoverInterruptedTagRewrite(
	ctx context.Context,
	attempt *persistence.TrackPublicationAttempt,
	input persistence.PublicationInputs,
	guard persistence.PublicationDeliveryGuard,
	outputRoot string,
	root sourcefs.OutputDirectory,
	commands *simplePublicationTrackingRunner,
) (bool, error) {
	manualLyricsRewrite := input.ManualLyrics != nil && input.Rewrite != nil &&
		(input.Rewrite.AudioFacts.Container == publication.AudioContainerFLAC && input.Rewrite.AudioFacts.Codec == "flac" ||
			input.Rewrite.AudioFacts.Container == publication.AudioContainerMKA && input.Rewrite.AudioFacts.Codec == "mp3") &&
		input.Rewrite.AudioFacts.SupportedSourceContainer && input.Rewrite.Ownership.SHA256 != ""
	if attempt == nil || attempt.ResumeMode != persistence.PublicationResumeModePrepare ||
		input.Version != persistence.PublicationInputsVersion || input.IntentKind != "rewrite" ||
		input.Format != publication.AudioFormatSource || input.Rewrite == nil || input.Audio != nil ||
		input.AudioUpgrade != nil || input.ProviderTags != nil || input.ManualLyrics != nil && !manualLyricsRewrite ||
		input.ManualCover != nil && !isManualCoverOnlyRewriteInput(input) || input.AutomaticLyricsRetry != nil || input.SourceRepublish != nil ||
		input.InitialLyrics != nil || attempt.ExpectedGeneration == nil ||
		*attempt.ExpectedGeneration != input.Rewrite.CurrentGeneration ||
		attempt.PositionID != guard.ExpectedPositionID || attempt.Epoch != guard.Epoch ||
		attempt.CandidateGeneration != guard.CandidateGeneration || input.Validate() != nil {
		return false, nil
	}
	var coverReceipt *persistence.PublicationAttemptCoverInput
	if input.ManualCover != nil {
		receipt, valid, err := executor.repository.PublicationAttemptCoverInputForDelivery(ctx, guard)
		if err != nil {
			return false, fmt.Errorf("read interrupted cover-rewrite receipt: %w", err)
		}
		if !valid {
			return false, nil
		}
		coverReceipt = &receipt
	}
	target, nodes, err := publicationTarget(attempt.NamespaceManifest)
	if err != nil {
		return false, err
	}
	if !publicationTargetIsExactlyReserved(target, nodes) || target.RelativePath != input.Rewrite.RelativePath {
		return false, nil
	}
	parent, chain, err := openExistingPublicationParent(ctx, root, target.RelativePath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open interrupted tag-rewrite target parent: %w", err)
	}
	defer closeOutputChain(chain)
	mutation, ok := parent.(sourcefs.OutputMutation)
	if !ok {
		return false, errors.New("native interrupted tag-rewrite inspection is unavailable")
	}
	name := path.Base(target.RelativePath)
	file, err := mutation.OpenExistingRegular(ctx, name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open interrupted tag-rewrite target: %w", err)
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat(ctx)
	if err != nil {
		return false, fmt.Errorf("stat interrupted tag-rewrite target: %w", err)
	}
	if !before.Mode().IsRegular() || before.Size() < 0 {
		return false, nil
	}
	targetPath := filepath.Join(outputRoot, filepath.FromSlash(target.RelativePath))
	if !filepath.IsAbs(targetPath) || filepath.Clean(targetPath) != targetPath {
		return false, errors.New("interrupted tag-rewrite target path is invalid")
	}
	tempDir, inspectionPath, digest, err := snapshotInterruptedPublicationTarget(ctx, file, before, filepath.Ext(targetPath))
	if err != nil {
		return false, fmt.Errorf("snapshot interrupted tag-rewrite target: %w", err)
	}
	defer func() {
		if !commands.QuiescenceUnknown() {
			_ = os.RemoveAll(tempDir)
		}
	}()
	if commands.QuiescenceUnknown() {
		return false, errors.New("publication command quiescence is unknown")
	}
	resolver := NewPublicationPinnedToolsResolver(executor.repository, simplePublicationToolCommands{runner: commands}, executor.platform)
	_, validTools, err := resolver.Resolve(ctx, guard, input.Tools)
	if err != nil || !validTools {
		return false, errors.Join(err, errors.New("pinned publication tools are unavailable"))
	}
	validator := executor.stageValidator
	if validator == nil {
		real := RealPublicationStageValidator{MetadataReader: metadata.Reader{}, Tools: NewGuardedPublicationPinnedTools(resolver, guard)}
		if runner, ok := commands.PublicationCommandRunner().(PublicationFileCommandRunner); ok {
			real.Probe = func(ctx context.Context, executable string, input sourcefs.RegularFile, container publication.AudioContainer) ([]byte, error) {
				demuxer, _ := publication.InputDemuxer(container)
				request, err := tools.BuildPublicationFileCommandRequest(executable, demuxer, nil)
				if err != nil {
					return nil, err
				}
				var output []byte
				err = input.Borrow(ctx, func(file *os.File) error {
					request.Input = file
					result, err := runner.RunFile(ctx, request)
					output = result.Output
					return err
				})
				return output, err
			}
		}
		validator = real
	}
	inspectionInput := input
	if input.ManualLyrics != nil {
		inspectionInput = input.Clone()
		tags, _, err := optionalLyricsTags(input.DesiredTags, input.ManualLyrics)
		if err != nil {
			return false, nil
		}
		inspectionInput.DesiredTags = tags
		inspectionInput.ManualLyrics = nil
	}
	observation, err := validator.Validate(ctx, inspectionPath, inspectionInput)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if commands.QuiescenceUnknown() {
			return false, errors.Join(err, errors.New("publication command quiescence is unknown"))
		}
		if isDefinitePublicationStageMismatch(err) {
			return false, nil
		}
		return false, fmt.Errorf("validate interrupted tag-rewrite target: %w", err)
	}
	if observation.SHA256 != digest || !rewriteObservationPreservesAudio(observation.Audio, input.Rewrite.AudioFacts) {
		return false, nil
	}
	var coverEmbedding *persistence.PublicationCoverEmbeddingProjection
	if coverReceipt != nil {
		assets, err := persistence.DecodePublicationAssetOutcomes(observation.Assets)
		if err != nil {
			return false, fmt.Errorf("decode interrupted cover-rewrite assets: %w", err)
		}
		coverEmbedding, err = interruptedCoverEmbedding(ctx, tempDir, *coverReceipt, attempt.CandidateGeneration, assets)
		if err != nil {
			return false, fmt.Errorf("observe interrupted cover-rewrite outcome: %w", err)
		}
	}
	if commands.QuiescenceUnknown() {
		return false, errors.New("publication command quiescence is unknown")
	}
	if _, err := verifyInterruptedSourceRepublishTarget(ctx, root, chain, mutation, name, file, before); err != nil {
		return false, err
	}
	if err := executor.checkSimplePublicationCurrent(ctx, guard, input); err != nil {
		return false, err
	}
	current, err := executor.repository.CheckPublicationResultCurrent(ctx, guard)
	if err != nil || !current {
		return false, errors.Join(err, errors.New("publication authority changed during interrupted tag-rewrite recovery"))
	}
	replaceAllowed, err := executor.repository.CheckPublicationTargetReplacement(ctx, guard, target.RelativePath)
	if err != nil || !replaceAllowed {
		return false, errors.Join(err, errors.New("publication namespace authority changed during interrupted tag-rewrite recovery"))
	}
	finalInfo, err := verifyInterruptedSourceRepublishTarget(ctx, root, chain, mutation, name, file, before)
	if err != nil {
		return false, err
	}
	size, mtime := finalInfo.Size(), finalInfo.ModTime().UnixMicro()
	actual := persistence.PublicationStageObservation{
		Version: 1, RelativePath: target.RelativePath,
		Ownership:     persistence.ArtifactOwnership{ExpectedSize: &size, ExpectedMTime: &mtime},
		PayloadSHA256: digest, ActualAudio: observation.Audio, ActualMetadata: observation.Metadata,
		ActualAssets: observation.Assets, QualityPolicyVersion: observation.QualityPolicyVersion,
	}
	result := persistence.PublicationResult{
		Observation: actual,
		InstalledOwnership: persistence.PublicationCurrentOwnership{Version: persistence.PublicationCurrentOwnershipVersion,
			Identity: persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: digest}},
		CoverEmbedding: coverEmbedding,
	}
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	committed, err := executor.repository.CommitPublicationResult(commitCtx, guard, result)
	if err != nil || !committed {
		return false, errors.Join(err, errors.New("recovered tag-rewrite result was not committed"))
	}
	return true, nil
}

func interruptedCoverEmbedding(
	ctx context.Context,
	tempDir string,
	receipt persistence.PublicationAttemptCoverInput,
	generation uuid.UUID,
	assets persistence.PublicationAssetOutcomes,
) (*persistence.PublicationCoverEmbeddingProjection, error) {
	if receipt.State != "value" || receipt.Generation == nil || receipt.MIME == nil || receipt.Size == nil || len(receipt.SHA256) == 0 {
		return nil, errors.New("interrupted cover-rewrite receipt is incomplete")
	}
	projection := &persistence.PublicationCoverEmbeddingProjection{
		Version:    persistence.PublicationCoverEmbeddingProjectionVersion,
		Generation: generation, ReceiptOperationID: receipt.OperationID,
		ReceiptReleaseID: receipt.ReleaseID, ReceiptRevision: receipt.Revision, ReceiptMode: receipt.Mode,
		RequestedGeneration: *receipt.Generation, RequestedSHA256: hex.EncodeToString(receipt.SHA256),
	}
	switch assets.Cover.State {
	case persistence.PublicationAssetAbsent:
		projection.Version = persistence.PublicationCoverEmbeddingDerivativeProjectionVersion
		projection.Outcome = "absent_after_interruption"
		projection.WarningCode = "cover_absent_after_interruption"
	case persistence.PublicationAssetEmbedded:
		if assets.Cover.SHA256 == projection.RequestedSHA256 && assets.Cover.MIME == *receipt.MIME && assets.Cover.SizeBytes != nil && *assets.Cover.SizeBytes == *receipt.Size {
			projection.Outcome = "verified"
			return projection, nil
		}
		thumbnail, err := publicationCoverThumbnail(ctx, tempDir, &receipt)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(thumbnail.Data)
		size := int64(len(thumbnail.Data))
		if assets.Cover.SHA256 == hex.EncodeToString(digest[:]) && assets.Cover.MIME == thumbnail.MIME && assets.Cover.SizeBytes != nil && *assets.Cover.SizeBytes == size {
			projection.Version = persistence.PublicationCoverEmbeddingDerivativeProjectionVersion
			projection.Outcome = "verified"
			projection.EmbeddedSHA256 = hex.EncodeToString(digest[:])
			projection.EmbeddedMIME = thumbnail.MIME
			projection.EmbeddedSize = &size
			return projection, nil
		}
		projection.Outcome = "mismatch"
		projection.WarningCode = "cover_mismatch"
	default:
		return nil, errors.New("interrupted cover-rewrite asset state is invalid")
	}
	return projection, nil
}

func snapshotInterruptedPublicationTarget(ctx context.Context, file sourcefs.RegularFile, before os.FileInfo, extension string) (string, string, string, error) {
	tempDir, err := os.MkdirTemp("", "publication-inspection-")
	if err != nil {
		return "", "", "", fmt.Errorf("create private publication inspection directory: %w", err)
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(tempDir)
		}
	}()
	inspectionPath := filepath.Join(tempDir, "inspection"+extension)
	inspection, err := os.OpenFile(inspectionPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return tempDir, "", "", fmt.Errorf("create private publication inspection snapshot: %w", err)
	}
	hash := sha256.New()
	copyErr := file.Borrow(ctx, func(native *os.File) error {
		written, err := io.CopyN(io.MultiWriter(inspection, hash), inspectionContextReader{ctx: ctx, reader: native}, before.Size())
		if err != nil {
			return err
		}
		if written != before.Size() {
			return io.ErrUnexpectedEOF
		}
		var extra [1]byte
		n, err := native.Read(extra[:])
		if n != 0 {
			return errors.New("interrupted publication target grew during inspection copy")
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		return ctx.Err()
	})
	if copyErr == nil {
		copyErr = inspection.Sync()
	}
	copyErr = errors.Join(copyErr, inspection.Close())
	if copyErr != nil {
		return tempDir, "", "", copyErr
	}
	success = true
	return tempDir, inspectionPath, hex.EncodeToString(hash.Sum(nil)), nil
}

func rewriteObservationPreservesAudio(raw json.RawMessage, expected persistence.PersistedAudioFacts) bool {
	var actual struct {
		Version int                             `json:"version"`
		Facts   persistence.PersistedAudioFacts `json:"facts"`
	}
	if json.Unmarshal(raw, &actual) != nil || actual.Version != 1 {
		return false
	}
	facts := actual.Facts
	return facts.Codec == expected.Codec && facts.Container == expected.Container &&
		matchesFrozenAudioMeasurement(facts.Channels, expected.Channels) &&
		matchesFrozenAudioMeasurement(facts.SampleRate, expected.SampleRate) &&
		matchesFrozenAudioMeasurement(facts.BitDepth, expected.BitDepth) &&
		(expected.ChannelLayout == "" || facts.ChannelLayout == expected.ChannelLayout)
}

func matchesFrozenAudioMeasurement(actual, frozen *int) bool {
	return frozen == nil || actual != nil && *actual == *frozen
}

// recoverInterruptedFirstAudio adopts only an output left by an interrupted
// first-audio delivery. The private snapshot is inspection input, not a stage:
// it is never transformed, tagged, or installed.
func (executor *PublicationExecutor) recoverInterruptedFirstAudio(
	ctx context.Context,
	attempt *persistence.TrackPublicationAttempt,
	input persistence.PublicationInputs,
	guard persistence.PublicationDeliveryGuard,
	outputRoot string,
	root sourcefs.OutputDirectory,
	commands *simplePublicationTrackingRunner,
) (bool, error) {
	if input.Version != persistence.PublicationInputsVersion || input.IntentKind != "audio" || input.Audio == nil ||
		input.Rewrite != nil || input.AudioUpgrade != nil || input.ManualLyrics != nil || input.ManualCover != nil ||
		input.AutomaticLyricsRetry != nil || input.ProviderTags != nil || attempt.ExpectedGeneration != nil {
		return false, nil
	}
	cover, found, err := executor.repository.PublicationAttemptCoverInputForDelivery(ctx, guard)
	if err != nil {
		return false, fmt.Errorf("read interrupted publication cover receipt: %w", err)
	}
	if !found || cover.State != "absent" && cover.State != "value" {
		return false, nil
	}
	target, nodes, err := publicationTarget(attempt.NamespaceManifest)
	if err != nil {
		return false, err
	}
	if !publicationTargetIsExactlyReserved(target, nodes) {
		return false, errors.New("interrupted publication target is not exactly reserved by its frozen namespace")
	}
	parent, chain, err := openExistingPublicationParent(ctx, root, target.RelativePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open interrupted publication target parent: %w", err)
	}
	defer closeOutputChain(chain)
	mutation, ok := parent.(sourcefs.OutputMutation)
	if !ok {
		return false, errors.New("native interrupted publication inspection is unavailable")
	}
	name := path.Base(target.RelativePath)
	file, err := mutation.OpenExistingRegular(ctx, name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open interrupted publication target: %w", err)
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat(ctx)
	if err != nil {
		return false, fmt.Errorf("stat interrupted publication target: %w", err)
	}
	if !before.Mode().IsRegular() || before.Size() < 0 {
		return false, nil
	}
	absolute := filepath.Join(outputRoot, filepath.FromSlash(target.RelativePath))
	if !filepath.IsAbs(absolute) || filepath.Clean(absolute) != absolute {
		return false, errors.New("interrupted publication target path is invalid")
	}
	tempDir, inspectionPath, digest, err := snapshotInterruptedPublicationTarget(ctx, file, before, filepath.Ext(target.RelativePath))
	if err != nil {
		return false, fmt.Errorf("snapshot interrupted publication target: %w", err)
	}
	defer func() {
		if !commands.QuiescenceUnknown() {
			_ = os.RemoveAll(tempDir)
		}
	}()

	resolver := NewPublicationPinnedToolsResolver(executor.repository, simplePublicationToolCommands{runner: commands}, executor.platform)
	_, validTools, err := resolver.Resolve(ctx, guard, input.Tools)
	if err != nil || !validTools {
		return false, errors.Join(err, errors.New("pinned publication tools are unavailable"))
	}
	pinned := NewGuardedPublicationPinnedTools(resolver, guard)
	validator := executor.stageValidator
	if validator == nil {
		real := RealPublicationStageValidator{MetadataReader: metadata.Reader{}, Tools: pinned}
		if runner, ok := commands.PublicationCommandRunner().(PublicationFileCommandRunner); ok {
			real.Probe = func(ctx context.Context, executable string, input sourcefs.RegularFile, container publication.AudioContainer) ([]byte, error) {
				demuxer, _ := publication.InputDemuxer(container)
				request, err := tools.BuildPublicationFileCommandRequest(executable, demuxer, nil)
				if err != nil {
					return nil, err
				}
				var output []byte
				err = input.Borrow(ctx, func(file *os.File) error {
					request.Input = file
					result, err := runner.RunFile(ctx, request)
					output = result.Output
					return err
				})
				return output, err
			}
		}
		validator = real
	}
	firstAudioValidator, ok := validator.(interface {
		ValidateFirstAudioCover(context.Context, string, persistence.PublicationInputs, persistence.PublicationCoverOutcome) (PublicationStageObservation, error)
	})
	if !ok {
		return false, errors.New("first-audio recovery validator is unavailable")
	}
	capture, err := (metadata.Reader{}).Read(ctx, inspectionPath, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		return false, fmt.Errorf("read interrupted publication metadata: %w", err)
	}
	if capture.Tags == nil || !equalTags(withoutLyrics(capture.Tags), withoutLyrics(input.DesiredTags)) {
		return false, nil
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		return false, fmt.Errorf("observe interrupted publication assets: %w", err)
	}
	expectedCover := persistence.PublicationCoverOutcome{State: persistence.PublicationAssetAbsent}
	var coverEmbedding *persistence.PublicationCoverEmbeddingProjection
	if cover.State == "value" {
		if cover.Generation == nil {
			return false, errors.New("interrupted publication cover receipt has no generation")
		}
		projection := persistence.PublicationCoverEmbeddingProjection{
			Version:             persistence.PublicationCoverEmbeddingDerivativeProjectionVersion,
			Generation:          guard.CandidateGeneration,
			ReceiptOperationID:  cover.OperationID,
			ReceiptReleaseID:    cover.ReleaseID,
			ReceiptRevision:     cover.Revision,
			ReceiptMode:         cover.Mode,
			RequestedGeneration: *cover.Generation,
			RequestedSHA256:     hex.EncodeToString(cover.SHA256),
		}
		if assets.Cover.State == persistence.PublicationAssetEmbedded {
			derivative, err := publicationCoverThumbnail(ctx, tempDir, &cover)
			if err != nil {
				return false, fmt.Errorf("derive interrupted publication cover: %w", err)
			}
			derivativeDigest := sha256.Sum256(derivative.Data)
			if assets.Cover.SHA256 != hex.EncodeToString(derivativeDigest[:]) || assets.Cover.MIME != derivative.MIME ||
				assets.Cover.SizeBytes == nil || *assets.Cover.SizeBytes != int64(len(derivative.Data)) {
				return false, nil
			}
			expectedCover = persistence.PublicationCoverOutcome{
				State: persistence.PublicationAssetEmbedded, SHA256: hex.EncodeToString(derivativeDigest[:]),
				MIME: derivative.MIME, SizeBytes: assets.Cover.SizeBytes,
			}
			projection.Outcome = "verified"
			projection.EmbeddedSHA256 = expectedCover.SHA256
			projection.EmbeddedMIME = expectedCover.MIME
			projection.EmbeddedSize = expectedCover.SizeBytes
		} else {
			projection.Outcome = "absent_after_interruption"
			projection.WarningCode = "cover_absent_after_interruption"
		}
		coverEmbedding = &projection
	} else if assets.Cover.State != persistence.PublicationAssetAbsent {
		return false, nil
	}
	if lyrics := capture.Tags["LYRICS"]; len(lyrics) > 0 && input.InitialLyrics != nil && input.InitialLyrics.Desired != nil &&
		(input.InitialLyrics.Desired.Authority == "manual" || input.InitialLyrics.Desired.State == "explicit_empty") {
		return false, nil
	}
	// Lyrics are observed output, not acquisition history. Permit exact read-back
	// while validating a detached input; never persist this projection as frozen input.
	inspectionInput := input.Clone()
	inspectionInput.DesiredTags = cloneTagMap(capture.Tags)
	observation, err := firstAudioValidator.ValidateFirstAudioCover(ctx, inspectionPath, inspectionInput, expectedCover)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		if commands.QuiescenceUnknown() {
			return false, errors.Join(err, errors.New("publication command quiescence is unknown"))
		}
		if isDefinitePublicationStageMismatch(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect interrupted publication target: %w", err)
	}
	if observation.SHA256 != digest {
		return false, errors.New("interrupted publication inspection snapshot changed during validation")
	}

	if commands.QuiescenceUnknown() {
		return false, errors.New("publication command quiescence is unknown")
	}
	if err := verifyPublicationParentChain(ctx, root, chain); err != nil {
		return false, err
	}
	after, err := file.Stat(ctx)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return false, errors.Join(err, errors.New("interrupted publication target changed during inspection"))
	}
	reopened, err := mutation.OpenExistingRegular(ctx, name)
	if err != nil {
		return false, fmt.Errorf("reopen interrupted publication target: %w", err)
	}
	reopenedInfo, statErr := reopened.Stat(ctx)
	closeErr := reopened.Close()
	if err := errors.Join(statErr, closeErr); err != nil || !os.SameFile(before, reopenedInfo) || before.Size() != reopenedInfo.Size() || !before.ModTime().Equal(reopenedInfo.ModTime()) {
		return false, errors.Join(err, errors.New("interrupted publication target was replaced during inspection"))
	}
	if err := executor.checkSimplePublicationCurrent(ctx, guard, input); err != nil {
		return false, err
	}
	current, err := executor.repository.CheckPublicationResultCurrent(ctx, guard)
	if err != nil || !current {
		return false, errors.Join(err, errors.New("publication authority changed during interrupted recovery"))
	}
	replaceAllowed, err := executor.repository.CheckPublicationTargetReplacement(ctx, guard, target.RelativePath)
	if err != nil || !replaceAllowed {
		return false, errors.Join(err, errors.New("publication namespace authority changed during interrupted recovery"))
	}
	size, mtime := before.Size(), before.ModTime().UnixMicro()
	actual := persistence.PublicationStageObservation{
		Version: 1, RelativePath: target.RelativePath,
		Ownership:     persistence.ArtifactOwnership{ExpectedSize: &size, ExpectedMTime: &mtime},
		PayloadSHA256: digest, ActualAudio: observation.Audio, ActualMetadata: observation.Metadata,
		ActualAssets: observation.Assets, QualityPolicyVersion: observation.QualityPolicyVersion,
	}
	result := persistence.PublicationResult{
		Observation: actual,
		InstalledOwnership: persistence.PublicationCurrentOwnership{Version: persistence.PublicationCurrentOwnershipVersion,
			Identity: persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: digest}},
		CoverEmbedding: coverEmbedding,
	}
	commitCtx, cancelCommit := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelCommit()
	committed, err := executor.repository.CommitPublicationResult(commitCtx, guard, result)
	if err != nil || !committed {
		return false, errors.Join(err, errors.New("recovered publication result was not committed"))
	}
	return true, nil
}

// recoverInterruptedSourceRepublish adopts only a validated same-path target
// left by an interrupted v4 source-republish delivery. The target is copied to
// a private inspection file, never used as a candidate; an unsuitable target can
// only be replaced after full source-backed preparation.
func (executor *PublicationExecutor) recoverInterruptedSourceRepublish(
	ctx context.Context,
	attempt *persistence.TrackPublicationAttempt,
	input persistence.PublicationInputs,
	guard persistence.PublicationDeliveryGuard,
	outputRoot string,
	root sourcefs.OutputDirectory,
	commands *simplePublicationTrackingRunner,
	authorizedObservedReplacement *persistence.PublicationFileIdentity,
) (recovered, resumePreparation bool, resultErr error) {
	if attempt == nil || attempt.ResumeMode != persistence.PublicationResumeModeInspectSourceRepublishTarget ||
		input.Version != persistence.PublicationSourceRepublishInputsVersion || input.IntentKind != "audio" ||
		input.Audio == nil || input.SourceRepublish == nil || input.Rewrite != nil || input.AudioUpgrade != nil ||
		input.ManualLyrics != nil || input.ManualCover != nil || input.AutomaticLyricsRetry != nil || input.ProviderTags != nil ||
		attempt.ExpectedGeneration == nil || *attempt.ExpectedGeneration != input.SourceRepublish.Current.Generation ||
		attempt.CandidateGeneration == uuid.Nil || attempt.CandidateGeneration != guard.CandidateGeneration ||
		attempt.PositionID != guard.ExpectedPositionID || attempt.Epoch != guard.Epoch || input.Validate() != nil {
		return false, false, errors.New("source-republish recovery requires valid frozen v4 inputs")
	}
	current := input.SourceRepublish.Current
	if current.Generation == uuid.Nil || current.RelativePath == "" ||
		current.Ownership.SizeBytes == nil || current.Ownership.MTime == nil || current.Ownership.SHA256 == "" {
		return false, false, errors.New("source-republish recovery target does not match frozen CURRENT ownership")
	}
	target, nodes, err := publicationTarget(attempt.NamespaceManifest)
	if err != nil {
		return false, false, err
	}
	if !publicationTargetIsExactlyReserved(target, nodes) {
		return false, false, errors.New("source-republish recovery target is not exactly reserved by its frozen namespace")
	}
	parent, chain, err := openExistingPublicationParent(ctx, root, target.RelativePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			resumed, resumeErr := executor.resumeMissingSourceRepublishPreparation(ctx, guard)
			if resumeErr != nil || !resumed {
				return false, false, errors.Join(resumeErr, errors.New("missing source-republish target parent did not authorize preparation"))
			}
			return false, true, nil
		}
		return false, false, fmt.Errorf("open interrupted source-republish target parent: %w", err)
	}
	defer closeOutputChain(chain)
	mutation, ok := parent.(sourcefs.OutputMutation)
	if !ok {
		return false, false, errors.New("native source-republish target inspection is unavailable")
	}
	name := path.Base(target.RelativePath)
	file, err := mutation.OpenExistingRegular(ctx, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			resumed, resumeErr := executor.resumeMissingSourceRepublishPreparation(ctx, guard)
			if resumeErr != nil || !resumed {
				return false, false, errors.Join(resumeErr, errors.New("missing source-republish target did not authorize preparation"))
			}
			return false, true, nil
		}
		return false, false, fmt.Errorf("open interrupted source-republish target: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	before, err := file.Stat(ctx)
	if err != nil || !before.Mode().IsRegular() || before.Size() < 0 {
		return false, false, errors.Join(err, errors.New("interrupted source-republish target is not a regular file"))
	}
	targetPath := filepath.Join(outputRoot, filepath.FromSlash(target.RelativePath))
	if !filepath.IsAbs(targetPath) || filepath.Clean(targetPath) != targetPath {
		return false, false, errors.New("interrupted source-republish target path is invalid")
	}
	inspectionDir, err := os.MkdirTemp("", "source-republish-inspection-")
	if err != nil {
		return false, false, fmt.Errorf("create private source-republish inspection directory: %w", err)
	}
	defer func() {
		if !commands.QuiescenceUnknown() {
			resultErr = errors.Join(resultErr, os.RemoveAll(inspectionDir))
		}
	}()
	inspectionPath := filepath.Join(inspectionDir, "inspection"+filepath.Ext(targetPath))
	inspection, err := os.OpenFile(inspectionPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false, false, fmt.Errorf("create private source-republish inspection snapshot: %w", err)
	}
	hash := sha256.New()
	copyErr := file.Borrow(ctx, func(native *os.File) error {
		writer := io.MultiWriter(inspection, hash)
		source := inspectionContextReader{ctx: ctx, reader: native}
		written, err := io.CopyN(writer, source, before.Size())
		if err != nil {
			return err
		}
		if written != before.Size() {
			return io.ErrUnexpectedEOF
		}
		var extra [1]byte
		n, err := native.Read(extra[:])
		if n != 0 {
			return errors.New("interrupted source-republish target grew during inspection copy")
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		return ctx.Err()
	})
	if copyErr == nil {
		copyErr = inspection.Sync()
	}
	copyErr = errors.Join(copyErr, inspection.Close())
	if copyErr != nil {
		return false, false, fmt.Errorf("snapshot interrupted source-republish target: %w", copyErr)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	size, mtime := before.Size(), before.ModTime().UnixMicro()
	observedIdentity := persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: digest}
	resumeFromObservedTarget := func() (bool, bool, error) {
		identity, err := executor.resumeObservedSourceRepublishPreparation(ctx, guard, input, target.RelativePath, observedIdentity)
		if err != nil {
			return false, false, err
		}
		*authorizedObservedReplacement = identity
		return false, true, nil
	}
	if target.RelativePath == current.RelativePath && digest == current.Ownership.SHA256 {
		if !samePublicationFileIdentity(observedIdentity, current.Ownership) {
			if _, err := verifyInterruptedSourceRepublishTarget(ctx, root, chain, mutation, name, file, before); err != nil {
				return false, false, err
			}
			return resumeFromObservedTarget()
		}
		if _, err := verifyInterruptedSourceRepublishTarget(ctx, root, chain, mutation, name, file, before); err != nil {
			return false, false, err
		}
		resumer, ok := executor.repository.(interface {
			ResumeSourceRepublishPreparation(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationFileIdentity) (bool, error)
		})
		if !ok {
			return false, false, errors.New("source-republish preparation resume capability is unavailable")
		}
		allowed, err := resumer.ResumeSourceRepublishPreparation(ctx, guard, observedIdentity)
		if err != nil || !allowed {
			return false, false, errors.Join(err, errors.New("unchanged CURRENT did not authorize source-republish preparation"))
		}
		return false, true, nil
	}
	if commands.QuiescenceUnknown() {
		return false, false, errors.New("publication command quiescence is unknown")
	}

	resolver := NewPublicationPinnedToolsResolver(executor.repository, simplePublicationToolCommands{runner: commands}, executor.platform)
	_, validTools, err := resolver.Resolve(ctx, guard, input.Tools)
	if err != nil || !validTools {
		return false, false, errors.Join(err, errors.New("pinned source-republish tools are unavailable"))
	}
	pinned := NewGuardedPublicationPinnedTools(resolver, guard)
	validator := executor.stageValidator
	if validator == nil {
		real := RealPublicationStageValidator{MetadataReader: metadata.Reader{}, Tools: pinned}
		if runner, ok := commands.PublicationCommandRunner().(PublicationFileCommandRunner); ok {
			real.Probe = func(ctx context.Context, executable string, input sourcefs.RegularFile, container publication.AudioContainer) ([]byte, error) {
				demuxer, _ := publication.InputDemuxer(container)
				request, err := tools.BuildPublicationFileCommandRequest(executable, demuxer, nil)
				if err != nil {
					return nil, err
				}
				var output []byte
				err = input.Borrow(ctx, func(file *os.File) error {
					request.Input = file
					result, err := runner.RunFile(ctx, request)
					output = result.Output
					return err
				})
				return output, err
			}
		}
		validator = real
	}
	sourceRepublishValidator, ok := validator.(interface {
		ValidateSourceRepublish(context.Context, string, persistence.PublicationInputs, persistence.PublicationCoverOutcome) (PublicationStageObservation, error)
	})
	if !ok {
		return false, false, errors.New("source-republish recovery validator is unavailable")
	}
	capture, err := (metadata.Reader{}).Read(ctx, inspectionPath, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		return false, false, fmt.Errorf("read interrupted source-republish metadata: %w", err)
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		return false, false, fmt.Errorf("observe interrupted source-republish assets: %w", err)
	}
	expectedCover, coverReceipt, err := sourceRepublishRecoveryCoverResult(input.SourceRepublish.Cover, assets.Cover)
	if err != nil {
		if ctx.Err() == nil && isDefinitePublicationStageMismatch(err) && !commands.QuiescenceUnknown() {
			if _, verifyErr := verifyInterruptedSourceRepublishTarget(ctx, root, chain, mutation, name, file, before); verifyErr != nil {
				return false, false, errors.Join(err, verifyErr)
			}
			return resumeFromObservedTarget()
		}
		return false, false, err
	}
	observation, err := sourceRepublishValidator.ValidateSourceRepublish(ctx, inspectionPath, input, expectedCover)
	if err != nil {
		if ctx.Err() != nil || !isDefinitePublicationStageMismatch(err) {
			return false, false, fmt.Errorf("validate interrupted source-republish target: %w", err)
		}
		if commands.QuiescenceUnknown() {
			return false, false, errors.New("publication command quiescence is unknown")
		}
		if _, verifyErr := verifyInterruptedSourceRepublishTarget(ctx, root, chain, mutation, name, file, before); verifyErr != nil {
			return false, false, errors.Join(err, verifyErr)
		}
		return resumeFromObservedTarget()
	}
	if observation.SHA256 != digest {
		return false, false, errors.New("interrupted source-republish snapshot changed during validation")
	}
	if commands.QuiescenceUnknown() {
		return false, false, errors.New("publication command quiescence is unknown")
	}
	if _, err := verifyInterruptedSourceRepublishTarget(ctx, root, chain, mutation, name, file, before); err != nil {
		return false, false, err
	}
	if err := executor.checkSimplePublicationCurrent(ctx, guard, input); err != nil {
		return false, false, err
	}
	currentAuthority, err := executor.repository.CheckPublicationResultCurrent(ctx, guard)
	if err != nil || !currentAuthority {
		return false, false, errors.Join(err, errors.New("source-republish authority changed during interruption recovery"))
	}
	replaceAllowed, err := executor.repository.CheckPublicationTargetReplacement(ctx, guard, target.RelativePath)
	if err != nil || !replaceAllowed {
		return false, false, errors.Join(err, errors.New("source-republish replacement authority changed during interruption recovery"))
	}
	finalInfo, err := verifyInterruptedSourceRepublishTarget(ctx, root, chain, mutation, name, file, before)
	if err != nil {
		return false, false, err
	}
	actualAssets, err := persistence.DecodePublicationAssetOutcomes(observation.Assets)
	if err != nil {
		return false, false, fmt.Errorf("decode interrupted source-republish assets: %w", err)
	}
	lyricsReceipt := persistence.PublicationSourceRepublishLyricsObservedAfterInterruption
	if input.SourceRepublish.Cover.Derivation == "ready" {
		switch actualAssets.Cover.State {
		case persistence.PublicationAssetEmbedded:
			coverReceipt = persistence.PublicationSourceRepublishCoverVerified
		case persistence.PublicationAssetAbsent:
			coverReceipt = persistence.PublicationSourceRepublishCoverAbsentAfterInterruption
		default:
			return false, false, errors.New("interrupted source-republish cover outcome is invalid")
		}
	}
	if input.SourceRepublish.Cover.Derivation == "absent" {
		coverReceipt = persistence.PublicationSourceRepublishCoverNotRequested
	} else if input.SourceRepublish.Cover.Derivation == "optional_failure" {
		coverReceipt = persistence.PublicationSourceRepublishCoverPreparationFailed
	} else if input.SourceRepublish.Cover.Derivation != "ready" {
		return false, false, errors.New("interrupted source-republish frozen cover derivation is invalid")
	}
	size, mtime = finalInfo.Size(), finalInfo.ModTime().UnixMicro()
	observedIdentity.SizeBytes = &size
	observedIdentity.MTime = &mtime
	actual := persistence.PublicationStageObservation{
		Version: 1, RelativePath: target.RelativePath,
		Ownership:     persistence.ArtifactOwnership{ExpectedSize: &size, ExpectedMTime: &mtime},
		PayloadSHA256: digest, ActualAudio: observation.Audio, ActualMetadata: observation.Metadata,
		ActualAssets: observation.Assets, QualityPolicyVersion: input.QualityPolicyVersion,
	}
	result := persistence.PublicationResult{
		Observation:        actual,
		InstalledOwnership: persistence.PublicationCurrentOwnership{Version: persistence.PublicationCurrentOwnershipVersion, Identity: observedIdentity},
		SourceRepublishOptionalResult: &persistence.PublicationSourceRepublishOptionalResult{
			LyricsOutcome: lyricsReceipt, CoverOutcome: coverReceipt,
		},
	}
	if target.RelativePath != current.RelativePath {
		previous, err := executor.repository.ReadPublicationObsoleteOutputCandidate(ctx, guard, target.RelativePath)
		if err != nil {
			return false, false, fmt.Errorf("read superseded source-republish publication: %w", err)
		}
		if previous != nil {
			cleanup, absent, err := captureSourceRepublishObsoleteOutput(ctx, root, outputRoot, previous.Publication, previous.NamespaceClaimID)
			if err != nil {
				return false, false, fmt.Errorf("capture superseded source-republish publication: %w", err)
			}
			result.ObsoleteOutputCleanup = cleanup
			result.ObsoleteOutputAbsent = absent
		}
		// Cleanup capture may traverse a different tree branch; fence the installed
		// destination again immediately before committing its read-back.
		if _, err := verifyInterruptedSourceRepublishTarget(ctx, root, chain, mutation, name, file, before); err != nil {
			return false, false, err
		}
	}
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	committed, err := executor.repository.CommitPublicationResult(commitCtx, guard, result)
	if err != nil || !committed {
		return false, false, errors.Join(err, errors.New("recovered source-republish result was not committed"))
	}
	return true, false, nil
}

func samePublicationFileIdentity(actual, expected persistence.PublicationFileIdentity) bool {
	return actual.SizeBytes != nil && expected.SizeBytes != nil && *actual.SizeBytes == *expected.SizeBytes &&
		actual.MTime != nil && expected.MTime != nil && *actual.MTime == *expected.MTime && actual.SHA256 != "" && actual.SHA256 == expected.SHA256
}

func (executor *PublicationExecutor) resumeMissingSourceRepublishPreparation(
	ctx context.Context,
	guard persistence.PublicationDeliveryGuard,
) (bool, error) {
	resumer, ok := executor.repository.(interface {
		ResumeMissingSourceRepublishPreparation(context.Context, persistence.PublicationDeliveryGuard) (bool, error)
	})
	if !ok {
		return false, errors.New("source-republish missing-target resume capability is unavailable")
	}
	return resumer.ResumeMissingSourceRepublishPreparation(ctx, guard)
}

// resumeObservedSourceRepublishPreparation rechecks recovery authority and
// returns the observed target identity, but leaves inspection mode durable until
// installation has produced and verified a complete source-backed candidate.
func (executor *PublicationExecutor) resumeObservedSourceRepublishPreparation(
	ctx context.Context,
	guard persistence.PublicationDeliveryGuard,
	input persistence.PublicationInputs,
	target string,
	observed persistence.PublicationFileIdentity,
) (persistence.PublicationFileIdentity, error) {
	if err := executor.checkSimplePublicationCurrent(ctx, guard, input); err != nil {
		return persistence.PublicationFileIdentity{}, err
	}
	current, err := executor.repository.CheckPublicationResultCurrent(ctx, guard)
	if err != nil || !current {
		return persistence.PublicationFileIdentity{}, errors.Join(err, errors.New("source-republish authority changed during interruption recovery"))
	}
	replaceAllowed, err := executor.repository.CheckPublicationTargetReplacement(ctx, guard, target)
	if err != nil || !replaceAllowed {
		return persistence.PublicationFileIdentity{}, errors.Join(err, errors.New("source-republish replacement authority changed during interruption recovery"))
	}
	return observed, nil
}

type inspectedSourceRepublishTarget struct {
	info        os.FileInfo
	identity    persistence.PublicationFileIdentity
	observation PublicationStageObservation
	optional    persistence.PublicationSourceRepublishOptionalResult
}

// inspectSourceRepublishTargetForReprepare validates a changed target against
// the frozen result unless interrupted recovery explicitly authorized replacing
// its safely observed unsuitable target. The returned live identity fences the
// subsequent native replacement.
func (executor *PublicationExecutor) inspectSourceRepublishTargetForReprepare(
	ctx context.Context,
	input persistence.PublicationInputs,
	guard persistence.PublicationDeliveryGuard,
	mutation sourcefs.OutputMutation,
	name string,
	file sourcefs.RegularFile,
	before os.FileInfo,
	commands *simplePublicationTrackingRunner,
	authorizedObservedReplacement persistence.PublicationFileIdentity,
) (inspectedSourceRepublishTarget, error) {
	if input.SourceRepublish == nil || before == nil || !before.Mode().IsRegular() || before.Size() < 0 || commands.QuiescenceUnknown() {
		return inspectedSourceRepublishTarget{}, errors.New("source-republish target inspection is unavailable")
	}
	inspectionDir, err := os.MkdirTemp("", "source-republish-retry-inspection-")
	if err != nil {
		return inspectedSourceRepublishTarget{}, fmt.Errorf("create private source-republish retry inspection directory: %w", err)
	}
	defer func() {
		if !commands.QuiescenceUnknown() {
			_ = os.RemoveAll(inspectionDir)
		}
	}()
	inspectionPath := filepath.Join(inspectionDir, "inspection"+filepath.Ext(name))
	inspection, err := os.OpenFile(inspectionPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return inspectedSourceRepublishTarget{}, fmt.Errorf("create source-republish retry inspection snapshot: %w", err)
	}
	hash := sha256.New()
	copyErr := file.Borrow(ctx, func(native *os.File) error {
		if _, err := native.Seek(0, io.SeekStart); err != nil {
			return err
		}
		written, err := io.CopyN(io.MultiWriter(inspection, hash), inspectionContextReader{ctx: ctx, reader: native}, before.Size())
		if err != nil {
			return err
		}
		if written != before.Size() {
			return io.ErrUnexpectedEOF
		}
		var extra [1]byte
		n, err := native.Read(extra[:])
		if n != 0 {
			return errors.New("source-republish target grew during retry inspection")
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		return ctx.Err()
	})
	if copyErr == nil {
		copyErr = inspection.Sync()
	}
	copyErr = errors.Join(copyErr, inspection.Close())
	if copyErr != nil {
		return inspectedSourceRepublishTarget{}, fmt.Errorf("snapshot source-republish retry target: %w", copyErr)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	size, mtime := before.Size(), before.ModTime().UnixMicro()
	observed := persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: digest}
	if _, err := verifySimpleSourceRepublishTarget(ctx, mutation, name, file, before); err != nil {
		return inspectedSourceRepublishTarget{}, err
	}
	if authorizedObservedReplacement.SHA256 != "" {
		if commands.QuiescenceUnknown() {
			return inspectedSourceRepublishTarget{}, errors.New("publication command quiescence is unknown")
		}
		if !samePublicationFileIdentity(observed, authorizedObservedReplacement) {
			return inspectedSourceRepublishTarget{}, errors.New("source-republish target changed after interrupted recovery inspection")
		}
		return inspectedSourceRepublishTarget{info: before, identity: observed}, nil
	}
	resolver := NewPublicationPinnedToolsResolver(executor.repository, simplePublicationToolCommands{runner: commands}, executor.platform)
	_, validTools, err := resolver.Resolve(ctx, guard, input.Tools)
	if err != nil || !validTools {
		return inspectedSourceRepublishTarget{}, errors.Join(err, errors.New("pinned source-republish tools are unavailable"))
	}
	validator := executor.stageValidator
	if validator == nil {
		real := RealPublicationStageValidator{MetadataReader: metadata.Reader{}, Tools: NewGuardedPublicationPinnedTools(resolver, guard)}
		if runner, ok := commands.PublicationCommandRunner().(PublicationFileCommandRunner); ok {
			real.Probe = func(ctx context.Context, executable string, input sourcefs.RegularFile, container publication.AudioContainer) ([]byte, error) {
				demuxer, _ := publication.InputDemuxer(container)
				request, err := tools.BuildPublicationFileCommandRequest(executable, demuxer, nil)
				if err != nil {
					return nil, err
				}
				var output []byte
				err = input.Borrow(ctx, func(file *os.File) error {
					request.Input = file
					result, err := runner.RunFile(ctx, request)
					output = result.Output
					return err
				})
				return output, err
			}
		}
		validator = real
	}
	sourceValidator, ok := validator.(interface {
		ValidateSourceRepublish(context.Context, string, persistence.PublicationInputs, persistence.PublicationCoverOutcome) (PublicationStageObservation, error)
	})
	if !ok {
		return inspectedSourceRepublishTarget{}, errors.New("source-republish retry validator is unavailable")
	}
	capture, err := (metadata.Reader{}).Read(ctx, inspectionPath, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		return inspectedSourceRepublishTarget{}, fmt.Errorf("read source-republish retry metadata: %w", err)
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		return inspectedSourceRepublishTarget{}, fmt.Errorf("observe source-republish retry assets: %w", err)
	}
	expectedCover, coverOutcome, err := sourceRepublishRecoveryCoverResult(input.SourceRepublish.Cover, assets.Cover)
	if err != nil {
		return inspectedSourceRepublishTarget{}, err
	}
	observation, err := sourceValidator.ValidateSourceRepublish(ctx, inspectionPath, input, expectedCover)
	if err != nil || observation.SHA256 != digest {
		return inspectedSourceRepublishTarget{}, errors.Join(err, errors.New("source-republish target differs from the frozen result"))
	}
	if commands.QuiescenceUnknown() {
		return inspectedSourceRepublishTarget{}, errors.New("publication command quiescence is unknown")
	}
	finalInfo, err := verifySimpleSourceRepublishTarget(ctx, mutation, name, file, before)
	if err != nil {
		return inspectedSourceRepublishTarget{}, err
	}
	size, mtime = finalInfo.Size(), finalInfo.ModTime().UnixMicro()
	observed.SizeBytes = &size
	observed.MTime = &mtime
	actualAssets, err := persistence.DecodePublicationAssetOutcomes(observation.Assets)
	if err != nil {
		return inspectedSourceRepublishTarget{}, fmt.Errorf("decode source-republish retry assets: %w", err)
	}
	if input.SourceRepublish.Cover.Derivation == "ready" {
		switch actualAssets.Cover.State {
		case persistence.PublicationAssetEmbedded:
			coverOutcome = persistence.PublicationSourceRepublishCoverVerified
		case persistence.PublicationAssetAbsent:
			coverOutcome = persistence.PublicationSourceRepublishCoverAbsentAfterInterruption
		default:
			return inspectedSourceRepublishTarget{}, errors.New("source-republish retry cover outcome is invalid")
		}
	}
	switch input.SourceRepublish.Cover.Derivation {
	case "absent":
		coverOutcome = persistence.PublicationSourceRepublishCoverNotRequested
	case "optional_failure":
		coverOutcome = persistence.PublicationSourceRepublishCoverPreparationFailed
	case "ready":
	default:
		return inspectedSourceRepublishTarget{}, errors.New("source-republish frozen cover derivation is invalid")
	}
	return inspectedSourceRepublishTarget{
		info: finalInfo, identity: observed, observation: observation,
		optional: persistence.PublicationSourceRepublishOptionalResult{
			LyricsOutcome: persistence.PublicationSourceRepublishLyricsObservedAfterInterruption,
			CoverOutcome:  coverOutcome,
		},
	}, nil
}

func verifySimpleSourceRepublishTarget(
	ctx context.Context,
	mutation sourcefs.OutputMutation,
	name string,
	file sourcefs.RegularFile,
	before os.FileInfo,
) (os.FileInfo, error) {
	after, err := file.Stat(ctx)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, errors.Join(err, errors.New("source-republish target changed during retry inspection"))
	}
	reopened, err := mutation.OpenExistingRegular(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("reopen source-republish retry target: %w", err)
	}
	reopenedInfo, statErr := reopened.Stat(ctx)
	closeErr := reopened.Close()
	if err := errors.Join(statErr, closeErr); err != nil || !os.SameFile(before, reopenedInfo) || before.Size() != reopenedInfo.Size() || !before.ModTime().Equal(reopenedInfo.ModTime()) {
		return nil, errors.Join(err, errors.New("source-republish target was replaced during retry inspection"))
	}
	return after, nil
}

func verifyInterruptedSourceRepublishTarget(
	ctx context.Context,
	root sourcefs.OutputDirectory,
	chain []recoveryDirectory,
	mutation sourcefs.OutputMutation,
	name string,
	file sourcefs.RegularFile,
	before os.FileInfo,
) (os.FileInfo, error) {
	if err := verifyPublicationParentChain(ctx, root, chain); err != nil {
		return nil, fmt.Errorf("verify interrupted source-republish parent chain: %w", err)
	}
	after, err := file.Stat(ctx)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, errors.Join(err, errors.New("interrupted source-republish target changed during inspection"))
	}
	reopened, err := mutation.OpenExistingRegular(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("reopen interrupted source-republish target: %w", err)
	}
	reopenedInfo, statErr := reopened.Stat(ctx)
	closeErr := reopened.Close()
	if err := errors.Join(statErr, closeErr); err != nil || !os.SameFile(before, reopenedInfo) || before.Size() != reopenedInfo.Size() || !before.ModTime().Equal(reopenedInfo.ModTime()) {
		return nil, errors.Join(err, errors.New("interrupted source-republish target was replaced during inspection"))
	}
	return after, nil
}

func sourceRepublishRecoveryCoverResult(
	frozen persistence.PublicationRepublishCover,
	actual persistence.PublicationCoverOutcome,
) (persistence.PublicationCoverOutcome, persistence.PublicationSourceRepublishCoverOutcome, error) {
	absent := persistence.PublicationCoverOutcome{State: persistence.PublicationAssetAbsent}
	switch frozen.Derivation {
	case "absent", "optional_failure":
		if actual.State != persistence.PublicationAssetAbsent {
			return persistence.PublicationCoverOutcome{}, "", publicationStageMismatch("interrupted source-republish target contains an unplanned cover")
		}
		return absent, "", nil
	case "ready":
		if actual.State == persistence.PublicationAssetAbsent {
			return absent, persistence.PublicationSourceRepublishCoverAbsentAfterInterruption, nil
		}
		size := frozen.SizeBytes
		if actual.State != persistence.PublicationAssetEmbedded || actual.SHA256 != frozen.SHA256 || actual.MIME != frozen.MIME || actual.SizeBytes == nil || *actual.SizeBytes != size {
			return persistence.PublicationCoverOutcome{}, "", publicationStageMismatch("interrupted source-republish target cover differs from frozen derivative")
		}
		return persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, SHA256: frozen.SHA256, MIME: frozen.MIME, SizeBytes: &size}, persistence.PublicationSourceRepublishCoverVerified, nil
	default:
		return persistence.PublicationCoverOutcome{}, "", errors.New("interrupted source-republish frozen cover derivation is invalid")
	}
}

// inspectionContextReader stops a snapshot copy between reads once the caller
// cancels, so inspection of a large target cannot outlive its context.
type inspectionContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r inspectionContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func isDefinitePublicationStageMismatch(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) != 1 {
			return false
		}
		return isDefinitePublicationStageMismatch(children[0])
	}
	if errors.Is(err, errPublicationStageMismatch) {
		return true
	}
	if wrapped := errors.Unwrap(err); wrapped != nil {
		return isDefinitePublicationStageMismatch(wrapped)
	}
	return false
}

func withoutLyrics(tags map[string][]string) map[string][]string {
	result := cloneTagMap(tags)
	for key := range result {
		if strings.EqualFold(strings.TrimSpace(key), "LYRICS") {
			delete(result, key)
		}
	}
	return result
}

func publicationTargetIsExactlyReserved(target persistence.PublicationNamespaceNode, nodes []persistence.PublicationNamespaceNode) bool {
	files := 0
	matched := false
	reservedDirs := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if node.NodeKind == "file" {
			files++
			matched = matched || node == target
		} else if node.NodeKind == "dir" && node.DirectoryScope != "" {
			reservedDirs[node.RelativePath] = true
		}
	}
	for parent := path.Dir(target.RelativePath); parent != "."; parent = path.Dir(parent) {
		if !reservedDirs[parent] {
			return false
		}
	}
	return files == 1 && matched
}

type recoveryDirectory struct {
	dir      sourcefs.OutputDirectory
	name     string
	identity sourcefs.DirectoryIdentity
}

func openExistingPublicationParent(ctx context.Context, root sourcefs.OutputDirectory, relative string) (sourcefs.OutputDirectory, []recoveryDirectory, error) {
	parentPath := path.Dir(relative)
	chain := []recoveryDirectory{{dir: root}}
	current := root
	for _, part := range splitPublicationPath(parentPath) {
		traversal, ok := current.(sourcefs.OutputDirectoryTraversal)
		if !ok {
			closeOutputChain(chain)
			return nil, nil, errors.New("publication target traversal capability is unavailable")
		}
		next, err := traversal.OpenExistingDir(ctx, part)
		if err != nil {
			closeOutputChain(chain)
			return nil, nil, err
		}
		identity, err := next.Identity(ctx)
		if err != nil {
			_ = next.Close()
			closeOutputChain(chain)
			return nil, nil, err
		}
		current = next
		chain = append(chain, recoveryDirectory{dir: next, name: part, identity: identity})
	}
	return current, chain, nil
}

func closeOutputChain(chain []recoveryDirectory) {
	for i := len(chain) - 1; i >= 1; i-- {
		_ = chain[i].dir.Close()
	}
}

func verifyPublicationParentChain(ctx context.Context, root sourcefs.OutputDirectory, chain []recoveryDirectory) error {
	if err := root.Verify(ctx); err != nil {
		return err
	}
	for i := 1; i < len(chain); i++ {
		if err := chain[i-1].dir.VerifyChild(ctx, chain[i].name, chain[i].identity); err != nil {
			return err
		}
	}
	return nil
}
