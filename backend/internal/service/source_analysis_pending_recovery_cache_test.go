package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type pendingRecoveryCacheRepositoryFixture struct {
	sourceAnalysisStartRepositoryFixture
	works          []persistence.SourceAnalysisPendingWork
	steps          map[uuid.UUID][]persistence.SourceAnalysisStep
	details        map[uuid.UUID]*persistence.SourceLocationDetailSnapshot
	probeCacheHit  bool
	fingerprint    *persistence.SourceFingerprintResult
	missingInstall uuid.UUID
	probeLookups   int
	fpLookups      int
	failures       []persistence.SourceAnalysisStepSelection
	reused         []persistence.SourceAnalysisStepSelection
	fpVersions     []string
}

var _ sourceAnalysisPendingRepository = (*pendingRecoveryCacheRepositoryFixture)(nil)

func (repository *pendingRecoveryCacheRepositoryFixture) ListPendingSourceAnalysisRoots(context.Context) ([]persistence.SourceRoot, error) {
	if len(repository.works) == 0 {
		return nil, nil
	}
	return []persistence.SourceRoot{repository.works[0].Root}, nil
}

func (repository *pendingRecoveryCacheRepositoryFixture) ListPendingSourceAnalysisWork(context.Context, uuid.UUID) ([]persistence.SourceAnalysisPendingWork, error) {
	works := make([]persistence.SourceAnalysisPendingWork, 0, len(repository.works))
	for _, work := range repository.works {
		work.Steps = append([]persistence.SourceAnalysisStep(nil), repository.steps[work.Work.ID]...)
		if len(work.Steps) != 0 {
			works = append(works, work)
		}
	}
	return works, nil
}

func (repository *pendingRecoveryCacheRepositoryFixture) FailPendingSourceAnalysisStep(_ context.Context, _ uuid.UUID, workID uuid.UUID, step persistence.SourceStepName, _ string) (bool, error) {
	repository.failures = append(repository.failures, persistence.SourceAnalysisStepSelection{WorkID: workID, Step: step})
	rows := repository.steps[workID]
	remaining := rows[:0]
	for _, row := range rows {
		if row.Step != string(step) {
			remaining = append(remaining, row)
		}
	}
	repository.steps[workID] = remaining
	return len(remaining) != len(rows), nil
}

func (repository *pendingRecoveryCacheRepositoryFixture) ReadSourceLocationDetail(_ context.Context, rootID, locationID uuid.UUID) (*persistence.SourceLocationDetailSnapshot, error) {
	for _, work := range repository.works {
		if work.Root.ID == rootID && work.Location.ID == locationID {
			return repository.details[work.Work.ID], nil
		}
	}
	return nil, persistence.ErrSourceLocationNotFound
}

func (repository *pendingRecoveryCacheRepositoryFixture) GetInstallation(_ context.Context, id uuid.UUID) (*persistence.ToolInstallation, error) {
	if id == repository.missingInstall {
		return nil, sql.ErrNoRows
	}
	return repository.installation, nil
}

func (repository *pendingRecoveryCacheRepositoryFixture) LookupSourceProbe(context.Context, [sha256.Size]byte, string, int) (*persistence.SourceMediaVariant, bool, error) {
	repository.probeLookups++
	return nil, repository.probeCacheHit, nil
}

func (repository *pendingRecoveryCacheRepositoryFixture) LookupSourceFingerprint(_ context.Context, _ [sha256.Size]byte, version string) (*persistence.SourceFingerprintResult, bool, error) {
	repository.fpLookups++
	repository.fpVersions = append(repository.fpVersions, version)
	return repository.fingerprint, repository.fingerprint != nil, nil
}

func (repository *pendingRecoveryCacheRepositoryFixture) ReusePendingSourceAnalysisCache(_ context.Context, _ uuid.UUID, workID uuid.UUID, step persistence.SourceStepName, _ uuid.UUID, _ string) (bool, error) {
	rows := repository.steps[workID]
	remaining := rows[:0]
	changed := false
	for _, row := range rows {
		if row.Step == string(step) {
			changed = true
			continue
		}
		remaining = append(remaining, row)
	}
	repository.steps[workID] = remaining
	if changed {
		repository.reused = append(repository.reused, persistence.SourceAnalysisStepSelection{WorkID: workID, Step: step})
	}
	return changed, nil
}

