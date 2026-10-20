package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// executeGenericPublication executes a frozen ordinary audio or rewrite
// publication under the already-claimed scope and pinned output root.
func executeGenericPublication(
	ctx context.Context,
	scope *PublicationExecutionScope,
	repository *persistence.SetupManagerRepository,
	input persistence.PublicationInputs,
	preparer PublicationStagePreparer,
	attempt *persistence.TrackPublicationAttempt,
	platform tools.Platform,
) (result PublicationStageObservation, resultErr error) {
	if scope == nil || repository == nil || attempt == nil || preparer.OutputParent == nil {
		return result, errors.New("generic publication execution dependencies are unavailable")
	}
	if !input.SupportsExecution() {
		return result, errors.New("publication inputs do not support generic execution")
	}
	if err := input.Validate(); err != nil {
		return result, fmt.Errorf("invalid frozen publication input: %w", err)
	}
	if attempt.OperationID != scope.guard.OperationID || attempt.IntentKind != input.IntentKind || (input.IntentKind != "audio" && input.IntentKind != "rewrite") {
		return result, errors.New("publication attempt does not match generic execution scope")
	}

	var namespace struct {
		Version int                                    `json:"version"`
		Nodes   []persistence.PublicationNamespaceNode `json:"nodes"`
	}
	decoder := json.NewDecoder(bytes.NewReader(attempt.NamespaceManifest))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&namespace); err != nil || namespace.Version != 1 || len(namespace.Nodes) == 0 || decoder.Decode(new(any)) != io.EOF {
		return result, errors.New("publication namespace manifest is invalid")
	}
	var target *persistence.PublicationNamespaceNode
	for index := range namespace.Nodes {
		node := &namespace.Nodes[index]
		if node.NodeKind != "file" {
			continue
		}
		if target != nil {
			return result, errors.New("publication namespace manifest has multiple file targets")
		}
		target = node
	}
	if target == nil || target.CanonicalKey == "" || target.RelativePath == "" {
		return result, errors.New("publication namespace manifest has no file target")
	}
	targetRelative := target.RelativePath
	var expectedGeneration *uuid.UUID
	if input.IntentKind == "rewrite" {
		if input.Rewrite == nil || attempt.ExpectedGeneration == nil || targetRelative != input.Rewrite.RelativePath || *attempt.ExpectedGeneration != input.Rewrite.CurrentGeneration {
			return result, errors.New("rewrite target does not match its frozen generation and namespace")
		}
		generation := *attempt.ExpectedGeneration
		expectedGeneration = &generation
	} else if input.Audio == nil || attempt.ExpectedGeneration != nil {
		return result, errors.New("audio publication must have an absent expected generation")
	}

	if input.IntentKind == "audio" {
		if preparer.HoldUUID == uuid.Nil {
			return result, errors.New("audio publication source hold is unavailable")
		}
	} else {
		preparer.HoldUUID = uuid.Nil
	}
	stage, err := preparer.prepareUnderScope(ctx, scope)
	if err != nil {
		return result, err
	}
	prepared, ok := stage.(*preparedPublicationStage)
	if !ok || prepared == nil || prepared.rewrite != (input.IntentKind == "rewrite") {
		return result, errors.Join(errors.New("generic publication preparation returned an incompatible stage"), stage.Close())
	}

	paths := stage.Paths()
	targetParent, err := openPublicationTargetParent(ctx, preparer.OutputParent, targetRelative, namespace.Nodes)
	if err != nil {
		return result, errors.Join(err, stage.Close())
	}
	if targetParent != preparer.OutputParent {
		defer func() { resultErr = errors.Join(resultErr, targetParent.Close()) }()
	}

	rootIdentity, err := preparer.OutputParent.Identity(ctx)
	if err != nil {
		return result, errors.Join(err, stage.Close())
	}
	targetIdentity, err := targetParent.Identity(ctx)
	if err != nil {
		return result, errors.Join(err, stage.Close())
	}
	rootPersisted := persistence.PublicationDirectoryIdentity{Platform: rootIdentity.Platform, Volume: rootIdentity.Volume, FileID: rootIdentity.FileID}
	targetPersisted := persistence.PublicationDirectoryIdentity{Platform: targetIdentity.Platform, Volume: targetIdentity.Volume, FileID: targetIdentity.FileID}
	if prepared.preparationRecord.ParentIdentity != rootPersisted {
		return result, errors.Join(errors.New("prepared publication root identity changed"), stage.Close())
	}
	intent := persistence.PublicationExposureIntent{
		Version:            persistence.PublicationExposureVersion,
		StagedPath:         paths.RelativePath,
		TargetCanonicalKey: target.CanonicalKey,
		TargetRelativePath: targetRelative,
		Expectation:        "absent",
		PrepOwner:          prepared.preparationRecord.OwnerToken,
		OutputRootParent:   rootPersisted,
		TargetParent:       targetPersisted,
	}
	if input.IntentKind == "rewrite" {
		intent.Expectation = "expected_current"
	}

	pinned, err := NewScopeBoundPublicationPinnedTools(repository, scope, scope.guard, platform)
	if err != nil {
		return result, errors.Join(err, stage.Close())
	}
	runner := PublicationStagingRunner{
		Repository: repository, PinnedTools: pinned,
		Metadata:  metadata.Writer{},
		Validator: &RealPublicationStageValidator{MetadataReader: metadata.Reader{}},
	}
	if input.IntentKind == "rewrite" {
		return runner.RunReplacementPublication(ctx, scope, input, stage, repository, intent, expectedGeneration, preparer.OutputParent, prepared.directory, targetParent, filepath.Base(paths.OutputPath), path.Base(targetRelative))
	}
	return runner.RunFirstAbsentPublication(ctx, scope, input, stage, repository, intent, preparer.OutputParent, prepared.directory, targetParent, filepath.Base(paths.OutputPath), path.Base(targetRelative))
}

