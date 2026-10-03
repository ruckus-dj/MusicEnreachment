package service_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type managedPathsFixture struct {
	tools  string
	output string
}

func (fixture managedPathsFixture) GetToolsDirectory(context.Context) (string, bool, error) {
	return fixture.tools, fixture.tools != "", nil
}

func (fixture managedPathsFixture) GetOutputDirectory(context.Context) (string, bool, error) {
	return fixture.output, fixture.output != "", nil
}

// sourceRootRepositoryFixture stores roots in memory for the path, name and
// confirmation rules, which the service owns. The durability, the unique
// configured path and the lock that makes the active-scan guard atomic are
// properties of PostgreSQL and are proven by the integration tests; busy stands
// in for a root whose scan is queued or running.
type sourceRootRepositoryFixture struct {
	roots     []*persistence.SourceRoot
	locations map[uuid.UUID]int64
	busy      bool
	created   int
	updated   int
	listCalls int
	deleted   []uuid.UUID
}

func (fixture *sourceRootRepositoryFixture) CreateSourceRoot(_ context.Context, root *persistence.SourceRoot) error {
	if root.ID == uuid.Nil {
		root.ID = uuid.New()
	}
	fixture.created++
	fixture.roots = append(fixture.roots, root)
	return nil
}

func (fixture *sourceRootRepositoryFixture) GetSourceRoot(_ context.Context, id uuid.UUID) (*persistence.SourceRoot, error) {
	for _, root := range fixture.roots {
		if root.ID == id {
			return root, nil
		}
	}
	return nil, fmt.Errorf("get source root: no rows in result set")
}

func (fixture *sourceRootRepositoryFixture) ListSourceRoots(context.Context) ([]persistence.SourceRoot, error) {
	fixture.listCalls++
	roots := make([]persistence.SourceRoot, 0, len(fixture.roots))
	for _, root := range fixture.roots {
		roots = append(roots, *root)
	}
	return roots, nil
}

func (fixture *sourceRootRepositoryFixture) UpdateSourceRoot(_ context.Context, root *persistence.SourceRoot) error {
	if fixture.busy {
		return fmt.Errorf("update source root: %w", persistence.ErrSourceRootActiveScan)
	}
	for index, stored := range fixture.roots {
		if stored.ID == root.ID {
			fixture.updated++
			fixture.roots[index] = root
			return nil
		}
	}
	return fmt.Errorf("update source root: root does not exist")
}

func (fixture *sourceRootRepositoryFixture) DeleteSourceRoot(_ context.Context, id uuid.UUID, _ string, _ int64) error {
	if fixture.busy {
		return fmt.Errorf("delete source root: %w", persistence.ErrSourceRootActiveScan)
	}
	for index, root := range fixture.roots {
		if root.ID == id {
			fixture.roots = append(fixture.roots[:index], fixture.roots[index+1:]...)
			fixture.deleted = append(fixture.deleted, id)
			return nil
		}
	}
	return fmt.Errorf("delete source root: root does not exist")
}

func (fixture *sourceRootRepositoryFixture) CountSourceLocations(_ context.Context, id uuid.UUID) (int64, error) {
	return fixture.locations[id], nil
}

type sourceRootsFixture struct {
	roots       *service.SourceRoots
	repository  *sourceRootRepositoryFixture
	tools       string
	output      string
	source      string
	normalized  string // the source path as NormalizePath stores it
	otherSource string
}

func newSourceRootsFixture(t *testing.T) sourceRootsFixture {
	t.Helper()
	source := t.TempDir()
	normalized, err := settings.NormalizePath(source)
	if err != nil {
		t.Fatalf("normalize the source directory: %v", err)
	}
	repository := &sourceRootRepositoryFixture{locations: map[uuid.UUID]int64{}}
	tools, output := t.TempDir(), t.TempDir()
	return sourceRootsFixture{
		roots:       service.NewSourceRoots(repository, managedPathsFixture{tools: tools, output: output}),
		repository:  repository,
		tools:       tools,
		output:      output,
		source:      source,
		normalized:  normalized,
		otherSource: t.TempDir(),
	}
}

func normalizedPath(t *testing.T, path string) string {
	t.Helper()
	normalized, err := settings.NormalizePath(path)
	if err != nil {
		t.Fatalf("normalize %q: %v", path, err)
	}
	return normalized
}

