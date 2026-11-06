package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestObserveCatalogAudioSourceAndPublication(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, data string) (int64, int64) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info.Size(), sourceScanMtime(info.ModTime()).UnixNano()
	}
	sourceSize, sourceMtime := write("source", "source data")
	write("published", "published")
	changedSize, changedMtime := write("changed", "different")
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "published"), filepath.Join(root, "link")); err != nil {
		t.Logf("symlink fixture unavailable: %v", err)
	}

	id := uuid.New()
	other := uuid.New()
	publishedOnly := uuid.New()
	candidates := []persistence.LibraryCatalogAudioCandidate{
		{PositionID: id, RootPath: root, RelativePath: "source", Kind: "source", SizeBytes: sourceSize, MTimeNS: sourceMtime},
		{PositionID: id, RootPath: root, RelativePath: "source", Kind: "source", SizeBytes: sourceSize, MTimeNS: sourceMtime}, // duplicate
		{PositionID: uuid.New(), RootPath: root, RelativePath: "absent", Kind: "source"},
		{PositionID: publishedOnly, RootPath: root, RelativePath: "published", Kind: "publication"},
		{PositionID: uuid.New(), RootPath: root, RelativePath: "absent", Kind: "publication"},
		{PositionID: uuid.New(), RootPath: root, RelativePath: "source", Kind: "source", SizeBytes: sourceSize + 1, MTimeNS: sourceMtime},
		{PositionID: uuid.New(), RootPath: root, RelativePath: "source", Kind: "source", SizeBytes: sourceSize, MTimeNS: sourceMtime + 1},
		{PositionID: uuid.New(), RootPath: root, RelativePath: "directory", Kind: "publication"},
		{PositionID: uuid.New(), RootPath: root, RelativePath: "link", Kind: "publication"},
		{PositionID: other, RootPath: root, RelativePath: "changed", Kind: "source", SizeBytes: changedSize - 1, MTimeNS: changedMtime},
		{PositionID: other, RootPath: root, RelativePath: "published", Kind: "publication"},
	}
	got, err := observeCatalogAudio(context.Background(), sourcefs.NewOpener(), candidates)
	if err != nil {
		t.Fatal(err)
	}
	want := map[uuid.UUID]bool{id: true, other: true, publishedOnly: true}
	if len(got) != len(want) {
		t.Fatalf("available IDs = %v", got)
	}
	for _, value := range got {
		if !want[value] {
			t.Errorf("unexpected available ID %s", value)
		}
	}
}

func TestObserveCatalogAudioUsesInventoryMtimePrecision(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "audio")
	if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	requested := time.Date(2026, time.January, 2, 3, 4, 5, 123456789, time.UTC)
	if err := os.Chtimes(path, requested, requested); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mtime := sourceScanMtime(info.ModTime()).UnixNano()
	positionID := uuid.New()
	observe := func(size int64, stamp int64) []uuid.UUID {
		t.Helper()
		ids, err := observeCatalogAudio(context.Background(), sourcefs.NewOpener(), []persistence.LibraryCatalogAudioCandidate{{
			PositionID: positionID, RootPath: root, RelativePath: "audio", Kind: "source", SizeBytes: size, MTimeNS: stamp,
		}})
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	if got := observe(info.Size(), mtime); len(got) != 1 || got[0] != positionID {
		t.Fatalf("inventory-normalized mtime unavailable: %v (native mtime %s)", got, info.ModTime())
	}
	if got := observe(info.Size()+1, mtime); len(got) != 0 {
		t.Fatalf("wrong size considered available: %v", got)
	}
	if got := observe(info.Size(), mtime+int64(time.Microsecond)); len(got) != 0 {
		t.Fatalf("wrong whole-microsecond mtime considered available: %v", got)
	}
}

func TestObserveCatalogAudioCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := observeCatalogAudio(ctx, sourcefs.NewOpener(), nil)
	if err != context.Canceled {
		t.Fatalf("error = %v, want context canceled", err)
	}
}