func openPublicationTargetParent(ctx context.Context, root sourcefs.OutputDirectory, relative string, nodes []persistence.PublicationNamespaceNode) (sourcefs.OutputDirectory, error) {
	if relative == "" || path.IsAbs(relative) || path.Clean(relative) != relative || strings.ContainsAny(relative, "\\:\x00") {
		return nil, sourcefs.ErrInvalidPath
	}
	for _, component := range strings.Split(relative, "/") {
		if component == "" || component == "." || component == ".." {
			return nil, sourcefs.ErrInvalidPath
		}
	}
	parent := path.Dir(relative)
	if parent == "." {
		return root, nil
	}
	current := root
	owned := false
	ancestors := splitPublicationPath(parent)
	for index, part := range ancestors {
		relativeAncestor := path.Join(ancestors[:index+1]...)
		approved := 0
		for _, node := range nodes {
			if node.RelativePath == relativeAncestor && node.NodeKind == "dir" && node.DirectoryScope != "" {
				approved++
			}
		}
		if approved != 1 {
			err := fmt.Errorf("publication target parent %q is not uniquely approved by the frozen namespace", relativeAncestor)
			if owned {
				err = errors.Join(err, current.Close())
			}
			return nil, err
		}
		traversal, ok := current.(sourcefs.OutputDirectoryTraversal)
		if !ok {
			err := errors.New("publication target parent traversal capability is unavailable")
			if owned {
				err = errors.Join(err, current.Close())
			}
			return nil, err
		}
		next, err := traversal.OpenExistingDir(ctx, part)
		created := false
		if errors.Is(err, os.ErrNotExist) {
			next, err = current.CreateDirExclusive(ctx, part)
			created = err == nil
			// Another authorized executor may have created the namespace directory
			// after our read-only lookup. Reopen it without following links.
			if errors.Is(err, os.ErrExist) {
				next, err = traversal.OpenExistingDir(ctx, part)
			}
		}
		if err != nil {
			if owned {
				err = errors.Join(err, current.Close())
			}
			return nil, fmt.Errorf("open approved publication target parent %q: %w", part, err)
		}
		if created {
			// These are manifest-reserved empty directory nodes, not publication
			// artifacts. Do not remove them on later failure: namespace ownership is
			// shared and this path has no safe authority to prune them.
			parentSync, parentOK := current.(sourcefs.OutputDirectorySync)
			childSync, childOK := next.(sourcefs.OutputDirectorySync)
			if !parentOK || !childOK {
				err := errors.Join(errors.New("publication target directory durability is unsupported"), sourcefs.ErrUnsupported, next.Close())
				if owned {
					err = errors.Join(err, current.Close())
				}
				return nil, err
			}
			if err := errors.Join(parentSync.SyncDirectory(ctx), childSync.SyncDirectory(ctx)); err != nil {
				err = errors.Join(err, next.Close())
				if owned {
					err = errors.Join(err, current.Close())
				}
				return nil, fmt.Errorf("sync approved publication target directory: %w", err)
			}
		}
		if owned {
			if err := current.Close(); err != nil {
				return nil, errors.Join(err, next.Close())
			}
		}
		current, owned = next, true
	}
	return current, nil
}

func splitPublicationPath(value string) []string {
	parts := make([]string, 0)
	for value != "" && value != "." && value != "/" {
		part, parent := path.Base(value), path.Dir(value)
		parts = append([]string{part}, parts...)
		value = parent
	}
	return parts
}
