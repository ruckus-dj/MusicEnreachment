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
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// captureObsoletePublicationOutput freezes cleanup authority only when the
// existing output still exactly matches its current publication witness.
func captureObsoletePublicationOutput(ctx context.Context, root sourcefs.OutputDirectory, outputRoot string, previous persistence.TrackPublication, namespaceClaimID uuid.UUID) (_ *persistence.ObsoletePublicationOutputCleanupAdmission, resultErr error) {
	if ctx == nil || root == nil || namespaceClaimID == uuid.Nil || previous.Epoch == uuid.Nil || previous.PositionID == uuid.Nil || previous.Generation == uuid.Nil {
		return nil, errors.New("obsolete publication output identity is incomplete")
	}
	rootPath := filepath.Clean(outputRoot)
	if !filepath.IsAbs(rootPath) || !persistencePathPortable(previous.RelativePath) {
		return nil, errors.New("obsolete publication output path is invalid")
	}
	var ownership persistence.PublicationCurrentOwnership
	decoder := json.NewDecoder(strings.NewReader(string(previous.Ownership)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ownership); err != nil {
		return nil, fmt.Errorf("decode current publication ownership: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("decode current publication ownership: trailing JSON")
	}
	if ownership.Version != persistence.PublicationCurrentOwnershipVersion || ownership.Identity.SizeBytes == nil || *ownership.Identity.SizeBytes < 0 || ownership.Identity.MTime == nil || !validReplacementDigest(ownership.Identity.SHA256) {
		return nil, errors.New("current publication ownership is incomplete")
	}
	relative := filepath.FromSlash(previous.RelativePath)
	cleanRelative := filepath.Clean(relative)
	if filepath.IsAbs(relative) || cleanRelative == "." || cleanRelative == ".." || strings.HasPrefix(cleanRelative, ".."+string(filepath.Separator)) {
		return nil, errors.New("obsolete publication output path is invalid")
	}
	nativePath := filepath.Join(rootPath, cleanRelative)
	relativeToRoot, err := filepath.Rel(rootPath, nativePath)
	if err != nil || relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(filepath.Separator)) {
		return nil, errors.New("obsolete publication output is outside output root")
	}
	if err := root.Verify(ctx); err != nil {
		return nil, fmt.Errorf("verify output root: %w", err)
	}
	rootID, err := root.Identity(ctx)
	if err != nil {
		return nil, fmt.Errorf("identify output root: %w", err)
	}
	rootIdentity := persistence.PublicationDirectoryIdentity{Platform: rootID.Platform, Volume: rootID.Volume, FileID: rootID.FileID}
	parent := root
	opened := make([]sourcefs.OutputDirectory, 0)
	type childIdentity struct {
		name     string
		identity sourcefs.DirectoryIdentity
	}
	chain := make([]childIdentity, 0)
	defer func() {
		for i := len(opened) - 1; i >= 0; i-- {
			resultErr = errors.Join(resultErr, opened[i].Close())
		}
	}()
	components := strings.Split(cleanRelative, string(filepath.Separator))
	traversal, ok := parent.(sourcefs.OutputDirectoryTraversal)
	if !ok {
		return nil, errors.New("output filesystem does not support safe traversal")
	}
	for _, component := range components[:len(components)-1] {
		child, openErr := traversal.OpenExistingDir(ctx, component)
		if openErr != nil {
			return nil, fmt.Errorf("open existing output parent: %w", openErr)
		}
		opened = append(opened, child)
		id, idErr := child.Identity(ctx)
		if idErr != nil {
			return nil, idErr
		}
		if verifyErr := parent.VerifyChild(ctx, component, id); verifyErr != nil {
			return nil, verifyErr
		}
		chain = append(chain, childIdentity{name: component, identity: id})
		parent = child
		traversal, ok = parent.(sourcefs.OutputDirectoryTraversal)
		if !ok {
			return nil, errors.New("output filesystem does not support safe traversal")
		}
	}
	parentID, err := parent.Identity(ctx)
	if err != nil {
		return nil, err
	}
	verifyChain := func() error {
		if err := root.Verify(ctx); err != nil {
			return err
		}
		ancestor := root
		for index, item := range chain {
			if err := ancestor.VerifyChild(ctx, item.name, item.identity); err != nil {
				return err
			}
			ancestor = opened[index]
		}
		return nil
	}
	if err := verifyChain(); err != nil {
		return nil, err
	}
	mutation, ok := parent.(sourcefs.OutputMutation)
	if !ok {
		return nil, errors.New("output filesystem does not support safe file access")
	}
	file, err := mutation.OpenExistingRegular(ctx, components[len(components)-1])
	if err != nil {
		return nil, fmt.Errorf("open existing obsolete output: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	before, err := file.Stat(ctx)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() != *ownership.Identity.SizeBytes || before.ModTime().UnixMicro() != *ownership.Identity.MTime {
		return nil, errors.New("obsolete output does not match current publication metadata")
	}
	hash := sha256.New()
	if err := file.Borrow(ctx, func(native *os.File) error { _, err := io.Copy(hash, native); return err }); err != nil {
		return nil, fmt.Errorf("hash obsolete output: %w", err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != ownership.Identity.SHA256 {
		return nil, errors.New("obsolete output does not match current publication hash")
	}
	after, err := file.Stat(ctx)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime().UnixMicro() != after.ModTime().UnixMicro() {
		return nil, errors.New("obsolete output changed while hashing")
	}
	if err := verifyChain(); err != nil {
		return nil, err
	}
	return &persistence.ObsoletePublicationOutputCleanupAdmission{
		Epoch: previous.Epoch, PositionID: previous.PositionID, Generation: previous.Generation,
		RelativePath: previous.RelativePath, NativePath: nativePath, NamespaceClaimID: namespaceClaimID,
		OutputRootIdentity: rootIdentity,
		ParentIdentity:     persistence.PublicationDirectoryIdentity{Platform: parentID.Platform, Volume: parentID.Volume, FileID: parentID.FileID},
		FileSize:           after.Size(), ModifiedAt: time.UnixMicro(after.ModTime().UnixMicro()).UTC(), SHA256: hash.Sum(nil),
	}, nil
}

func persistencePathPortable(value string) bool {
	if value == "" || !utf8.ValidString(value) || strings.Contains(value, `\`) || strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") {
		return false
	}
	if len(value) >= 2 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':' {
		return false
	}
	path := filepath.FromSlash(value)
	return filepath.Clean(path) == path && path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator))
}
