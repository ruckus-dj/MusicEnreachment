package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestPendingStepsContainsOnlyEnumeratedPendingSteps(t *testing.T) {
	works := []persistence.SourceAnalysisPendingWork{
		{Steps: []persistence.SourceAnalysisStep{{Step: string(persistence.SourceStepSHA256)}, {Step: string(persistence.SourceStepProbe)}}},
		{Steps: []persistence.SourceAnalysisStep{{Step: string(persistence.SourceStepFingerprint)}}},
	}
	got := pendingSteps(works)
	for _, step := range []persistence.SourceStepName{
		persistence.SourceStepSHA256, persistence.SourceStepProbe, persistence.SourceStepFingerprint,
	} {
		if !got[step] {
			t.Errorf("pending step %q was omitted", step)
		}
	}
	if len(got) != 3 {
		t.Fatalf("got %d pending steps, want exactly 3", len(got))
	}
}

func TestRetainedPendingStepKeepsOriginalPinAndSeparatesNeverAdmittedWork(t *testing.T) {
	workID := uuid.New()
	stepName := string(persistence.SourceStepFingerprint)
	rerun, shaEnabled, cacheOnly := false, true, false
	original := persistence.SourceAnalysisToolSelection{
		PackageKind: "fpcalc", InstallationID: uuid.New(), RelativePath: "fpcalc/1.5.0",
		Executable: "fpcalc", Version: "1.5.0", VersionBanner: "fpcalc version 1.5.0",
	}
	raw, err := persistence.ProjectSourceAnalysisStepSnapshot(persistence.SourceAnalysisOperationSnapshot{
		SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion,
		Mode:          persistence.SourceAnalysisModeSingleStep,
		WorkIDs:       []uuid.UUID{workID}, TargetWorkID: &workID, TargetStep: &stepName,
		RerunTarget: &rerun, SHA256Enabled: &shaEnabled, CacheOnlyReuse: &cacheOnly,
		ToolsReadRequired: true, Tools: []persistence.SourceAnalysisToolSelection{original},
	}, workID, persistence.SourceStepFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	retained := persistence.SourceAnalysisPendingWork{
		Work:  persistence.SourceAnalysisWork{ID: workID, SHA256Enabled: true},
		Steps: []persistence.SourceAnalysisStep{{WorkID: workID, Step: stepName, State: "pending", InputSnapshot: raw}},
	}
	neverAdmitted := persistence.SourceAnalysisPendingWork{
		Work:  persistence.SourceAnalysisWork{ID: uuid.New(), SHA256Enabled: true},
		Steps: []persistence.SourceAnalysisStep{{Step: stepName}},
	}
	ordinary, intents, malformed := separateRetainedSourceAnalysisSteps([]persistence.SourceAnalysisPendingWork{retained, neverAdmitted})
	if len(malformed) != 0 || len(intents) != 1 || len(ordinary) != 1 || ordinary[0].Work.ID != neverAdmitted.Work.ID {
		t.Fatalf("ordinary=%+v intents=%+v malformed=%+v", ordinary, intents, malformed)
	}
	selection, version := retainedCacheSelection(intents[0].Snapshot, persistence.SourceStepFingerprint)
	if selection != original || version != original.Version {
		t.Fatalf("retained cache selection=%+v version=%q, want original pin %+v", selection, version, original)
	}
	if got := intents[0].Snapshot.CacheOnlyReuse; got == nil || *got {
		t.Fatal("ordinary pinned-tool selection was not retained")
	}
}

func TestPendingSourceAnalysisCohortUsesFirstCurrentWork(t *testing.T) {
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(rootPath, "current.flac")
	if err := os.WriteFile(filePath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	mtime := info.ModTime().UTC().Truncate(time.Microsecond)
	rootID := uuid.New()
	staleEnabled := pendingWork(rootID, rootPath, "stale.flac", info.Size(), mtime, true)
	currentDisabled := pendingWork(rootID, rootPath, "current.flac", info.Size(), mtime, false)

	// The stale first row has a different immutable path and policy. It must not
	// pin the cohort before filesystem validation excludes it.
	cohort := currentPendingSourceAnalysisCohort(t.Context(), []persistence.SourceAnalysisPendingWork{staleEnabled, currentDisabled})
	if len(cohort) != 1 || cohort[0].Work.SHA256Enabled {
		t.Fatalf("got cohort %#v, want only current SHA-disabled work", cohort)
	}
}

func pendingWork(rootID uuid.UUID, rootPath, relativePath string, size int64, mtime time.Time, shaEnabled bool) persistence.SourceAnalysisPendingWork {
	return persistence.SourceAnalysisPendingWork{
		Root:     persistence.SourceRoot{ID: rootID, ConfiguredPath: rootPath, InventoryPath: &rootPath, Enabled: true},
		Work:     persistence.SourceAnalysisWork{ID: uuid.New(), SourceRootID: rootID, ConfiguredPath: rootPath, InventoryPath: rootPath, RelativePath: relativePath, SizeBytes: size, Mtime: mtime, SHA256Enabled: shaEnabled},
		Location: persistence.SourceLocation{SourceRootID: rootID, RelativePath: relativePath, SizeBytes: size, Mtime: mtime},
	}
}

func TestPendingSourceFileCurrentRejectsUnavailableOrMismatchedWorkBeforeOpening(t *testing.T) {
	configuredPath := "/music"
	work := persistence.SourceAnalysisPendingWork{
		Root:     persistence.SourceRoot{ID: uuid.New(), ConfiguredPath: configuredPath, InventoryPath: &configuredPath, Enabled: false},
		Work:     persistence.SourceAnalysisWork{ConfiguredPath: configuredPath, InventoryPath: configuredPath, RelativePath: "song.flac"},
		Location: persistence.SourceLocation{RelativePath: "song.flac"},
	}
	if pendingSourceFileCurrent(t.Context(), work) {
		t.Fatal("disabled root should not be dispatched")
	}
	work.Root.Enabled = true
	work.Work.RelativePath = "other.flac"
	if pendingSourceFileCurrent(t.Context(), work) {
		t.Fatal("work whose immutable path differs from the location should not be dispatched")
	}
}
