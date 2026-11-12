package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// recoverInterruptedAudioUpgrade adopts only a suitable, already-installed v2
// automatic audio-upgrade target. Its private snapshot is inspection input and
// is never transcoded, tagged, or installed.
func (executor *PublicationExecutor) recoverInterruptedAudioUpgrade(
	ctx context.Context,
	attempt *persistence.TrackPublicationAttempt,
	input persistence.PublicationInputs,
	guard persistence.PublicationDeliveryGuard,
	outputRoot string,
	root sourcefs.OutputDirectory,
	commands *simplePublicationTrackingRunner,
) (bool, error) {
	if attempt == nil || attempt.ResumeMode != persistence.PublicationResumeModePrepare ||
		input.Version != persistence.PublicationAudioUpgradeInputsVersion || input.IntentKind != "audio" ||
		input.AudioUpgrade == nil || input.Audio == nil || input.Validate() != nil ||
		attempt.ExpectedGeneration == nil || *attempt.ExpectedGeneration != input.AudioUpgrade.Current.Generation ||
		attempt.PositionID != guard.ExpectedPositionID || attempt.Epoch != guard.Epoch ||
		attempt.CandidateGeneration != guard.CandidateGeneration {
		return false, nil
	}
	target, nodes, err := publicationTarget(attempt.NamespaceManifest)
	if err != nil {
		return false, err
	}
	if !publicationTargetIsExactlyReserved(target, nodes) {
		return false, errors.New("interrupted audio-upgrade target is not exactly reserved by its frozen namespace")
	}
	parent, chain, err := openExistingPublicationParent(ctx, root, target.RelativePath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open interrupted audio-upgrade target parent: %w", err)
	}
	defer closeOutputChain(chain)
	mutation, ok := parent.(sourcefs.OutputMutation)
	if !ok {
		return false, errors.New("native interrupted audio-upgrade inspection is unavailable")
	}
	name := path.Base(target.RelativePath)
	file, err := mutation.OpenExistingRegular(ctx, name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open interrupted audio-upgrade target: %w", err)
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat(ctx)
	if err != nil {
		return false, fmt.Errorf("stat interrupted audio-upgrade target: %w", err)
	}
	if !before.Mode().IsRegular() || before.Size() < 0 {
		return false, nil
	}
	absolute := filepath.Join(outputRoot, filepath.FromSlash(target.RelativePath))
	if !filepath.IsAbs(absolute) || filepath.Clean(absolute) != absolute {
		return false, errors.New("interrupted audio-upgrade target path is invalid")
	}
	tempDir, inspectionPath, digest, err := snapshotInterruptedPublicationTarget(ctx, file, before, filepath.Ext(target.RelativePath))
	if err != nil {
		return false, fmt.Errorf("snapshot interrupted audio-upgrade target: %w", err)
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
	observation, err := validator.Validate(ctx, inspectionPath, input)
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
		return false, fmt.Errorf("validate interrupted audio-upgrade target: %w", err)
	}
	if observation.SHA256 != digest {
		return false, errors.New("interrupted audio-upgrade snapshot changed during validation")
	}
	actualAssets, err := persistence.DecodePublicationAssetOutcomes(observation.Assets)
	if err != nil {
		return false, fmt.Errorf("decode interrupted audio-upgrade assets: %w", err)
	}
	if !reflect.DeepEqual(actualAssets, input.AudioUpgrade.Current.Assets) {
		return false, nil
	}
	var actual struct {
		Version int                             `json:"version"`
		Facts   persistence.PersistedAudioFacts `json:"facts"`
	}
	if err := json.Unmarshal(observation.Audio, &actual); err != nil || actual.Version != 1 || actual.Facts.Validate() != nil {
		return false, errors.New("interrupted audio-upgrade validator returned invalid audio facts")
	}
	comparison := publication.CompareQuality(
		audioUpgradeQualityCandidate(actual.Facts),
		audioUpgradeQualityCandidate(input.AudioUpgrade.Current.AudioFacts),
	)
	if comparison != publication.QualityBetter {
		return false, nil
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
		return false, errors.Join(err, errors.New("audio-upgrade authority changed during interruption recovery"))
	}
	replaceAllowed, err := executor.repository.CheckPublicationTargetReplacement(ctx, guard, target.RelativePath)
	if err != nil || !replaceAllowed {
		return false, errors.Join(err, errors.New("audio-upgrade namespace authority changed during interruption recovery"))
	}
	finalInfo, err := verifyInterruptedSourceRepublishTarget(ctx, root, chain, mutation, name, file, before)
	if err != nil {
		return false, err
	}
	size, mtime := finalInfo.Size(), finalInfo.ModTime().UnixMicro()
	actualObservation := persistence.PublicationStageObservation{
		Version: 1, RelativePath: target.RelativePath,
		Ownership:     persistence.ArtifactOwnership{ExpectedSize: &size, ExpectedMTime: &mtime},
		PayloadSHA256: digest, ActualAudio: observation.Audio, ActualMetadata: observation.Metadata,
		ActualAssets: observation.Assets, QualityPolicyVersion: observation.QualityPolicyVersion,
	}
	result := persistence.PublicationResult{
		Observation: actualObservation,
		InstalledOwnership: persistence.PublicationCurrentOwnership{Version: persistence.PublicationCurrentOwnershipVersion,
			Identity: persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: digest}},
	}
	if target.RelativePath != input.AudioUpgrade.Current.RelativePath {
		previous, err := executor.repository.ReadPublicationObsoleteOutputCandidate(ctx, guard, target.RelativePath)
		if err != nil {
			return false, fmt.Errorf("read superseded audio-upgrade publication: %w", err)
		}
		if previous != nil {
			cleanup, err := captureObsoletePublicationOutput(ctx, root, outputRoot, previous.Publication, previous.NamespaceClaimID)
			if err != nil {
				return false, fmt.Errorf("capture superseded audio-upgrade publication: %w", err)
			}
			result.ObsoleteOutputCleanup = cleanup
		}
		if _, err := verifyInterruptedSourceRepublishTarget(ctx, root, chain, mutation, name, file, before); err != nil {
			return false, err
		}
	}
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	committed, err := executor.repository.CommitPublicationResult(commitCtx, guard, result)
	if err != nil || !committed {
		return false, errors.Join(err, errors.New("recovered audio-upgrade result was not committed"))
	}
	return true, nil
}