func TestCreateSourceRootRejectsEmptyDisplayName(t *testing.T) {
	fixture := newSourceRootsFixture(t)

	for _, name := range []string{"", "   "} {
		view, err := fixture.roots.Create(context.Background(), name, fixture.source)
		if err == nil || !strings.Contains(err.Error(), "display name") {
			t.Fatalf("display name %q accepted: %#v, %v", name, view, err)
		}
	}
	if fixture.repository.created != 0 {
		t.Fatalf("roots created by a rejected name: %d, want 0", fixture.repository.created)
	}
}

func TestWindowsUNCSourcePathsAreRefusedWithoutMutation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows UNC source-path contract")
	}
	fixture := newSourceRootsFixture(t)
	stored := &persistence.SourceRoot{
		ID: uuid.New(), DisplayName: "legacy UNC", ConfiguredPath: `\\server\share`, Enabled: true,
	}
	fixture.repository.roots = append(fixture.repository.roots, stored)

	unsupported := []string{
		`\\?\UNC\server\share`, `\\?\unc\server\share`, `//?/UNC/server/share`,
		`\\?/uNc/server\share`, `\??\UNC\server\share`, `/??/unc/server/share`,
		`\\.\UNC\server\share`, `\\.\unc\server\share`, `//./UNC/server/share`,
		`\\./uNc/server\share`,
	}
	for _, path := range unsupported {
		if _, err := fixture.roots.Create(context.Background(), "new UNC", path); !errors.Is(err, service.ErrUnsupportedSourceRoot) {
			t.Errorf("Create(%q) error = %v, want unsupported network root", path, err)
		}
		if _, err := fixture.roots.Edit(context.Background(), stored.ID, service.SourceRootEdit{ConfiguredPath: stringPointer(path)}); !errors.Is(err, service.ErrUnsupportedSourceRoot) {
			t.Errorf("Edit(%q) error = %v, want unsupported network root", path, err)
		}
		if _, err := fixture.roots.ValidateSourcePath(context.Background(), path, &stored.ID); !errors.Is(err, service.ErrUnsupportedSourceRoot) {
			t.Errorf("revalidation(%q) error = %v, want unsupported network root", path, err)
		}
	}
	if fixture.repository.created != 0 {
		t.Fatalf("Create wrote %d roots for unsupported UNC paths", fixture.repository.created)
	}
	if fixture.repository.updated != 0 || fixture.repository.roots[0] != stored {
		t.Fatalf("unsupported paths mutated the stored root: updates=%d, root=%+v", fixture.repository.updated, fixture.repository.roots[0])
	}
	if fixture.repository.listCalls != 0 {
		t.Fatalf("unsupported paths reached the source-root repository %d times, want 0", fixture.repository.listCalls)
	}
	if stored.ConfiguredPath != `\\server\share` {
		t.Fatalf("Edit mutated stored UNC path to %q", stored.ConfiguredPath)
	}
	view, err := fixture.roots.Get(context.Background(), stored.ID)
	if err != nil || view.ConfiguredPath != stored.ConfiguredPath {
		t.Fatalf("legacy UNC GET = %#v, %v; want readable stored path", view, err)
	}
}

func TestCreateSourceRootRejectsUnusableDirectories(t *testing.T) {
	ctx := context.Background()
	regularFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(regularFile, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	loop := filepath.Join(t.TempDir(), "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name       string
		path       string
		wantPhrase string
	}{
		{name: "relative path", path: "relative/source", wantPhrase: "absolute"},
		{name: "missing directory", path: filepath.Join(t.TempDir(), "absent"), wantPhrase: "must exist"},
		{name: "regular file", path: regularFile, wantPhrase: "not a directory"},
		{name: "symlink loop", path: loop, wantPhrase: "must exist"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSourceRootsFixture(t)
			view, err := fixture.roots.Create(ctx, "Music", test.path)
			if err == nil || !strings.Contains(err.Error(), test.wantPhrase) {
				t.Fatalf("path %q accepted or rejected with the wrong reason: %#v, %v", test.path, view, err)
			}
			if fixture.repository.created != 0 {
				t.Fatalf("roots created by a rejected path: %d, want 0", fixture.repository.created)
			}
			if test.name == "missing directory" {
				if _, err := os.Stat(test.path); !os.IsNotExist(err) {
					t.Fatalf("rejected source directory exists after the attempt: %v", err)
				}
			}
		})
	}
}

