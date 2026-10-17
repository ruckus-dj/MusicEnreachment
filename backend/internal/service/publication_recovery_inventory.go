package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// PublicationRecoveryInventory is a read-only, point-in-time observation. It is
// not authority to resume execution, clean up files, or assert database freshness.
type PublicationRecoveryInventory struct {
	Assessment  persistence.PublicationRecoveryAssessment `json:"assessment"`
	Consistency string                                    `json:"consistency"`
	Private     []PublicationRecoveryFile                 `json:"private,omitempty"`
	Stage       *PublicationRecoveryFile                  `json:"stage,omitempty"`
	Target      *PublicationRecoveryFile                  `json:"target,omitempty"`
	Limitations []string                                  `json:"limitations"`
}

type PublicationRecoveryFile struct {
	Path            string `json:"path"`
	State           string `json:"state"`
	Size            *int64 `json:"size,omitempty"`
	MTimeUnixMicro  *int64 `json:"mtime_unix_micro,omitempty"`
	SHA256          string `json:"sha256,omitempty"`
	Identity        string `json:"identity,omitempty"`
	ExpectedMatches *bool  `json:"expected_matches,omitempty"`
}

// ObservePublicationRecovery inventories only names fixed by the validated
// journal plan. It never creates, renames, or removes filesystem objects.
func ObservePublicationRecovery(ctx context.Context, snapshot persistence.PublicationRecoverySnapshot, pinnedRoot sourcefs.Directory, configuredRootPath string, opener sourcefs.Opener) (inventory PublicationRecoveryInventory, err error) {
	// Assessment must finish before even the configured root is opened.
	plan, err := persistence.PublicationRecoveryObservationPlanFor(snapshot)
	if err != nil {
		return PublicationRecoveryInventory{}, err
	}
	inventory = PublicationRecoveryInventory{Assessment: plan.Assessment, Consistency: "consistent", Limitations: []string{"old_execution_quiescence", "namespace_live_fence", "cleanup_ownership", "database_freshness"}}
	if pinnedRoot == nil || opener == nil || configuredRootPath == "" {
		return PublicationRecoveryInventory{}, errors.New("publication recovery requires pinned root and opener")
	}
	defer func() {
		if err != nil {
			inventory = PublicationRecoveryInventory{}
		}
	}()
	owned := make([]*retainedRecoveryDir, 0)
	defer func() {
		for i := len(owned) - 1; i >= 0; i-- {
			err = errors.Join(err, owned[i].dir.Close())
		}
	}()
	rootInfo, e := pinnedRoot.Stat(ctx)
	if e != nil {
		return PublicationRecoveryInventory{}, e
	}
	rootID, e := sourcefsDirectoryIdentifier(ctx, pinnedRoot)
	if e != nil {
		return PublicationRecoveryInventory{}, e
	}
	if plan.Preparation != nil && rootID != plan.Preparation.ParentIdentity {
		return PublicationRecoveryInventory{}, errors.New("publication recovery root identity does not match journal")
	}
	if plan.Exposure != nil && rootID != plan.Exposure.OutputRootParent {
		return PublicationRecoveryInventory{}, errors.New("publication recovery output root identity does not match journal")
	}
	if err = verifyRecoveryRoot(ctx, opener, configuredRootPath, rootInfo); err != nil {
		return inventory, err
	}
	if plan.Preparation != nil {
		prep := plan.Preparation
		var expectedDir *persistence.PublicationDirectoryIdentity
		if prep.Step == "directory_created" || prep.Step == "input_ready" {
			expectedDir = prep.DirectoryIdentity
		}
		privateDir, observeErr := observeRecoveryDirectory(ctx, pinnedRoot, prep.PrivateDirRel, expectedDir, &owned)
		if observeErr != nil {
			return inventory, observeErr
		}
		inventory.Private = append(inventory.Private, privateDir)
		if privateDir.State != "present" || (privateDir.ExpectedMatches != nil && !*privateDir.ExpectedMatches) {
			// Never inspect children of an absent or substituted private directory.
		} else {
			for _, path := range []string{prep.InputRel, prep.OutputRel} {
				if len(inventory.Private) > 0 && inventory.Private[0].Path == prep.PrivateDirRel && inventory.Private[0].State == "missing" && path != prep.PrivateDirRel {
					continue
				}
				item, observeErr := observeRecoveryPath(ctx, pinnedRoot, path, &owned)
				if observeErr != nil {
					return inventory, observeErr
				}
				if path == prep.InputRel && item.State == "present" && prep.InputSize != nil {
					matches := item.Size != nil && *item.Size == *prep.InputSize
					item.ExpectedMatches = &matches
				}
				inventory.Private = append(inventory.Private, item)
			}
		}
	}
	privateMismatch := plan.Preparation != nil && len(inventory.Private) > 0 && inventory.Private[0].Path == plan.Preparation.PrivateDirRel &&
		(inventory.Private[0].State != "present" || inventory.Private[0].ExpectedMatches != nil && !*inventory.Private[0].ExpectedMatches)
	if plan.ValidatedStage != nil && !privateMismatch {
		item, observeErr := observeRecoveryPath(ctx, pinnedRoot, plan.ValidatedStage.RelativePath, &owned)
		if observeErr != nil {
			return inventory, observeErr
		}
		if item.State == "present" {
			expected := recoveryFileMatchesStage(item, plan.ValidatedStage)
			item.ExpectedMatches = &expected
		}
		inventory.Stage = &item
	}
	if plan.Exposure != nil {
		parent, parentErr := openRecoveryParent(ctx, pinnedRoot, plan.Exposure.TargetRelativePath, &owned)
		if parentErr != nil {
			if errors.Is(parentErr, os.ErrNotExist) {
				inventory.Target = &PublicationRecoveryFile{Path: plan.Exposure.TargetRelativePath, State: "missing"}
			} else if errors.Is(parentErr, sourcefs.ErrLink) || errors.Is(parentErr, sourcefs.ErrNotDirectory) || errors.Is(parentErr, sourcefs.ErrInvalidPath) {
				return PublicationRecoveryInventory{}, errors.New("publication recovery target parent is unsafe")
			} else {
				return PublicationRecoveryInventory{}, parentErr
			}
		} else {
			parentID, identityErr := sourcefsDirectoryIdentifier(ctx, parent)
			if identityErr != nil {
				return inventory, identityErr
			}
			if parentID != plan.Exposure.TargetParent {
				return PublicationRecoveryInventory{}, errors.New("publication recovery target parent identity mismatch")
			} else {
				item, observeErr := observeRecoveryPath(ctx, pinnedRoot, plan.Exposure.TargetRelativePath, &owned)
				if observeErr != nil {
					return inventory, observeErr
				}
				inventory.Target = &item
				if item.State == "present" && plan.ValidatedStage != nil {
					matches := recoveryFileMatchesStage(item, plan.ValidatedStage)
					item.ExpectedMatches = &matches
					inventory.Target = &item
				}
			}
		}
	}
	if err = verifyRecoveryRoot(ctx, opener, configuredRootPath, rootInfo); err != nil {
		return inventory, err
	}
	if err = revalidateRecoveryDirs(ctx, pinnedRoot, owned); err != nil {
		return inventory, err
	}
	return inventory, nil
}

