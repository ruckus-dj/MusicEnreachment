package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
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

type pendingBatchAdmissionFixture struct {
	*pendingRecoveryCacheRepositoryFixture
}

func (repository *pendingBatchAdmissionFixture) CreateNormalizedSourceAnalysisOperationAndEnqueue(_ context.Context, operation *persistence.Operation, _ persistence.RiverInserter, _ river.JobArgs, _ *river.InsertOpts) error {
	snapshot, err := persistence.DecodeSourceAnalysisOperationSnapshot(operation.InputSnapshot)
	if err != nil {
		return err
	}
	repository.created = append(repository.created, operation)
	for _, selection := range snapshot.SelectedSteps {
		rows := repository.steps[selection.WorkID]
		remaining := rows[:0]
		for _, row := range rows {
			if row.Step != string(selection.Step) {
				remaining = append(remaining, row)
			}
		}
		repository.steps[selection.WorkID] = remaining
	}
	return nil
}

func (repository *pendingBatchAdmissionFixture) ListPendingSourceAnalysisWork(ctx context.Context, rootID uuid.UUID) ([]persistence.SourceAnalysisPendingWork, error) {
	works, err := repository.pendingRecoveryCacheRepositoryFixture.ListPendingSourceAnalysisWork(ctx, rootID)
	if err != nil {
		return nil, err
	}
	sort.Slice(works, func(i, j int) bool {
		return works[i].Work.ID.String() < works[j].Work.ID.String()
	})
	return works, nil
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

func (repository *pendingRecoveryCacheRepositoryFixture) LookupSourceFingerprint(_ context.Context, _ [sha256.Size]byte) (*persistence.SourceFingerprintResult, bool, error) {
	repository.fpLookups++
	return repository.fingerprint, repository.fingerprint != nil, nil
}

func (repository *pendingRecoveryCacheRepositoryFixture) ReusePendingSourceAnalysisCache(_ context.Context, _ uuid.UUID, workID uuid.UUID, step persistence.SourceStepName, _ uuid.UUID, expectedVersion string) (bool, error) {
	if step == persistence.SourceStepFingerprint {
		repository.fpVersions = append(repository.fpVersions, expectedVersion)
	}
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

func TestPendingSourceAnalysisAdmitsOneBatchPerWorkWithExactSteps(t *testing.T) {
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootID := uuid.New()
	tool := persistence.SourceAnalysisToolSelection{
		PackageKind: "fpcalc", InstallationID: uuid.New(), RelativePath: "fpcalc/1.5.0",
		Executable: "fpcalc", Version: "1.5.0", VersionBanner: "fpcalc version 1.5.0",
	}
	base := &pendingRecoveryCacheRepositoryFixture{
		sourceAnalysisStartRepositoryFixture: sourceAnalysisStartRepositoryFixture{operations: map[uuid.UUID]*persistence.Operation{}},
		steps:                                map[uuid.UUID][]persistence.SourceAnalysisStep{},
		details:                              map[uuid.UUID]*persistence.SourceLocationDetailSnapshot{},
	}
	base.installation = &persistence.ToolInstallation{
		ID: tool.InstallationID, PackageKind: tool.PackageKind, State: "ready", RelativePath: tool.RelativePath,
		PlatformGOOS: "linux", PlatformGOARCH: "amd64", VerifiedAt: new(time.Now()),
		ExecutableVersions: []byte(`{"fpcalc":"fpcalc version 1.5.0"}`),
	}
	fixture := &pendingBatchAdmissionFixture{pendingRecoveryCacheRepositoryFixture: base}
	wantSteps := map[uuid.UUID]persistence.SourceStepName{}
	for index, step := range []persistence.SourceStepName{persistence.SourceStepSHA256, persistence.SourceStepFingerprint} {
		filePath := filepath.Join(rootPath, string(rune('a'+index))+".flac")
		if err := os.WriteFile(filePath, []byte("audio "+string(rune('1'+index))), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filePath)
		if err != nil {
			t.Fatal(err)
		}
		work := pendingWork(rootID, rootPath, filepath.Base(filePath), info.Size(), info.ModTime().UTC().Truncate(time.Microsecond), true)
		workID, locationID := uuid.New(), uuid.New()
		work.Work.ID, work.Work.LocationID = workID, locationID
		work.Location.ID = locationID
		wantSteps[workID] = step
		work.Steps = nil
		work.Steps = append(work.Steps, persistence.SourceAnalysisStep{WorkID: workID, Step: string(step), State: "pending"})
		fixture.works = append(fixture.works, work)
		fixture.steps[workID] = append([]persistence.SourceAnalysisStep(nil), work.Steps...)
		fixture.details[workID] = &persistence.SourceLocationDetailSnapshot{Root: &work.Root, Location: &work.Location, Work: &work.Work}
	}
	service := NewSourceAnalysisOperations(fixture, sourceAnalysisStartSetupFixture(true), sourceAnalysisStartRuntimeFixture(settings.RuntimeSettings{
		ActiveFPCalcInstallation: tool.InstallationID.String(),
	}), settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, sourceAnalysisStartRiverFixture{})

	if _, err := service.AdmitPending(context.Background(), rootID); err != nil {
		t.Fatal(err)
	}
	if len(fixture.created) != 2 {
		t.Fatalf("admitted %d operations, want one per pending work item; prerequisite failures: %+v; pending: %+v", len(fixture.created), fixture.failures, fixture.steps)
	}
	for index, operation := range fixture.created {
		snapshot, err := persistence.DecodeSourceAnalysisOperationSnapshot(operation.InputSnapshot)
		if err != nil {
			t.Fatalf("decode admitted operation %d: %v", index, err)
		}
		if len(snapshot.WorkIDs) != 1 || len(snapshot.SelectedSteps) != 1 {
			t.Errorf("operation %d selected work/steps = %v/%v, want exactly one work and one selected step", index, snapshot.WorkIDs, snapshot.SelectedSteps)
			continue
		}
		wantStep, exists := wantSteps[snapshot.WorkIDs[0]]
		if !exists || snapshot.SelectedSteps[0] != (persistence.SourceAnalysisStepSelection{WorkID: snapshot.WorkIDs[0], Step: wantStep}) {
			t.Errorf("operation %d selected work/steps = %v/%v, want exact step %q for its work", index, snapshot.WorkIDs, snapshot.SelectedSteps, wantStep)
		}
	}
}

func TestRetainedRecoveryUsesCurrentToolsAndAdmitsOneRemainingStep(t *testing.T) {
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
	fixture.normalizedWork = &fixture.works[0].Work
	fixture.normalizedLocation = &fixture.works[0].Location
	fixture.probeCacheHit = false
	fixture.installation = &persistence.ToolInstallation{ID: toolProbe.InstallationID, PackageKind: "ffmpeg", State: "ready", RelativePath: toolProbe.RelativePath,
		PlatformGOOS: "linux", PlatformGOARCH: "amd64", VerifiedAt: new(time.Now()), ExecutableVersions: []byte(`{"ffprobe":"ffprobe version 8"}`)}
	service := NewSourceAnalysisOperations(fixture, sourceAnalysisStartSetupFixture(true), sourceAnalysisStartRuntimeFixture(settings.RuntimeSettings{
		ActiveFFmpegInstallation: toolProbe.InstallationID.String(), ActiveFPCalcInstallation: toolFP.InstallationID.String(),
	}), settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, sourceAnalysisStartRiverFixture{})

	operation, err := service.AdmitPending(context.Background(), rootID)
	if err != nil {
		t.Fatal(err)
	}
	if operation == nil || len(fixture.created) != 2 {
		t.Fatalf("operation=%+v admitted=%d, want both independently eligible probe steps admitted", operation, len(fixture.created))
	}
	for _, created := range fixture.created {
		var intent persistence.SourceAnalysisOperationSnapshot
		if err := json.Unmarshal(created.InputSnapshot, &intent); err != nil || len(intent.WorkIDs) != 1 || intent.TargetStep == nil || *intent.TargetStep != string(persistence.SourceStepProbe) {
			t.Fatalf("admitted operation does not contain singleton probe intent: %+v, %v", intent, err)
		}
	}
	if fixture.fpLookups != 0 || fixture.probeLookups != 0 || len(fixture.fpVersions) != 0 {
		t.Errorf("retained intent performed cache lookup from absent durable pins: fp=%d probe=%d versions=%v", fixture.fpLookups, fixture.probeLookups, fixture.fpVersions)
	}
	if len(fixture.failures) != 1 || fixture.failures[0] != (persistence.SourceAnalysisStepSelection{WorkID: fixture.works[1].Work.ID, Step: persistence.SourceStepFingerprint}) {
		t.Errorf("failed pending steps=%+v, want only unavailable current fpcalc step", fixture.failures)
	}
	if len(fixture.reused) != 0 {
		t.Errorf("reused tuples=%+v, want no reuse based on historical tool pins", fixture.reused)
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
	if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil || snapshot.Mode != persistence.SourceAnalysisModeSingleStep || snapshot.TargetStep == nil || *snapshot.TargetStep != string(persistence.SourceStepProbe) || len(snapshot.Tools) != 0 {
		t.Errorf("admitted snapshot=%+v decode error=%v, want the exact remaining probe step", snapshot, err)
	}
	if len(operation.SourceAnalysisTools) != 1 || operation.SourceAnalysisTools[0].InstallationID != toolProbe.InstallationID {
		t.Errorf("transient current tool selection=%+v, want active ffprobe %s", operation.SourceAnalysisTools, toolProbe.InstallationID)
	}
}