func TestCreateSourceRootRejectsUnreadableDirectory(t *testing.T) {
	fixture := newSourceRootsFixture(t)
	unreadable := t.TempDir()
	if err := os.Chmod(unreadable, 0o311); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o755) })
	if _, err := os.ReadDir(unreadable); err == nil {
		t.Skip("this runner does not enforce directory permissions, so an unreadable directory cannot be built")
	}

	view, err := fixture.roots.Create(context.Background(), "Music", unreadable)
	if err == nil || !strings.Contains(err.Error(), "not readable") {
		t.Fatalf("unreadable directory accepted: %#v, %v", view, err)
	}
	if fixture.repository.created != 0 {
		t.Fatalf("roots created by an unreadable path: %d, want 0", fixture.repository.created)
	}
}

func TestCreateSourceRootRejectsManagedOverlap(t *testing.T) {
	ctx := context.Background()
	fixture := newSourceRootsFixture(t)
	linkIntoOutput := filepath.Join(t.TempDir(), "output-link")
	if err := os.Symlink(fixture.output, linkIntoOutput); err != nil {
		t.Fatal(err)
	}
	insideOutput := filepath.Join(fixture.output, "nested")
	if err := os.Mkdir(insideOutput, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name       string
		path       string
		wantPhrase string
	}{
		{name: "the managed output directory", path: fixture.output, wantPhrase: "managed output"},
		{name: "a directory inside the managed output directory", path: insideOutput, wantPhrase: "managed output"},
		{name: "the managed tools directory", path: fixture.tools, wantPhrase: "managed tools"},
		{name: "a symlink resolving into the managed output directory", path: linkIntoOutput, wantPhrase: "managed output"},
	} {
		t.Run(test.name, func(t *testing.T) {
			view, err := fixture.roots.Create(ctx, "Music", test.path)
			if err == nil || !strings.Contains(err.Error(), test.wantPhrase) {
				t.Fatalf("overlapping path %q accepted or rejected with the wrong reason: %#v, %v", test.path, view, err)
			}
		})
	}

	// The containing direction needs managed roots of its own: the fixture's
	// tools and output directories are siblings, so their shared parent would
	// report the tools overlap first.
	t.Run("a directory containing the managed output directory", func(t *testing.T) {
		parent := t.TempDir()
		nested := filepath.Join(parent, "output")
		if err := os.Mkdir(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		repository := &sourceRootRepositoryFixture{locations: map[uuid.UUID]int64{}}
		roots := service.NewSourceRoots(repository, managedPathsFixture{tools: t.TempDir(), output: nested})
		if _, err := roots.Create(ctx, "Music", parent); err == nil || !strings.Contains(err.Error(), "managed output") {
			t.Fatalf("a directory containing the managed output directory was accepted: %v", err)
		}
	})

	if fixture.repository.created != 0 {
		t.Fatalf("roots created by an overlapping path: %d, want 0", fixture.repository.created)
	}
}

func TestCreateSourceRootRejectsDuplicateNormalizedPath(t *testing.T) {
	ctx := context.Background()
	fixture := newSourceRootsFixture(t)
	created, err := fixture.roots.Create(ctx, "Music", fixture.source)
	if err != nil {
		t.Fatalf("create the first root: %v", err)
	}
	if created.ConfiguredPath != fixture.normalized {
		t.Fatalf("stored path = %q, want the normalized %q", created.ConfiguredPath, fixture.normalized)
	}

	link := filepath.Join(t.TempDir(), "same-directory")
	if err := os.Symlink(fixture.source, link); err != nil {
		t.Fatal(err)
	}
	for _, duplicate := range []string{fixture.source + string(filepath.Separator), filepath.Join(fixture.source, "..", filepath.Base(fixture.source)), link} {
		if _, err := fixture.roots.Create(ctx, "Second", duplicate); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("duplicate configured path %q accepted: %v", duplicate, err)
		}
	}
	if fixture.repository.created != 1 {
		t.Fatalf("roots created = %d, want only the first root", fixture.repository.created)
	}
}

func TestCreateSourceRootLeavesTheDirectoryUntouched(t *testing.T) {
	fixture := newSourceRootsFixture(t)
	album := filepath.Join(fixture.source, "album")
	if err := os.Mkdir(album, 0o755); err != nil {
		t.Fatal(err)
	}
	track := filepath.Join(album, "track.flac")
	if err := os.WriteFile(track, []byte("audio bytes"), 0o444); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(fixture.source)
	if err != nil {
		t.Fatal(err)
	}

	view, err := fixture.roots.Create(context.Background(), " Music ", fixture.source)
	if err != nil {
		t.Fatalf("create a root for an existing directory: %v", err)
	}

	if view.DisplayName != "Music" {
		t.Fatalf("display name = %q, want the trimmed name", view.DisplayName)
	}
	after, err := os.ReadDir(fixture.source)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || after[0].Name() != before[0].Name() {
		t.Fatalf("source directory entries = %v, want the untouched %v", after, before)
	}
	contents, err := os.ReadFile(track)
	if err != nil || string(contents) != "audio bytes" {
		t.Fatalf("source file after registration = %q, %v", contents, err)
	}
}