func TestRetainedRecoveryCacheHitPrecedesUnavailableSiblingTool(t *testing.T) {
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootID, ownerID := uuid.New(), uuid.New()
	rawSHA := sha256.Sum256([]byte("same source digest"))
	toolProbe := persistence.SourceAnalysisToolSelection{PackageKind: "ffmpeg", InstallationID: uuid.New(), RelativePath: "ffmpeg/8", Executable: "ffprobe", Version: "8", VersionBanner: "ffprobe version 8"}
	toolFP := persistence.SourceAnalysisToolSelection{PackageKind: "fpcalc", InstallationID: uuid.New(), RelativePath: "fpcalc/1.5", Executable: "fpcalc", Version: "1.5", VersionBanner: "fpcalc version 1.5"}
	fixture := &pendingRecoveryCacheRepositoryFixture{
		sourceAnalysisStartRepositoryFixture: sourceAnalysisStartRepositoryFixture{operations: map[uuid.UUID]*persistence.Operation{}},
		steps:                                map[uuid.UUID][]persistence.SourceAnalysisStep{}, details: map[uuid.UUID]*persistence.SourceLocationDetailSnapshot{},
		fingerprint:    &persistence.SourceFingerprintResult{ID: uuid.New(), FPCalcVersion: toolFP.Version},
		missingInstall: toolFP.InstallationID,
	}
	for index, entry := range []struct {
		step string
		tool persistence.SourceAnalysisToolSelection
	}{{string(persistence.SourceStepProbe), toolProbe}, {string(persistence.SourceStepFingerprint), toolFP}} {
		filePath := filepath.Join(rootPath, string(rune('a'+index))+".flac")
		if err := os.WriteFile(filePath, []byte("audio"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filePath)
		if err != nil {
			t.Fatal(err)
		}
		mtime := info.ModTime().UTC().Truncate(time.Microsecond)
		workID, locationID := uuid.New(), uuid.New()
		work := pendingWork(rootID, rootPath, filepath.Base(filePath), info.Size(), mtime, true)
		work.Work.ID, work.Work.LocationID = workID, locationID
		work.Location.ID = locationID
		work.Steps = nil
		step := persistence.SourceAnalysisStep{WorkID: workID, Step: entry.step, State: "pending", LastOperationID: &ownerID}
		stepName := entry.step
		run, shaEnabled, cacheOnly := false, true, false
		raw, err := persistence.ProjectSourceAnalysisStepSnapshot(persistence.SourceAnalysisOperationSnapshot{
			SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion, Mode: persistence.SourceAnalysisModeSingleStep,
			WorkIDs: []uuid.UUID{workID}, TargetWorkID: &workID, TargetStep: &stepName,
			RerunTarget: &run, SHA256Enabled: &shaEnabled, CacheOnlyReuse: &cacheOnly,
			ToolsReadRequired: true, Tools: []persistence.SourceAnalysisToolSelection{entry.tool},
		}, workID, persistence.SourceStepName(entry.step))
		if err != nil {
			t.Fatal(err)
		}
		step.InputSnapshot = raw
		fixture.works = append(fixture.works, work)
		fixture.steps[workID] = []persistence.SourceAnalysisStep{step}
		fixture.details[workID] = &persistence.SourceLocationDetailSnapshot{
			Root: &work.Root, Location: &work.Location, Work: &work.Work,
			SHAVariant: &persistence.SourceMediaVariant{SourceSHA256: rawSHA[:]},
		}
	}
	fixture.probeCacheHit = false
	fixture.installation = &persistence.ToolInstallation{ID: toolProbe.InstallationID, PackageKind: "ffmpeg", State: "ready", RelativePath: toolProbe.RelativePath}
	service := NewSourceAnalysisOperations(fixture, sourceAnalysisStartSetupFixture(true), sourceAnalysisStartRuntimeFixture{}, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, sourceAnalysisStartRiverFixture{})

	operation, err := service.AdmitPending(context.Background(), rootID)
	if err != nil {
		t.Fatal(err)
	}
	if operation == nil || len(fixture.created) != 1 {
		t.Fatalf("operation=%+v admitted=%d, want the remaining probe step admitted", operation, len(fixture.created))
	}
	if fixture.fpLookups != 1 {
		t.Errorf("fingerprint cache lookups=%d, want one pinned-version lookup", fixture.fpLookups)
	}
	if fixture.probeLookups != 1 {
		t.Errorf("probe cache lookups=%d, want the original intent's single lookup", fixture.probeLookups)
	}
	if len(fixture.fpVersions) != 1 || fixture.fpVersions[0] != toolFP.Version {
		t.Errorf("fingerprint cache versions=%v, want retained pinned version %q", fixture.fpVersions, toolFP.Version)
	}
	if len(fixture.failures) != 0 {
		t.Errorf("failed pending steps=%+v, want no prerequisite failures", fixture.failures)
	}
	fpWorkID := fixture.works[1].Work.ID
	if len(fixture.reused) != 1 || fixture.reused[0] != (persistence.SourceAnalysisStepSelection{WorkID: fpWorkID, Step: persistence.SourceStepFingerprint}) {
		t.Errorf("reused tuples=%+v, want fingerprint cache reuse for %s", fixture.reused, fpWorkID)
	}
	for _, work := range fixture.works {
		if len(fixture.steps[work.Work.ID]) == 0 {
			continue
		}
		if fixture.steps[work.Work.ID][0].Step != string(persistence.SourceStepProbe) {
			t.Errorf("remaining step for work %s=%s, want probe", work.Work.ID, fixture.steps[work.Work.ID][0].Step)
		}
	}
	var snapshot persistence.SourceAnalysisOperationSnapshot
	if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil || snapshot.Mode != persistence.SourceAnalysisModeSingleStep || snapshot.TargetStep == nil || *snapshot.TargetStep != string(persistence.SourceStepProbe) {
		t.Errorf("admitted snapshot=%+v decode error=%v, want the exact remaining probe step", snapshot, err)
	}
}
