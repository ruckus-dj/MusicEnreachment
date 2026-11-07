package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type sourceScanRepositoryFixture struct {
	root            *persistence.SourceRoot
	candidates      map[uuid.UUID][]persistence.SourceScanCandidateInput
	deletions       []sourceScanCandidateDeletion
	batchSizes      []int
	identityTargets []persistence.SourceScanIdentityTarget
	identityFences  []persistence.SourceScanIdentityFence
	observations    []persistence.SourceChangedFileObservation
	observe         func(persistence.SourceChangedFileObservation) error
	deleteErr       error
	appendErr       error
}

type sourceScanCandidateDeletion struct {
	operationID uuid.UUID
	rootID      uuid.UUID
	path        string
	attempt     int
	jobID       int64
}

func (fixture *sourceScanRepositoryFixture) GetSourceRoot(context.Context, uuid.UUID) (*persistence.SourceRoot, error) {
	return fixture.root, nil
}

func (fixture *sourceScanRepositoryFixture) ListSourceScanIdentityTargets(_ context.Context, fence persistence.SourceScanIdentityFence) ([]persistence.SourceScanIdentityTarget, error) {
	fixture.identityFences = append(fixture.identityFences, fence)
	return fixture.identityTargets, nil
}

func (fixture *sourceScanRepositoryFixture) ObserveChangedSourceFile(_ context.Context, observation persistence.SourceChangedFileObservation) error {
	fixture.observations = append(fixture.observations, observation)
	if fixture.observe != nil {
		return fixture.observe(observation)
	}
	return nil
}

func (fixture *sourceScanRepositoryFixture) DeleteSourceScanCandidatesForRootDelivery(_ context.Context, operationID, rootID uuid.UUID, path string, attempt int, jobID int64) error {
	fixture.deletions = append(fixture.deletions, sourceScanCandidateDeletion{operationID: operationID, rootID: rootID, path: path, attempt: attempt, jobID: jobID})
	if fixture.deleteErr != nil {
		return fixture.deleteErr
	}
	fixture.candidates[operationID] = nil
	return nil
}

func (fixture *sourceScanRepositoryFixture) AppendSourceScanCandidatesForDelivery(_ context.Context, operationID uuid.UUID, _ int, _ int64, candidates []persistence.SourceScanCandidateInput) error {
	fixture.batchSizes = append(fixture.batchSizes, len(candidates))
	if fixture.appendErr != nil {
		return fixture.appendErr
	}
	fixture.candidates[operationID] = append(fixture.candidates[operationID], candidates...)
	return nil
}

func newSourceScanFixture(t *testing.T) (*sourceScanRepositoryFixture, *SourceScan, SourceScanRequest) {
	t.Helper()
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repository := &sourceScanRepositoryFixture{
		root:       &persistence.SourceRoot{ID: uuid.New(), ConfiguredPath: rootPath, Enabled: true},
		candidates: make(map[uuid.UUID][]persistence.SourceScanCandidateInput),
	}
	request := SourceScanRequest{OperationID: uuid.New(), RootID: repository.root.ID, ExpectedConfiguredPath: rootPath, ExpectedAttempt: 1, ExpectedJobID: 11}
	return repository, NewSourceScan(repository), request
}

