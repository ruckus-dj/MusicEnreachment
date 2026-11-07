package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
	if !found || cover.State != "absent" {
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
	tempDir, err := os.MkdirTemp("", "publication-inspection-")
	if err != nil {
		return false, fmt.Errorf("create private publication inspection directory: %w", err)
	}
	defer func() {
		if !commands.QuiescenceUnknown() {
			_ = os.RemoveAll(tempDir)
		}
	}()
	inspectionPath := filepath.Join(tempDir, "inspection"+filepath.Ext(target.RelativePath))
	inspection, err := os.OpenFile(inspectionPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false, fmt.Errorf("create private publication inspection snapshot: %w", err)
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
		return false, fmt.Errorf("snapshot interrupted publication target: %w", copyErr)
	}
	digest := hex.EncodeToString(hash.Sum(nil))

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
	if lyrics := capture.Tags["LYRICS"]; len(lyrics) > 0 && input.InitialLyrics != nil && input.InitialLyrics.Desired != nil &&
		(input.InitialLyrics.Desired.Authority == "manual" || input.InitialLyrics.Desired.State == "explicit_empty") {
		return false, nil
	}
	// Lyrics are observed output, not acquisition history. Permit exact read-back
	// while validating a detached input; never persist this projection as frozen input.
	inspectionInput := input.Clone()
	inspectionInput.DesiredTags = cloneTagMap(capture.Tags)
	observation, err := firstAudioValidator.ValidateFirstAudioCover(ctx, inspectionPath, inspectionInput, persistence.PublicationCoverOutcome{State: persistence.PublicationAssetAbsent})
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
	}
	commitCtx, cancelCommit := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelCommit()
	committed, err := executor.repository.CommitPublicationResult(commitCtx, guard, result)
	if err != nil || !committed {
		return false, errors.Join(err, errors.New("recovered publication result was not committed"))
	}
	return true, nil
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
