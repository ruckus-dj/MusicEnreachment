package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type obsoleteOutputCleanupRepository interface {
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	ReadObsoletePublicationOutputCleanup(context.Context, uuid.UUID) (*persistence.ObsoletePublicationOutputCleanup, error)
	ExecuteObsoletePublicationOutputCleanup(context.Context, uuid.UUID, int, int64, func(context.Context, persistence.ObsoletePublicationOutputCleanup) error) (bool, error)
}

type obsoleteOutputCleanupSettings interface {
	ReadRuntimeSettings(context.Context) (settings.RuntimeSettings, error)
}

// ObsoletePublicationOutputCleanupRunner executes one ID-only cleanup delivery.
type ObsoletePublicationOutputCleanupRunner struct {
	repository  obsoleteOutputCleanupRepository
	settings    obsoleteOutputCleanupSettings
	coordinator *publication.Coordinator
	opener      sourcefs.OutputOpener
}

func NewObsoletePublicationOutputCleanupRunner(repository obsoleteOutputCleanupRepository, runtimeSettings obsoleteOutputCleanupSettings, coordinator *publication.Coordinator, opener sourcefs.OutputOpener) *ObsoletePublicationOutputCleanupRunner {
	if opener == nil {
		opener = sourcefs.NewOutputOpener()
	}
	return &ObsoletePublicationOutputCleanupRunner{repository: repository, settings: runtimeSettings, coordinator: coordinator, opener: opener}
}

func (runner *ObsoletePublicationOutputCleanupRunner) Execute(ctx context.Context, operationID uuid.UUID, riverJobID int64) (resultErr error) {
	if runner == nil || runner.repository == nil || runner.settings == nil || runner.coordinator == nil || runner.opener == nil {
		return errors.New("obsolete publication output cleanup is unavailable")
	}
	if ctx == nil || operationID == uuid.Nil || riverJobID < 1 {
		return errors.New("obsolete publication output cleanup identity is invalid")
	}
	release, err := runner.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
	if err != nil {
		return err
	}
	defer release()
	operation, err := runner.repository.GetOperation(ctx, operationID)
	if err != nil {
		return fmt.Errorf("read obsolete publication output cleanup operation: %w", err)
	}
	if operation == nil || operation.Kind != persistence.ObsoletePublicationOutputCleanupOperationKind || operation.State != "queued" && operation.State != "running" {
		return nil
	}
	runtime, err := runner.settings.ReadRuntimeSettings(ctx)
	if err != nil {
		return fmt.Errorf("read output settings: %w", err)
	}
	_, err = runner.repository.ExecuteObsoletePublicationOutputCleanup(ctx, operationID, operation.Attempt, riverJobID, func(ctx context.Context, authority persistence.ObsoletePublicationOutputCleanup) error {
		return runner.remove(ctx, authority, runtime.OutputDirectory)
	})
	if err == nil || ctx.Err() != nil {
		return err
	}
	current, readErr := runner.repository.GetOperation(ctx, operationID)
	if readErr == nil && current != nil && current.State == "failed" {
		return nil
	}
	return fmt.Errorf("execute obsolete publication output cleanup: %w", err)
}