func TestSourceScanEnumeratesStatOnlyApprovedCandidates(t *testing.T) {
	repository, scan, request := newSourceScanFixture(t)
	writeSourceScanFile(t, repository.root.ConfiguredPath, filepath.Join("Album", "Track.FLAC"), "audio bytes")
	writeSourceScanFile(t, repository.root.ConfiguredPath, "notes.txt", "ignored")
	writeSourceScanFile(t, repository.root.ConfiguredPath, "album.WaV", "case-insensitive extension")
	for _, extension := range []string{"flac", "wav", "aif", "aiff", "ape", "wv", "mp3", "m4a", "aac", "ogg", "opus", "wma", "mka"} {
		writeSourceScanFile(t, repository.root.ConfiguredPath, "allowlist/track."+extension, extension)
	}

	_, err := scan.EnumerateObserved(context.Background(), request)
	if err != nil {
		t.Fatalf("enumerate source root: %v", err)
	}
	candidates := repository.candidates[request.OperationID]
	if len(candidates) != 15 {
		t.Fatalf("candidates = %#v, want the two mixed-case files and all thirteen approved extensions", candidates)
	}
	gotPaths := make([]string, len(candidates))
	for index, candidate := range candidates {
		gotPaths[index] = candidate.RelativePath
	}
	slices.Sort(gotPaths)
	if !slices.Contains(gotPaths, "Album/Track.FLAC") || !slices.Contains(gotPaths, "album.WaV") {
		t.Fatalf("candidate paths do not preserve exact spelling: %v", gotPaths)
	}
	for _, extension := range []string{"flac", "wav", "aif", "aiff", "ape", "wv", "mp3", "m4a", "aac", "ogg", "opus", "wma", "mka"} {
		if !slices.Contains(gotPaths, "allowlist/track."+extension) {
			t.Errorf("approved extension %q was not enumerated: %v", extension, gotPaths)
		}
	}
	for _, candidate := range candidates {
		if candidate.ProbeStatus != persistence.SourceProbeStatusNotAnalyzed || candidate.SizeBytes <= 0 || candidate.Mtime.IsZero() {
			t.Errorf("candidate is not a complete stat-only observation: %+v", candidate)
		}
		if candidate.SafeError != nil || candidate.PreparedAnalysis != nil || len(candidate.SourceSHA256) != 0 || candidate.FFProbeJSON != nil {
			t.Errorf("candidate includes analysis output: %+v", candidate)
		}
		if !candidate.Mtime.Equal(candidate.Mtime.Truncate(time.Microsecond)) {
			t.Errorf("candidate mtime was not truncated to PostgreSQL precision: %v", candidate.Mtime)
		}
	}
}

func TestSourceScanRequiresDeliveryIdentityAndRefusesStaleRootBeforeWrites(t *testing.T) {
	repository, scan, request := newSourceScanFixture(t)
	for _, test := range []struct {
		name   string
		mutate func(*SourceScanRequest)
		root   func(*persistence.SourceRoot)
	}{
		{name: "missing attempt", mutate: func(request *SourceScanRequest) { request.ExpectedAttempt = 0 }},
		{name: "missing job", mutate: func(request *SourceScanRequest) { request.ExpectedJobID = 0 }},
		{name: "disabled", root: func(root *persistence.SourceRoot) { root.Enabled = false }},
		{name: "path changed", mutate: func(request *SourceScanRequest) { request.ExpectedConfiguredPath += "-old" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := request
			if test.mutate != nil {
				test.mutate(&current)
			}
			if test.root != nil {
				test.root(repository.root)
			}
			before := len(repository.deletions)
			if _, err := scan.EnumerateObserved(context.Background(), current); err == nil {
				t.Fatal("invalid delivery or stale root was accepted")
			}
			if len(repository.deletions) != before {
				t.Fatal("invalid delivery or stale root reached candidate persistence")
			}
			repository.root.Enabled = true
		})
	}
}

func TestSourceScanReplacesCandidatesForTheFencedDelivery(t *testing.T) {
	repository, scan, request := newSourceScanFixture(t)
	writeSourceScanFile(t, repository.root.ConfiguredPath, "track.flac", "audio")
	repository.candidates[request.OperationID] = []persistence.SourceScanCandidateInput{{RelativePath: "stale.flac"}}
	request.ExpectedAttempt = 4
	request.ExpectedJobID = 77
	if _, err := scan.EnumerateObserved(context.Background(), request); err != nil {
		t.Fatalf("enumerate retry: %v", err)
	}
	if got, want := repository.deletions, []sourceScanCandidateDeletion{{operationID: request.OperationID, rootID: request.RootID, path: request.ExpectedConfiguredPath, attempt: 4, jobID: 77}}; !slices.Equal(got, want) {
		t.Fatalf("candidate deletion fence = %#v, want %#v", got, want)
	}
	if len(repository.candidates[request.OperationID]) != 1 || repository.candidates[request.OperationID][0].RelativePath != "track.flac" {
		t.Fatalf("retry candidates = %#v, want only the current observation", repository.candidates[request.OperationID])
	}
}