func recoveryFileMatchesStage(item PublicationRecoveryFile, stage *persistence.PublicationStageObservation) bool {
	return stage != nil && item.SHA256 == stage.PayloadSHA256 && item.MTimeUnixMicro != nil && stage.Ownership.ExpectedMTime != nil && *item.MTimeUnixMicro == *stage.Ownership.ExpectedMTime && item.Size != nil && stage.Ownership.ExpectedSize != nil && *item.Size == *stage.Ownership.ExpectedSize
}

func sourcefsDirectoryIdentifier(ctx context.Context, dir sourcefs.Directory) (persistence.PublicationDirectoryIdentity, error) {
	identifier, ok := dir.(sourcefs.DirectoryIdentifier)
	if !ok {
		return persistence.PublicationDirectoryIdentity{}, errors.New("source filesystem does not expose directory identity")
	}
	id, err := identifier.Identity(ctx)
	if err != nil {
		return persistence.PublicationDirectoryIdentity{}, err
	}
	return persistence.PublicationDirectoryIdentity{Platform: id.Platform, Volume: id.Volume, FileID: id.FileID}, nil
}

func verifyRecoveryRoot(ctx context.Context, opener sourcefs.Opener, path string, pinned fs.FileInfo) error {
	current, err := opener.OpenRoot(ctx, path)
	if err != nil {
		return err
	}
	info, statErr := current.Stat(ctx)
	closeErr := current.Close()
	if statErr == nil && !os.SameFile(pinned, info) {
		statErr = errors.New("publication recovery root namespace changed")
	}
	return errors.Join(statErr, closeErr)
}