func TestCreateSourceRootAcceptsReadOnlyDirectory(t *testing.T) {
	fixture := newSourceRootsFixture(t)
	readOnly := t.TempDir()
	if err := os.Chmod(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o755) })

	view, err := fixture.roots.Create(context.Background(), "Archive", readOnly)
	if err != nil {
		t.Fatalf("read-only source directory rejected: %v", err)
	}
	if view.ConfiguredPath != normalizedPath(t, readOnly) || !view.Enabled {
		t.Fatalf("created root = %+v, want the read-only path and an enabled root", view)
	}
	entries, err := os.ReadDir(readOnly)
	if err != nil || len(entries) != 0 {
		t.Fatalf("read-only source directory after registration = %v, %v; want no probe file", entries, err)
	}
}

func TestSourceValidationNeverAttemptsFilesystemCreates(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	source := filepath.Join(base, "Music")
	managed := filepath.Join(base, "music")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(managed, 0o755); err != nil {
		if os.IsExist(err) {
			t.Skip("requires a case-sensitive filesystem")
		}
		t.Fatal(err)
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	managedInfo, err := os.Stat(managed)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(sourceInfo, managedInfo) {
		t.Skip("requires a case-sensitive filesystem")
	}
	nested := filepath.Join(source, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	repository := &sourceRootRepositoryFixture{locations: map[uuid.UUID]int64{}}
	roots := service.NewSourceRoots(repository, managedPathsFixture{tools: managed})
	var attempts int
	restore := settings.SetFilesystemCreateAttemptHook(func(string) { attempts++ })
	defer restore()

	created, err := roots.Create(ctx, "Music", source)
	if err != nil {
		t.Fatalf("create source root: %v", err)
	}
	if _, err := roots.ValidateSourcePath(ctx, source, &created.ID); err != nil {
		t.Fatalf("revalidate source path: %v", err)
	}
	newPath := nested
	if _, err := roots.Edit(ctx, created.ID, service.SourceRootEdit{ConfiguredPath: &newPath}); err != nil {
		t.Fatalf("edit source root: %v", err)
	}
	if attempts != 0 {
		t.Fatalf("source path validation attempted %d filesystem creates", attempts)
	}
}

func TestOverlappingSourceRootsKeepTheirOwnInventory(t *testing.T) {
	ctx := context.Background()
	fixture := newSourceRootsFixture(t)
	nested := filepath.Join(fixture.source, "albums", "live")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	outer, err := fixture.roots.Create(ctx, "Library", fixture.source)
	if err != nil {
		t.Fatalf("create the containing root: %v", err)
	}
	inner, err := fixture.roots.Create(ctx, "Live", nested)
	if err != nil {
		t.Fatalf("create the contained root: %v", err)
	}
	fixture.repository.locations[outer.ID] = 2
	fixture.repository.locations[inner.ID] = 1

	views, err := fixture.roots.List(ctx)
	if err != nil {
		t.Fatalf("list overlapping roots: %v", err)
	}
	counts := map[string]int64{}
	for _, view := range views {
		counts[view.ConfiguredPath] = view.LocationCount
	}
	if len(views) != 2 || counts[fixture.normalized] != 2 || counts[normalizedPath(t, nested)] != 1 {
		t.Fatalf("overlapping roots = %+v, want two independent roots with their own locations", views)
	}
}

func TestEditSourceRootChangesNamePathAndEnabled(t *testing.T) {
	ctx := context.Background()
	fixture := newSourceRootsFixture(t)
	created, err := fixture.roots.Create(ctx, "Music", fixture.source)
	if err != nil {
		t.Fatalf("create the root: %v", err)
	}
	stored := *fixture.repository.roots[0]
	stored.InventoryPath = &fixture.normalized
	stored.ScanGeneration = 3
	fixture.repository.roots[0] = &stored
	fixture.repository.locations[created.ID] = 4

	name, enabled := "Archive", false
	view, err := fixture.roots.Edit(ctx, created.ID, service.SourceRootEdit{
		DisplayName: &name, ConfiguredPath: &fixture.otherSource, Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("edit the root: %v", err)
	}

	edited := *fixture.repository.roots[0]
	if edited.DisplayName != "Archive" || edited.ConfiguredPath != normalizedPath(t, fixture.otherSource) || edited.Enabled {
		t.Fatalf("edited root = %+v, want the new name, path and disabled state", edited)
	}
	if edited.InventoryPath == nil || *edited.InventoryPath != fixture.normalized || edited.ScanGeneration != 3 {
		t.Fatalf("edited root inventory = %v/%d, want the inventory of the previous path kept",
			edited.InventoryPath, edited.ScanGeneration)
	}
	if !view.Stale || view.LocationCount != 4 {
		t.Fatalf("edited view = %+v, want a stale root that keeps its 4 locations", view)
	}
}

func TestEditSourceRootKeepsPreviousStateWhenThePathIsRejected(t *testing.T) {
	ctx := context.Background()
	fixture := newSourceRootsFixture(t)
	created, err := fixture.roots.Create(ctx, "Music", fixture.source)
	if err != nil {
		t.Fatalf("create the root: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "absent")

	view, err := fixture.roots.Edit(ctx, created.ID, service.SourceRootEdit{DisplayName: new("Archive"), ConfiguredPath: &missing})
	if err == nil || errors.Is(err, service.ErrSourceRootBusy) {
		t.Fatalf("edit with a missing path = %#v, %v; want a validation failure", view, err)
	}

	stored := *fixture.repository.roots[0]
	if stored.DisplayName != "Music" || stored.ConfiguredPath != fixture.normalized || !stored.Enabled {
		t.Fatalf("root after the rejected edit = %+v, want the previous data", stored)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("the rejected path exists after the edit: %v", err)
	}
}

func TestEditSourceRootIsRefusedWhileAScanIsActive(t *testing.T) {
	ctx := context.Background()
	fixture := newSourceRootsFixture(t)
	created, err := fixture.roots.Create(ctx, "Music", fixture.source)
	if err != nil {
		t.Fatalf("create the root: %v", err)
	}
	fixture.repository.busy = true

	disabled := false
	for _, edit := range []service.SourceRootEdit{
		{ConfiguredPath: &fixture.otherSource},
		{Enabled: &disabled},
	} {
		view, err := fixture.roots.Edit(ctx, created.ID, edit)
		if !errors.Is(err, service.ErrSourceRootBusy) {
			t.Fatalf("edit %+v while a scan is active = %#v, %v; want ErrSourceRootBusy", edit, view, err)
		}
	}
	stored := *fixture.repository.roots[0]
	if stored.ConfiguredPath != fixture.normalized || !stored.Enabled {
		t.Fatalf("root after the refused edits = %+v, want the previous data", stored)
	}
}

func TestDeleteSourceRootRequiresMatchingConfirmations(t *testing.T) {
	ctx := context.Background()
	fixture := newSourceRootsFixture(t)
	created, err := fixture.roots.Create(ctx, "Music", fixture.source)
	if err != nil {
		t.Fatalf("create the root: %v", err)
	}
	fixture.repository.locations[created.ID] = 2

	if err := fixture.roots.Delete(ctx, created.ID, fixture.otherSource, 2); !errors.Is(err, service.ErrSourceRootConfirmation) {
		t.Fatalf("deletion with another path = %v, want ErrSourceRootConfirmation", err)
	}
	if err := fixture.roots.Delete(ctx, created.ID, fixture.normalized, 3); !errors.Is(err, service.ErrSourceRootConfirmation) {
		t.Fatalf("deletion with another location count = %v, want ErrSourceRootConfirmation", err)
	}
	if _, err := fixture.roots.Get(ctx, created.ID); err != nil {
		t.Fatalf("root after the refused deletions: %v", err)
	}

	if err := fixture.roots.Delete(ctx, created.ID, fixture.normalized, 2); err != nil {
		t.Fatalf("confirmed deletion: %v", err)
	}
	if _, err := fixture.roots.Get(ctx, created.ID); err == nil {
		t.Fatal("deleted root is still readable")
	}
}

func TestDeleteSourceRootIsRefusedWhileAScanIsActive(t *testing.T) {
	ctx := context.Background()
	fixture := newSourceRootsFixture(t)
	created, err := fixture.roots.Create(ctx, "Music", fixture.source)
	if err != nil {
		t.Fatalf("create the root: %v", err)
	}
	fixture.repository.busy = true

	err = fixture.roots.Delete(ctx, created.ID, fixture.normalized, 0)
	if !errors.Is(err, service.ErrSourceRootBusy) {
		t.Fatalf("deletion while a scan is active = %v, want ErrSourceRootBusy", err)
	}
	if _, err := fixture.roots.Get(ctx, created.ID); err != nil {
		t.Fatalf("root after the refused deletion: %v", err)
	}
}