func TestSourceScanPersistsCandidatesInBoundedBatches(t *testing.T) {
	repository, scan, request := newSourceScanFixture(t)
	for index := 0; index < sourceScanCandidateBatchSize+1; index++ {
		writeSourceScanFile(t, repository.root.ConfiguredPath, filepath.Join("album", fmt.Sprintf("track-%03d.flac", index)), "audio")
	}
	if _, err := scan.EnumerateObserved(context.Background(), request); err != nil {
		t.Fatalf("enumerate source root: %v", err)
	}
	if want := []int{sourceScanCandidateBatchSize, 1}; !slices.Equal(repository.batchSizes, want) {
		t.Fatalf("persisted batch sizes = %v, want %v", repository.batchSizes, want)
	}
}

func TestSourceScanStopsWhenCandidateCleanupFails(t *testing.T) {
	repository, scan, request := newSourceScanFixture(t)
	writeSourceScanFile(t, repository.root.ConfiguredPath, "track.flac", "audio")
	wantErr := errors.New("candidate cleanup failed")
	repository.deleteErr = wantErr
	if _, err := scan.EnumerateObserved(context.Background(), request); !errors.Is(err, wantErr) {
		t.Fatalf("enumeration error = %v, want %v", err, wantErr)
	}
	if len(repository.batchSizes) != 0 {
		t.Fatalf("candidate batches after cleanup failure = %v, want none", repository.batchSizes)
	}
}

func TestSourceScanObservesChangedKnownFileBeforeCancellationAndSkipsUnchanged(t *testing.T) {
	repository, scan, request := newSourceScanFixture(t)
	writeSourceScanFile(t, repository.root.ConfiguredPath, "track.flac", "changed audio")
	path := filepath.Join(repository.root.ConfiguredPath, "track.flac")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	workID := uuid.New()
	target := persistence.SourceScanIdentityTarget{
		LocationID: uuid.New(), WorkID: &workID, RelativePath: "track.flac",
		SizeBytes: info.Size() - 1, Mtime: sourceScanMtime(info.ModTime()).Add(-time.Second),
	}
	repository.identityTargets = []persistence.SourceScanIdentityTarget{target}
	ctx, cancel := context.WithCancel(context.Background())
	repository.observe = func(persistence.SourceChangedFileObservation) error {
		cancel()
		return nil
	}
	if _, err := scan.EnumerateObserved(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("enumeration error = %v, want cancellation after observation", err)
	}
	if len(repository.identityFences) != 1 {
		t.Fatalf("identity target loads = %d, want exactly one", len(repository.identityFences))
	}
	wantFence := persistence.SourceScanIdentityFence{
		OperationID: request.OperationID, RootID: request.RootID, ExpectedAttempt: request.ExpectedAttempt,
		ExpectedJobID: request.ExpectedJobID, ExpectedConfiguredPath: repository.root.ConfiguredPath,
	}
	if repository.identityFences[0] != wantFence {
		t.Fatalf("identity fence = %+v, want %+v", repository.identityFences[0], wantFence)
	}
	if len(repository.observations) != 1 {
		t.Fatalf("changed-file observations = %d, want one before cancellation", len(repository.observations))
	}
	observation := repository.observations[0]
	if observation.Fence != wantFence || observation.Target != target || observation.SizeBytes != info.Size() || !observation.Mtime.Equal(sourceScanMtime(info.ModTime())) {
		t.Fatalf("changed-file observation = %+v, want fenced target and observed facts", observation)
	}
	if len(repository.batchSizes) != 0 {
		t.Fatalf("candidate batches after cancellation = %v, want none", repository.batchSizes)
	}

	unchangedRepository, unchangedScan, unchangedRequest := newSourceScanFixture(t)
	writeSourceScanFile(t, unchangedRepository.root.ConfiguredPath, "track.flac", "same audio")
	unchangedInfo, err := os.Stat(filepath.Join(unchangedRepository.root.ConfiguredPath, "track.flac"))
	if err != nil {
		t.Fatal(err)
	}
	unchangedRepository.identityTargets = []persistence.SourceScanIdentityTarget{{
		LocationID: uuid.New(), RelativePath: "track.flac", SizeBytes: unchangedInfo.Size(), Mtime: sourceScanMtime(unchangedInfo.ModTime()),
	}}
	if _, err := unchangedScan.EnumerateObserved(context.Background(), unchangedRequest); err != nil {
		t.Fatalf("enumerate unchanged source root: %v", err)
	}
	if len(unchangedRepository.identityFences) != 1 || len(unchangedRepository.observations) != 0 {
		t.Fatalf("unchanged identity loads/observations = %d/%d, want one load and no observation", len(unchangedRepository.identityFences), len(unchangedRepository.observations))
	}
}

