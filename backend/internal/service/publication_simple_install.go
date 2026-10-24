package service

import (
	"bytes"
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

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// executeSimplePublication is the journal-free live path for first-audio and
// rewrite deliveries. The candidate is rebuilt beside the destination before
// native installation and the actual installed result is committed directly.
func (executor *PublicationExecutor) executeSimplePublication(
	ctx context.Context,
	attempt *persistence.TrackPublicationAttempt,
	input persistence.PublicationInputs,
	guard persistence.PublicationDeliveryGuard,
	outputRoot string,
	root sourcefs.OutputDirectory,
) (resultErr error) {
	if input.IntentKind == "rewrite" && (input.Rewrite == nil || input.Audio != nil || input.AudioUpgrade != nil) ||
		input.IntentKind == "audio" && (input.Audio == nil || input.Rewrite != nil) ||
		input.IntentKind != "rewrite" && input.IntentKind != "audio" {
		return errors.New("simple publication input is incomplete")
	}
	if err := input.Validate(); err != nil {
		return fmt.Errorf("validate frozen publication inputs: %w", err)
	}
	manifest, nodes, err := publicationTarget(attempt.NamespaceManifest)
	if err != nil {
		return err
	}
	if input.Rewrite != nil {
		if manifest.RelativePath != input.Rewrite.RelativePath || attempt.ExpectedGeneration == nil || *attempt.ExpectedGeneration != input.Rewrite.CurrentGeneration {
			return errors.New("publication rewrite target does not match the frozen current generation")
		}
	} else if input.AudioUpgrade != nil {
		expected, err := audioUpgradeTargetRelativePath(input.AudioUpgrade.Current.RelativePath, input.Format, inputAudioFacts(input).AudioSourceFacts())
		if err != nil || manifest.RelativePath != expected || attempt.ExpectedGeneration == nil || *attempt.ExpectedGeneration != input.AudioUpgrade.Current.Generation {
			return errors.New("audio-upgrade target does not match the frozen current generation")
		}
	} else if manifest.RelativePath == "" || attempt.ExpectedGeneration != nil {
		return errors.New("first publication target does not match the frozen candidate")
	}
	targetParent, err := openPublicationTargetParent(ctx, root, manifest.RelativePath, nodes)
	if err != nil {
		return fmt.Errorf("open publication destination parent: %w", err)
	}
	if targetParent != root {
		defer func() { _ = targetParent.Close() }()
	}
	targetName := path.Base(manifest.RelativePath)
	targetAbsolute := filepath.Join(outputRoot, filepath.FromSlash(manifest.RelativePath))
	if !filepath.IsAbs(targetAbsolute) || filepath.Clean(targetAbsolute) != targetAbsolute {
		return errors.New("publication destination path is invalid")
	}
	mutation, ok := targetParent.(sourcefs.OutputMutation)
	if !ok {
		return errors.New("native publication destination mutation is unavailable")
	}
	inputPath := targetAbsolute
	currentPathForPreparation := ""
	if input.Audio != nil {
		inputPath, err = executor.openSimpleAudioSource(ctx, guard, *input.Audio)
		if err != nil {
			return err
		}
		if input.AudioUpgrade != nil {
			currentPath := filepath.Join(outputRoot, filepath.FromSlash(input.AudioUpgrade.Current.RelativePath))
			if !filepath.IsAbs(currentPath) || filepath.Clean(currentPath) != currentPath {
				return errors.New("audio-upgrade current path is invalid")
			}
			currentParent, parentErr := openPublicationTargetParent(ctx, root, input.AudioUpgrade.Current.RelativePath, nodes)
			if parentErr != nil {
				return fmt.Errorf("open current audio-upgrade parent: %w", parentErr)
			}
			if currentParent != root {
				defer func() { _ = currentParent.Close() }()
			}
			currentMutation, ok := currentParent.(sourcefs.OutputMutation)
			if !ok {
				return errors.New("native current publication inspection is unavailable")
			}
			currentFile, openErr := currentMutation.OpenExistingRegular(ctx, path.Base(input.AudioUpgrade.Current.RelativePath))
			if openErr != nil {
				return fmt.Errorf("open current audio-upgrade publication: %w", openErr)
			}
			currentInfo, statErr := currentFile.Stat(ctx)
			if statErr != nil {
				_ = currentFile.Close()
				return fmt.Errorf("inspect current audio-upgrade publication: %w", statErr)
			}
			if !currentInfo.Mode().IsRegular() {
				_ = currentFile.Close()
				return errors.New("current audio-upgrade publication is not a regular file")
			}
			if closeErr := currentFile.Close(); closeErr != nil {
				return fmt.Errorf("close current audio-upgrade publication: %w", closeErr)
			}
			// The preparation reader works on a private copy. The handle-relative open
			// above pins and rejects links for the live CURRENT path.
			currentPathForPreparation = currentPath
		}
	} else {
		currentFile, openErr := mutation.OpenExistingRegular(ctx, targetName)
		if openErr != nil {
			return fmt.Errorf("open current publication rewrite input: %w", openErr)
		}
		if err := currentFile.Close(); err != nil {
			return err
		}
	}
	tempDir, err := os.MkdirTemp(filepath.Dir(targetAbsolute), ".publication-delivery-")
	if err != nil {
		return fmt.Errorf("create publication candidate directory: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(tempDir)) }()
	tempName := filepath.Base(targetAbsolute)
	candidatePath := filepath.Join(tempDir, tempName)
	resolver := NewPublicationPinnedToolsResolver(executor.repository, simplePublicationToolCommands{executor.commands}, executor.platform)
	toolPair, validTools, err := resolver.Resolve(ctx, guard, input.Tools)
	if err != nil || !validTools {
		return errors.Join(err, errors.New("pinned publication tools are unavailable"))
	}
	ffmpegPath := toolPair.FFmpeg
	pinned := NewGuardedPublicationPinnedTools(resolver, guard)
	validator := executor.stageValidator
	if validator == nil {
		real := RealPublicationStageValidator{MetadataReader: metadata.Reader{}, Tools: pinned}
		if runner, ok := executor.commands.(PublicationFileCommandRunner); ok {
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
		if input.ManualLyrics != nil {
			validator = simpleManualLyricsValidator{validator: real}
		} else {
			validator = real
		}
	}
	writer := executor.metadata
	if writer == nil {
		writer = metadata.Writer{}
	}
	var coverInput *persistence.PublicationAttemptCoverInput
	if (input.Audio != nil && input.AudioUpgrade == nil) || input.ManualCover != nil {
		receipt, found, readErr := executor.repository.PublicationAttemptCoverInputForDelivery(ctx, guard)
		if readErr != nil {
			return fmt.Errorf("read frozen publication cover receipt: %w", readErr)
		}
		if input.ManualCover != nil && !found {
			return errors.New("manual cover receipt is unavailable")
		}
		if found {
			coverInput = &receipt
		}
	}
	preparation := PublicationSimplePreparation{
		Commands: executor.commands, FFmpegPath: ffmpegPath, FFprobePath: toolPair.FFprobe,
		Validator: validator, Metadata: writer, MetadataReader: metadata.Reader{}, Lyrics: executor.lyrics, CoverInput: coverInput,
	}
	var prepared PublicationSimplePreparationResult
	if input.AudioUpgrade != nil {
		prepared, err = preparation.PrepareAudioUpgrade(ctx, input, inputPath, currentPathForPreparation, tempDir, candidatePath)
	} else {
		prepared, err = preparation.Prepare(ctx, input, inputPath, tempDir, candidatePath)
	}
	if err != nil {
		return err
	}
	observation := prepared.Observation
	observation.RelativePath = manifest.RelativePath
	if err := executor.checkSimplePublicationCurrent(ctx, guard, input); err != nil {
		return err
	}
	candidateRoot, err := sourcefs.NewOutputOpener().OpenRoot(ctx, tempDir)
	if err != nil {
		return fmt.Errorf("open publication candidate directory: %w", err)
	}
	defer func() { _ = candidateRoot.Close() }()
	candidateMutation, ok := candidateRoot.(sourcefs.OutputMutation)
	if !ok {
		return errors.New("native publication candidate mutation is unavailable")
	}
	candidateFile, err := candidateMutation.OpenExistingRegular(ctx, tempName)
	if err != nil {
		return fmt.Errorf("open validated publication candidate: %w", err)
	}
	candidateInfo, err := candidateFile.Stat(ctx)
	if err != nil {
		_ = candidateFile.Close()
		return err
	}
	if !candidateInfo.Mode().IsRegular() {
		_ = candidateFile.Close()
		return errors.New("validated publication candidate is not a regular file")
	}
	if err := candidateFile.Close(); err != nil {
		return err
	}
	if err := executor.checkSimplePublicationCurrent(ctx, guard, input); err != nil {
		return err
	}
	var obsoleteOutput *persistence.ObsoletePublicationOutputCleanupAdmission
	if attempt.ExpectedGeneration != nil {
		previous, err := executor.repository.ReadPublicationObsoleteOutputCandidate(ctx, guard, manifest.RelativePath)
		if err != nil {
			return fmt.Errorf("read superseded publication output: %w", err)
		}
		if previous != nil {
			obsoleteOutput, err = captureObsoletePublicationOutput(ctx, root, outputRoot, previous.Publication, previous.NamespaceClaimID)
			if err != nil {
				return fmt.Errorf("capture superseded publication output: %w", err)
			}
		}
	}
	currentFile, currentErr := mutation.OpenExistingRegular(ctx, targetName)
	if currentErr == nil {
		currentInfo, statErr := currentFile.Stat(ctx)
		if statErr != nil {
			_ = currentFile.Close()
			return statErr
		}
		currentDigest, digestErr := digestRegular(ctx, currentFile)
		closeErr := currentFile.Close()
		if digestErr != nil || closeErr != nil {
			return errors.Join(digestErr, closeErr)
		}
		if currentDigest == observation.PayloadSHA256 {
			// A prior delivery may have installed this exact validated candidate and
			// then lost its database acknowledgement. Recommit its observation.
			_ = currentInfo
		} else {
			if err := executor.checkPublicationResultCurrent(ctx, guard); err != nil {
				return err
			}
			replacer, ok := targetParent.(sourcefs.OutputAtomicReplace)
			if !ok {
				return errors.New("native atomic publication replacement is unavailable")
			}
			if err := replacer.AtomicReplaceExisting(ctx, candidateRoot, tempName, candidateInfo, targetName, currentInfo); err != nil {
				return fmt.Errorf("atomically replace current publication: %w", err)
			}
		}
	} else if errors.Is(currentErr, fs.ErrNotExist) || errors.Is(currentErr, os.ErrNotExist) {
		if err := executor.checkPublicationResultCurrent(ctx, guard); err != nil {
			return err
		}
		if err := mutation.RenameNoReplace(ctx, candidateRoot, tempName, targetParent, targetName); err != nil {
			return fmt.Errorf("install first publication without replacement: %w", err)
		}
	} else {
		return fmt.Errorf("inspect current publication destination: %w", currentErr)
	}
	installed, err := mutation.OpenExistingRegular(ctx, targetName)
	if err != nil {
		return fmt.Errorf("open installed publication: %w", err)
	}
	installedInfo, err := installed.Stat(ctx)
	if err != nil {
		_ = installed.Close()
		return err
	}
	digest, digestErr := digestRegular(ctx, installed)
	closeErr := installed.Close()
	if err := errors.Join(digestErr, closeErr); err != nil {
		return err
	}
	if !installedInfo.Mode().IsRegular() || digest != observation.PayloadSHA256 {
		return errors.New("installed publication does not match the validated candidate")
	}
	size, mtime := installedInfo.Size(), installedInfo.ModTime().UnixMicro()
	result := persistence.PublicationResult{
		Observation:           observation,
		InitialLyrics:         prepared.InitialLyrics,
		ObsoleteOutputCleanup: obsoleteOutput,
		InstalledOwnership: persistence.PublicationCurrentOwnership{
			Version:  persistence.PublicationCurrentOwnershipVersion,
			Identity: persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: digest},
		},
	}
	if prepared.CoverEmbedding != nil {
		prepared.CoverEmbedding.Generation = guard.CandidateGeneration
		result.CoverEmbedding = prepared.CoverEmbedding
	}
	committed, err := executor.repository.CommitPublicationResult(context.WithoutCancel(ctx), guard, result)
	if err != nil || !committed {
		return errors.Join(err, errors.New("publication result was not committed"))
	}
	return nil
}

type simpleManualLyricsValidator struct {
	validator PublicationStageValidator
}

func (validator simpleManualLyricsValidator) Validate(ctx context.Context, filename string, input persistence.PublicationInputs) (PublicationStageObservation, error) {
	// The manual authority payload is not executable metadata. Audio/tag validation
	// checks only the exact desired tags; DB authority is not a tag-reader concern.
	input.ManualLyrics = nil
	return validator.validator.Validate(ctx, filename, input)
}

func (executor *PublicationExecutor) openSimpleAudioSource(ctx context.Context, guard persistence.PublicationDeliveryGuard, input persistence.PublicationAudioInput) (string, error) {
	holdID, found, err := executor.repository.PublicationSourceHoldForDelivery(ctx, guard)
	if err != nil || !found {
		return "", errors.Join(err, errors.New("publication source hold is unavailable"))
	}
	hold, valid, err := executor.repository.CheckPublicationSourceHoldForDelivery(ctx, guard, holdID)
	if err != nil || !valid || hold.SourceRootID != input.RootID || hold.SourceLocationID != input.LocationID ||
		hold.WorkID != input.WorkID || hold.MediaVariantID != input.MediaVariantID ||
		hold.ConfiguredPath != input.ConfiguredRootPath || hold.InventoryPath != input.InventoryRootPath ||
		hold.RelativePath != input.RelativePath || hold.SizeBytes != input.SizeBytes || !hold.Mtime.Equal(input.MTime) {
		return "", errors.Join(err, errors.New("publication source hold does not match frozen audio input"))
	}
	inputPath, err := resolveSimpleAudioSourcePath(ctx, hold.ConfiguredPath, hold.RelativePath, hold.SizeBytes, hold.Mtime)
	if err != nil {
		return "", err
	}
	return inputPath, nil
}

func resolveSimpleAudioSourcePath(ctx context.Context, rootPath, relativePath string, size int64, mtime time.Time) (string, error) {
	root, err := sourcefs.NewOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		return "", fmt.Errorf("open held publication source root: %w", err)
	}
	defer func() { _ = root.Close() }()
	file, err := sourcefs.OpenRegularAt(ctx, root, relativePath)
	if err != nil {
		return "", fmt.Errorf("open held publication source: %w", err)
	}
	info, statErr := file.Stat(ctx)
	closeErr := file.Close()
	if err := errors.Join(statErr, closeErr); err != nil {
		return "", fmt.Errorf("inspect held publication source: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != size || !info.ModTime().UTC().Truncate(time.Microsecond).Equal(mtime) {
		return "", errors.New("held publication source changed before preparation")
	}
	return filepath.Join(rootPath, filepath.FromSlash(relativePath)), nil
}

func (executor *PublicationExecutor) checkSimplePublicationCurrent(ctx context.Context, guard persistence.PublicationDeliveryGuard, input persistence.PublicationInputs) error {
	var current persistence.PublicationInputs
	var valid bool
	var err error
	if input.ManualLyrics != nil {
		current, valid, err = executor.repository.PublicationManualLyricsPreparationInputs(ctx, guard)
	} else {
		current, valid, err = executor.repository.PublicationPreparationInputs(ctx, guard)
	}
	if err != nil || !valid || !sameSimplePublicationInputs(current, input) {
		return errors.Join(err, errors.New("publication inputs or current generation changed before installation"))
	}
	return nil
}

func (executor *PublicationExecutor) checkPublicationResultCurrent(ctx context.Context, guard persistence.PublicationDeliveryGuard) error {
	current, err := executor.repository.CheckPublicationResultCurrent(ctx, guard)
	if err != nil || !current {
		return errors.Join(err, errors.New("publication inputs or current generation changed before installation"))
	}
	return nil
}

func sameSimplePublicationInputs(current, frozen persistence.PublicationInputs) bool {
	// Reuse the strict frozen-input representation comparison without depending on
	// incidental struct pointer identity.
	left, errLeft := json.Marshal(current)
	right, errRight := json.Marshal(frozen)
	return errLeft == nil && errRight == nil && string(left) == string(right)
}

type simplePublicationToolCommands struct{ runner PublicationCommandRunner }

func (commands simplePublicationToolCommands) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	result, err := commands.runner.Run(ctx, name, args...)
	return result.Output, err
}

func publicationTarget(raw []byte) (persistence.PublicationNamespaceNode, []persistence.PublicationNamespaceNode, error) {
	var manifest struct {
		Version int                                    `json:"version"`
		Nodes   []persistence.PublicationNamespaceNode `json:"nodes"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil || manifest.Version != 1 || len(manifest.Nodes) == 0 || decoder.Decode(new(any)) != io.EOF {
		return persistence.PublicationNamespaceNode{}, nil, errors.New("publication namespace manifest is invalid")
	}
	var target *persistence.PublicationNamespaceNode
	for i := range manifest.Nodes {
		if manifest.Nodes[i].NodeKind == "file" {
			if target != nil {
				return persistence.PublicationNamespaceNode{}, nil, errors.New("publication namespace has multiple file targets")
			}
			target = &manifest.Nodes[i]
		}
	}
	if target == nil || target.RelativePath == "" || target.CanonicalKey == "" {
		return persistence.PublicationNamespaceNode{}, nil, errors.New("publication namespace has no file target")
	}
	return *target, manifest.Nodes, nil
}

func audioUpgradeTargetRelativePath(currentRelative string, format publication.AudioPublicationFormat, source publication.AudioSourceFacts) (string, error) {
	if currentRelative == "" || path.IsAbs(currentRelative) || path.Clean(currentRelative) != currentRelative || strings.ContainsAny(currentRelative, "\\:\x00") {
		return "", errors.New("audio-upgrade current relative path is invalid")
	}
	for _, component := range strings.Split(currentRelative, "/") {
		if component == "" || component == "." || component == ".." {
			return "", errors.New("audio-upgrade current relative path is invalid")
		}
	}
	plan, err := publication.PlanAudioTransform(format, source)
	if err != nil {
		return "", fmt.Errorf("plan audio-upgrade target: %w", err)
	}
	if plan.Extension == "" || strings.ContainsAny(plan.Extension, "/\\:\x00") || plan.Extension == "." || plan.Extension == ".." {
		return "", errors.New("audio-upgrade target extension is invalid")
	}
	return strings.TrimSuffix(currentRelative, path.Ext(currentRelative)) + "." + plan.Extension, nil
}

func digestRegular(ctx context.Context, file sourcefs.RegularFile) (string, error) {
	var sum [sha256.Size]byte
	err := file.Borrow(ctx, func(native *os.File) error {
		hash := sha256.New()
		if _, err := io.Copy(hash, native); err != nil {
			return err
		}
		copy(sum[:], hash.Sum(nil))
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sum[:]), nil
}