func (runner *ObsoletePublicationOutputCleanupRunner) remove(ctx context.Context, authority persistence.ObsoletePublicationOutputCleanup, outputDirectory string) (resultErr error) {
	rootPath := filepath.Clean(outputDirectory)
	relativePath := filepath.FromSlash(authority.RelativePath)
	if !filepath.IsAbs(rootPath) || filepath.Clean(authority.NativePath) != filepath.Join(rootPath, filepath.Clean(relativePath)) || filepath.IsAbs(relativePath) {
		return errors.New("output authority path does not match runtime output directory")
	}
	rel := filepath.Clean(relativePath)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("invalid output authority path")
	}
	var expectedRoot, expectedParent persistence.PublicationDirectoryIdentity
	if err := decodeCleanupIdentity(authority.OutputRootIdentity, &expectedRoot); err != nil {
		return err
	}
	if err := decodeCleanupIdentity(authority.ParentIdentity, &expectedParent); err != nil {
		return err
	}
	root, err := runner.opener.OpenRoot(ctx, rootPath)
	if err != nil {
		return fmt.Errorf("open output root: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	if err := root.Verify(ctx); err != nil {
		return err
	}
	identity, err := root.Identity(ctx)
	if err != nil {
		return err
	}
	if !sameCleanupIdentity(identity, expectedRoot) {
		return errors.New("output root identity changed")
	}
	components := strings.Split(rel, string(filepath.Separator))
	if len(components) == 0 {
		return errors.New("invalid output authority path")
	}
	parent := root
	opened := []sourcefs.OutputDirectory{}
	type pinnedChild struct {
		name     string
		identity sourcefs.DirectoryIdentity
	}
	chain := make([]pinnedChild, 0, len(components)-1)
	defer func() {
		for i := len(opened) - 1; i >= 0; i-- {
			resultErr = errors.Join(resultErr, opened[i].Close())
		}
	}()
	traversal, ok := root.(sourcefs.OutputDirectoryTraversal)
	if !ok {
		return errors.New("output filesystem does not support safe traversal")
	}
	for _, component := range components[:len(components)-1] {
		child, openErr := traversal.OpenExistingDir(ctx, component)
		if openErr != nil {
			return fmt.Errorf("open output parent: %w", openErr)
		}
		opened = append(opened, child)
		childIdentity, identityErr := child.Identity(ctx)
		if identityErr != nil {
			return identityErr
		}
		if err := parent.VerifyChild(ctx, component, childIdentity); err != nil {
			return err
		}
		chain = append(chain, pinnedChild{name: component, identity: childIdentity})
		parent = child
		traversal, ok = child.(sourcefs.OutputDirectoryTraversal)
		if !ok {
			return errors.New("output filesystem does not support safe traversal")
		}
	}
	parentIdentity, err := parent.Identity(ctx)
	if err != nil {
		return err
	}
	if !sameCleanupIdentity(parentIdentity, expectedParent) {
		return errors.New("output parent identity changed")
	}
	verifyNamespace := func() error {
		if err := root.Verify(ctx); err != nil {
			return err
		}
		ancestor := root
		for index, child := range chain {
			if err := ancestor.VerifyChild(ctx, child.name, child.identity); err != nil {
				return err
			}
			ancestor = opened[index]
		}
		return nil
	}
	if err := verifyNamespace(); err != nil {
		return err
	}
	mutation, ok := parent.(sourcefs.OutputMutation)
	if !ok {
		return errors.New("output filesystem does not support safe removal")
	}
	leaf := components[len(components)-1]
	file, err := mutation.OpenExistingRegular(ctx, leaf)
	if errors.Is(err, fs.ErrNotExist) {
		if verifyErr := verifyNamespace(); verifyErr != nil {
			return verifyErr
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("open obsolete output: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	info, err := file.Stat(ctx)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != authority.FileSize || info.ModTime().UnixMicro() != authority.ModifiedAt.UnixMicro() {
		return errors.New("obsolete output metadata changed")
	}
	hash := sha256.New()
	if err := file.Borrow(ctx, func(handle *os.File) error { _, copyErr := io.Copy(hash, handle); return copyErr }); err != nil {
		return fmt.Errorf("hash obsolete output: %w", err)
	}
	if !equalBytes(hash.Sum(nil), authority.SHA256) {
		return errors.New("obsolete output hash changed")
	}
	after, err := file.Stat(ctx)
	if err != nil {
		return err
	}
	if !os.SameFile(info, after) || info.Size() != after.Size() || info.ModTime().UnixMicro() != after.ModTime().UnixMicro() {
		return errors.New("obsolete output changed while hashing")
	}
	if err := verifyNamespace(); err != nil {
		return err
	}
	cleanup, ok := parent.(sourcefs.OutputCleanup)
	if !ok {
		return errors.New("output filesystem does not support safe removal")
	}
	return cleanup.RemoveOwnedRegular(ctx, leaf, parentIdentity, after)
}

func decodeCleanupIdentity(raw json.RawMessage, identity *persistence.PublicationDirectoryIdentity) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(identity); err != nil {
		return fmt.Errorf("decode output directory identity: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("decode output directory identity: trailing data")
	}
	if identity.Platform == "" || identity.Volume == "" || identity.FileID == "" {
		return errors.New("output directory identity is incomplete")
	}
	return nil
}

func sameCleanupIdentity(actual sourcefs.DirectoryIdentity, expected persistence.PublicationDirectoryIdentity) bool {
	return actual.Platform == expected.Platform && actual.Volume == expected.Volume && actual.FileID == expected.FileID
}

func equalBytes(a, b []byte) bool { return bytes.Equal(a, b) }
