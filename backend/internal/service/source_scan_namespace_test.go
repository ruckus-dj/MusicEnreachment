package service_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func TestSourceScanAbandonsPreparationWhenRootNamespaceIsReplaced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not allow renaming a source root while preparation holds open descendant file handles")
	}

	fixture := newSourceScanFixture(t)
	fixture.write(t, "album/track.flac", "audio bytes")
	pinnedPath := fixture.root.ConfiguredPath
	rootInfo, err := os.Stat(pinnedPath)
	if err != nil {
		t.Fatalf("stat source root: %v", err)
	}
	fileInfo, err := os.Stat(filepath.Join(pinnedPath, "album", "track.flac"))
	if err != nil {
		t.Fatalf("stat source file: %v", err)
	}
	replacement := t.TempDir()
	if err := os.MkdirAll(filepath.Join(replacement, "album"), 0o755); err != nil {
		t.Fatalf("create replacement tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(replacement, "album", "track.flac"), []byte("other bytes"), 0o644); err != nil {
		t.Fatalf("write replacement file: %v", err)
	}
	if err := os.Chtimes(filepath.Join(replacement, "album", "track.flac"), fileInfo.ModTime(), fileInfo.ModTime()); err != nil {
		t.Fatalf("preserve replacement file timestamps: %v", err)
	}
	if err := os.Chtimes(replacement, rootInfo.ModTime(), rootInfo.ModTime()); err != nil {
		t.Fatalf("preserve replacement root timestamps: %v", err)
	}
	replacementInfo, err := os.Stat(filepath.Join(replacement, "album", "track.flac"))
	if err != nil {
		t.Fatalf("stat replacement file: %v", err)
	}
	if replacementInfo.Size() != fileInfo.Size() || !replacementInfo.ModTime().Equal(fileInfo.ModTime()) || os.SameFile(replacementInfo, fileInfo) {
		t.Fatalf("replacement file did not preserve metadata while changing identity: before=%v after=%v", fileInfo, replacementInfo)
	}

	fixture.probe.answer("track.flac", true)
	movedPath := pinnedPath + "-pinned"
	t.Cleanup(func() {
		if err := os.RemoveAll(movedPath); err != nil {
			t.Errorf("remove parked fixture root: %v", err)
		}
	})
	fixture.scan = service.NewSourceScan(
		fixture.repository,
		fixture.probe,
		fixture.stages,
		service.WithSourceScanAnalysis(sourceScanPreparingFunc(func(ctx context.Context, request service.SourceAnalysisPrepareRequest) service.SourceAnalysisPreparation {
			result := fixture.probe.Prepare(ctx, request)
			if err := os.Rename(pinnedPath, movedPath); err != nil {
				t.Fatalf("move original source root from preparer: %v", err)
			}
			if err := os.Rename(replacement, pinnedPath); err != nil {
				t.Fatalf("replace source root from preparer: %v", err)
			}
			return result
		})),
	)

	if err := fixture.run(t); err == nil {
		t.Fatal("scan succeeded after the pathname root was replaced")
	}
	if got := fixture.repository.candidates[fixture.operationID]; len(got) != 0 {
		t.Fatalf("abandoned scan retained candidates: %#v", got)
	}
	if len(fixture.repository.locations) != 0 {
		t.Fatalf("abandoned scan published inventory changes: %#v", fixture.repository.locations)
	}
}

type sourceScanPreparingFunc func(context.Context, service.SourceAnalysisPrepareRequest) service.SourceAnalysisPreparation

func (prepare sourceScanPreparingFunc) Prepare(ctx context.Context, request service.SourceAnalysisPrepareRequest) service.SourceAnalysisPreparation {
	return prepare(ctx, request)
}