func TestSourceScanStopsAfterCandidateAppendFailure(t *testing.T) {
	repository, scan, request := newSourceScanFixture(t)
	for index := 0; index < sourceScanCandidateBatchSize+1; index++ {
		writeSourceScanFile(t, repository.root.ConfiguredPath, filepath.Join("album", fmt.Sprintf("track-%03d.flac", index)), "audio")
	}
	wantErr := errors.New("candidate append failed")
	repository.appendErr = wantErr
	if _, err := scan.EnumerateObserved(context.Background(), request); !errors.Is(err, wantErr) {
		t.Fatalf("enumeration error = %v, want %v", err, wantErr)
	}
	if want := []int{sourceScanCandidateBatchSize}; !slices.Equal(repository.batchSizes, want) {
		t.Fatalf("append attempts = %v, want no batch after first failure %v", repository.batchSizes, want)
	}
}

func TestSourceScanSkipsSymlinksWithoutLeavingTheRoot(t *testing.T) {
	repository, scan, request := newSourceScanFixture(t)
	outside := t.TempDir()
	writeSourceScanFile(t, repository.root.ConfiguredPath, "inside.flac", "inside")
	writeSourceScanFile(t, outside, "outside.flac", "outside")
	if err := os.Symlink(filepath.Join(outside, "outside.flac"), filepath.Join(repository.root.ConfiguredPath, "escape.flac")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := scan.EnumerateObserved(context.Background(), request); err != nil {
		t.Fatalf("enumerate source root: %v", err)
	}
	if got := len(repository.candidates[request.OperationID]); got != 1 || repository.candidates[request.OperationID][0].RelativePath != "inside.flac" {
		t.Fatalf("candidates = %#v, want only inside.flac", repository.candidates[request.OperationID])
	}
}

func TestSourceScanFiltersCandidatesCoveredByUnreadableScopes(t *testing.T) {
	repository, _, request := newSourceScanFixture(t)
	path := filepath.Join(repository.root.ConfiguredPath, "partially-read.flac")
	if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat(repository.root.ConfiguredPath)
	if err != nil {
		t.Fatal(err)
	}
	fakeRoot := &sourceWalkFakeDirectory{
		entries: []sourcefs.Entry{{Name: "partially-read.flac", Kind: sourcefs.KindRegular}},
		readErr: fs.ErrPermission,
		files:   map[string]*sourceWalkFakeFile{"partially-read.flac": {info: fileInfo}},
		info:    rootInfo,
	}
	scan := NewSourceScan(repository, WithSourceScanOpener(sourceEnumerationTestOpener{root: fakeRoot}))
	scopes, err := scan.EnumerateObserved(context.Background(), request)
	if err != nil {
		t.Fatalf("enumerate partially unreadable root: %v", err)
	}
	if len(scopes) != 1 || scopes[0] != (SourceEnumerationScope{Kind: "root"}) {
		t.Fatalf("unreadable scopes = %#v, want the root scope", scopes)
	}
	if candidates := repository.candidates[request.OperationID]; len(candidates) != 0 {
		t.Fatalf("candidates covered by unreadable scope were persisted: %#v", candidates)
	}
}

func writeSourceScanFile(t *testing.T, root, relative, contents string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