func observeRecoveryPath(ctx context.Context, root sourcefs.Directory, path string, retained *[]*retainedRecoveryDir) (item PublicationRecoveryFile, err error) {
	item = PublicationRecoveryFile{Path: path}
	parts, err := recoveryPathParts(path)
	if err != nil {
		return item, err
	}
	currentParent := root
	for _, part := range parts[:len(parts)-1] {
		next, openErr := currentParent.OpenDir(ctx, part)
		if openErr != nil {
			if ctx.Err() != nil {
				return item, ctx.Err()
			}
			state, classified := recoveryState(openErr)
			item.State = state
			if classified {
				return item, nil
			}
			return item, errors.Join(errors.New("publication recovery path parent is unsafe or inaccessible"), openErr)
		}
		info, statErr := next.Stat(ctx)
		if statErr != nil {
			return item, errors.Join(statErr, next.Close())
		}
		parent := currentParent
		*retained = append(*retained, &retainedRecoveryDir{parent: parent, name: part, dir: next, info: info})
		currentParent = next
	}
	f, openErr := currentParent.OpenRegular(ctx, parts[len(parts)-1])
	if openErr != nil {
		if ctx.Err() != nil {
			return item, ctx.Err()
		}
		state, classified := recoveryState(openErr)
		item.State = state
		if classified {
			return item, nil
		}
		return item, errors.Join(errors.New("publication recovery file is unsafe or inaccessible"), openErr)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	before, err := f.Stat(ctx)
	if err != nil {
		return item, err
	}
	if !before.Mode().IsRegular() {
		item.State = "unsafe"
		return item, nil
	}
	item.State = "present"
	size := before.Size()
	mt := before.ModTime().UnixMicro()
	item.Size = &size
	item.MTimeUnixMicro = &mt
	digest, hashErr := hashRecoveryRegular(ctx, f)
	if hashErr != nil {
		return item, hashErr
	}
	item.SHA256 = digest
	// Re-resolve the anchored name after hashing; the open descriptor alone does
	// not prove that the name still refers to the object observed initially.
	reopened, openErr := currentParent.OpenRegular(ctx, parts[len(parts)-1])
	if openErr != nil {
		if ctx.Err() != nil {
			return item, ctx.Err()
		}
		return item, errors.New("publication recovery file namespace changed")
	}
	reopenedInfo, statErr := reopened.Stat(ctx)
	closeErr := reopened.Close()
	if statErr != nil || closeErr != nil {
		return item, errors.Join(statErr, closeErr)
	}
	if !os.SameFile(before, reopenedInfo) {
		return item, errors.New("publication recovery file namespace changed")
	}
	after, err := f.Stat(ctx)
	if err != nil {
		return item, err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return item, errors.New("publication recovery file changed during observation")
	}
	return item, nil
}

func hashRecoveryRegular(ctx context.Context, file sourcefs.RegularFile) (string, error) {
	var sum [sha256.Size]byte
	err := file.Borrow(ctx, func(f *os.File) error {
		h := sha256.New()
		buf := make([]byte, 64*1024)
		for {
			if e := ctx.Err(); e != nil {
				return e
			}
			n, e := f.Read(buf)
			if n > 0 {
				_, _ = h.Write(buf[:n])
			}
			if errors.Is(e, io.EOF) {
				break
			}
			if e != nil {
				return e
			}
		}
		copy(sum[:], h.Sum(nil))
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sum[:]), nil
}

func recoveryPathParts(path string) ([]string, error) {
	if path == "" || path[0] == '/' {
		return nil, sourcefs.ErrInvalidPath
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\\x00") {
			return nil, sourcefs.ErrInvalidPath
		}
	}
	return parts, nil
}

func recoveryState(err error) (string, bool) {
	switch {
	case errors.Is(err, sourcefs.ErrLink):
		return "unsafe", true
	case errors.Is(err, os.ErrNotExist):
		return "missing", true
	case errors.Is(err, os.ErrPermission):
		return "inaccessible", true
	case errors.Is(err, sourcefs.ErrNotDirectory), errors.Is(err, sourcefs.ErrNotRegular), errors.Is(err, sourcefs.ErrInvalidPath):
		return "unsafe", true
	default:
		return "", false
	}
}

func observeRecoveryDirectory(ctx context.Context, root sourcefs.Directory, path string, expected *persistence.PublicationDirectoryIdentity, retained *[]*retainedRecoveryDir) (PublicationRecoveryFile, error) {
	item := PublicationRecoveryFile{Path: path}
	parts, err := recoveryPathParts(path)
	if err != nil {
		return item, err
	}
	current := root
	for _, part := range parts {
		parent := current
		next, openErr := current.OpenDir(ctx, part)
		if openErr != nil {
			if ctx.Err() != nil {
				return item, ctx.Err()
			}
			state, classified := recoveryState(openErr)
			if !classified {
				return item, openErr
			}
			item.State = state
			return item, nil
		}
		info, statErr := next.Stat(ctx)
		if statErr != nil {
			return item, errors.Join(statErr, next.Close())
		}
		*retained = append(*retained, &retainedRecoveryDir{parent: parent, name: part, dir: next, info: info})
		current = next
	}
	item.State = "present"
	id, err := sourcefsDirectoryIdentifier(ctx, current)
	if err != nil {
		return item, err
	}
	item.Identity = id.Platform + ":" + id.Volume + ":" + id.FileID
	if expected != nil {
		matches := item.Identity == expected.Platform+":"+expected.Volume+":"+expected.FileID
		item.ExpectedMatches = &matches
	}
	return item, nil
}

func openRecoveryParent(ctx context.Context, root sourcefs.Directory, path string, retained *[]*retainedRecoveryDir) (sourcefs.Directory, error) {
	parts, err := recoveryPathParts(path)
	if err != nil {
		return nil, err
	}
	current := root
	for _, part := range parts[:len(parts)-1] {
		next, openErr := current.OpenDir(ctx, part)
		if openErr != nil {
			return nil, openErr
		}
		info, statErr := next.Stat(ctx)
		if statErr != nil {
			return nil, errors.Join(statErr, next.Close())
		}
		*retained = append(*retained, &retainedRecoveryDir{parent: current, name: part, dir: next, info: info})
		current = next
	}
	return current, nil
}

type retainedRecoveryDir struct {
	parent sourcefs.Directory
	name   string
	dir    sourcefs.Directory
	info   fs.FileInfo
}

func revalidateRecoveryDirs(ctx context.Context, root sourcefs.Directory, retained []*retainedRecoveryDir) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := root.Stat(ctx); err != nil {
		return err
	}
	for _, entry := range retained {
		if err := ctx.Err(); err != nil {
			return err
		}
		reopened, err := entry.parent.OpenDir(ctx, entry.name)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("publication recovery namespace changed")
		}
		info, statErr := reopened.Stat(ctx)
		closeErr := reopened.Close()
		if statErr != nil || closeErr != nil {
			return errors.Join(statErr, closeErr)
		}
		if !os.SameFile(entry.info, info) {
			return errors.New("publication recovery namespace changed")
		}
	}
	return nil
}
