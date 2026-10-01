package service_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// A scan consumes the walker, the managed probe and the inventory repository
// through interfaces; these assertions pin the production types to them without
// starting a database or a real ffprobe.
var (
	_ service.SourceProbe          = (*tools.FFProbe)(nil)
	_ service.SourceScanRepository = (*persistence.SourceInventoryRepository)(nil)
	_ service.SourceScanStages     = (*service.Operations)(nil)
)

type sourceScanProbeAnswer struct {
	hasAudio bool
	err      error
}

// sourceScanProbeFixture stands in for the managed ffprobe: it answers per file
// name, records every probed file and lets a test change the tree at the exact
// moment a file is probed, which is the deterministic barrier for a file that
// changes under the scan.
type sourceScanProbeFixture struct {
	answers map[string]sourceScanProbeAnswer
	probed  []string
	onProbe func(name, absolutePath string)
}

func newSourceScanProbeFixture() *sourceScanProbeFixture {
	return &sourceScanProbeFixture{answers: map[string]sourceScanProbeAnswer{}}
}

func (fixture *sourceScanProbeFixture) answer(name string, hasAudio bool) {
	fixture.answers[name] = sourceScanProbeAnswer{hasAudio: hasAudio}
}

func (fixture *sourceScanProbeFixture) fail(name string, err error) {
	fixture.answers[name] = sourceScanProbeAnswer{err: err}
}

func (fixture *sourceScanProbeFixture) probes() []string {
	probed := slices.Clone(fixture.probed)
	slices.Sort(probed)
	return probed
}

func (fixture *sourceScanProbeFixture) Probe(_ context.Context, absoluteServerPath string) (bool, error) {
	name := filepath.Base(absoluteServerPath)
	fixture.probed = append(fixture.probed, name)
	if fixture.onProbe != nil {
		fixture.onProbe(name, absoluteServerPath)
	}
	answer, known := fixture.answers[name]
	if !known {
		return true, nil
	}
	return answer.hasAudio, answer.err
}

// errSourceScanStageReport is what the stages fixture returns for the one stage
// a test makes fail, so a scan that dies after its traversal is observable.
var errSourceScanStageReport = errors.New("the operation could not be advanced")

type sourceScanStagesFixture struct {
	operationID uuid.UUID
	stages      []string
	// failAt names the stage whose report fails. Every attempted stage is still
	// recorded, so a test can see how far a scan got before it died.
	failAt string
}

func (fixture *sourceScanStagesFixture) Running(_ context.Context, operationID uuid.UUID, stage string) error {
	fixture.operationID = operationID
	fixture.stages = append(fixture.stages, stage)
	if stage == fixture.failAt {
		return errSourceScanStageReport
	}
	return nil
}

// sourceScanRepositoryFixture plays the inventory repository contract a
// traversal depends on: the locations of the last applied generation, paginated
// exactly like PostgreSQL, and the candidate rows of one operation. It records
// every call, so the order a retry cleans up in is observable, and it keeps the
// locations immutable because no scan may write one.
type sourceScanRepositoryFixture struct {
	root       *persistence.SourceRoot
	locations  []persistence.SourceLocation
	candidates map[uuid.UUID][]persistence.SourceScanCandidateInput
	events     []string
	batchSizes []int
	pageLimits []int
	deleteErr  error
	appendErr  error
}

func newSourceScanRepositoryFixture(root *persistence.SourceRoot) *sourceScanRepositoryFixture {
	return &sourceScanRepositoryFixture{
		root: root, candidates: map[uuid.UUID][]persistence.SourceScanCandidateInput{},
	}
}

func (fixture *sourceScanRepositoryFixture) GetSourceRoot(_ context.Context, id uuid.UUID) (*persistence.SourceRoot, error) {
	if fixture.root == nil || fixture.root.ID != id {
		return nil, fmt.Errorf("get source root: no rows in result set")
	}
	return fixture.root, nil
}

func (fixture *sourceScanRepositoryFixture) ListSourceLocationsPage(_ context.Context, rootID uuid.UUID, cursor *persistence.SourceLocationCursor, limit int) ([]persistence.SourceLocation, *persistence.SourceLocationCursor, error) {
	fixture.pageLimits = append(fixture.pageLimits, limit)
	page := make([]persistence.SourceLocation, 0, limit)
	for _, location := range fixture.locations {
		if location.SourceRootID != rootID || len(page) == limit {
			continue
		}
		if cursor != nil && !sourceScanAfterCursor(location, *cursor) {
			continue
		}
		page = append(page, location)
	}
	if len(page) < limit {
		return page, nil, nil
	}
	last := page[len(page)-1]
	return page, &persistence.SourceLocationCursor{RelativePath: last.RelativePath, ID: last.ID}, nil
}

func (fixture *sourceScanRepositoryFixture) DeleteSourceScanCandidates(_ context.Context, operationID uuid.UUID) error {
	fixture.events = append(fixture.events, "delete "+operationID.String())
	if fixture.deleteErr != nil {
		return fixture.deleteErr
	}
	delete(fixture.candidates, operationID)
	return nil
}

func (fixture *sourceScanRepositoryFixture) AppendSourceScanCandidates(_ context.Context, operationID uuid.UUID, batch []persistence.SourceScanCandidateInput) error {
	fixture.events = append(fixture.events, fmt.Sprintf("append %s %d", operationID, len(batch)))
	fixture.batchSizes = append(fixture.batchSizes, len(batch))
	if fixture.appendErr != nil {
		return fixture.appendErr
	}
	fixture.candidates[operationID] = append(fixture.candidates[operationID], batch...)
	return nil
}

func sourceScanAfterCursor(location persistence.SourceLocation, cursor persistence.SourceLocationCursor) bool {
	if location.RelativePath != cursor.RelativePath {
		return location.RelativePath > cursor.RelativePath
	}
	return location.ID.String() > cursor.ID.String()
}

type sourceScanFixture struct {
	scan        *service.SourceScan
	repository  *sourceScanRepositoryFixture
	probe       *sourceScanProbeFixture
	stages      *sourceScanStagesFixture
	root        *persistence.SourceRoot
	operationID uuid.UUID
	tree        string
}

func newSourceScanFixture(t *testing.T) *sourceScanFixture {
	t.Helper()
	tree := t.TempDir()
	path := tree
	root := &persistence.SourceRoot{
		ID: uuid.New(), DisplayName: "music", ConfiguredPath: path, Enabled: true, InventoryPath: &path,
	}
	repository := newSourceScanRepositoryFixture(root)
	probe := newSourceScanProbeFixture()
	stages := &sourceScanStagesFixture{}
	return &sourceScanFixture{
		scan: service.NewSourceScan(repository, probe, stages), repository: repository, probe: probe,
		stages: stages, root: root, operationID: uuid.New(), tree: tree,
	}
}

func (fixture *sourceScanFixture) write(t *testing.T, relativePath, content string) string {
	t.Helper()
	absolute := filepath.Join(fixture.tree, relativePath)
	writeSourceWalkFile(t, absolute, content)
	return absolute
}

// storeLocation records the inventory row a successful scan of the tree would
// have left behind: the file's own facts at the microsecond resolution
// PostgreSQL keeps them in.
func (fixture *sourceScanFixture) storeLocation(t *testing.T, relativePath, status string) {
	t.Helper()
	info, err := os.Stat(filepath.Join(fixture.tree, relativePath))
	if err != nil {
		t.Fatalf("stat %q: %v", relativePath, err)
	}
	fixture.repository.locations = append(fixture.repository.locations, persistence.SourceLocation{
		ID: uuid.New(), SourceRootID: fixture.root.ID, RelativePath: relativePath,
		SizeBytes: info.Size(), Mtime: info.ModTime().Truncate(time.Microsecond), ProbeStatus: status,
	})
}

func (fixture *sourceScanFixture) run(t *testing.T) error {
	t.Helper()
	return fixture.scan.Run(context.Background(), service.SourceScanRequest{
		OperationID: fixture.operationID, RootID: fixture.root.ID,
	})
}

func (fixture *sourceScanFixture) candidates(t *testing.T) []persistence.SourceScanCandidateInput {
	t.Helper()
	candidates := slices.Clone(fixture.repository.candidates[fixture.operationID])
	slices.SortFunc(candidates, func(left, right persistence.SourceScanCandidateInput) int {
		return strings.Compare(left.RelativePath, right.RelativePath)
	})
	return candidates
}

func (fixture *sourceScanFixture) statuses(t *testing.T) map[string]persistence.SourceScanCandidateInput {
	t.Helper()
	stored := map[string]persistence.SourceScanCandidateInput{}
	for _, candidate := range fixture.candidates(t) {
		if _, duplicate := stored[candidate.RelativePath]; duplicate {
			t.Fatalf("candidate %q was stored twice", candidate.RelativePath)
		}
		stored[candidate.RelativePath] = candidate
	}
	return stored
}

func sourceScanCandidatePaths(candidates []persistence.SourceScanCandidateInput) []string {
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		paths = append(paths, candidate.RelativePath)
	}
	return paths
}

func requireSourceScanStatus(t *testing.T, stored map[string]persistence.SourceScanCandidateInput, relativePath, status string) {
	t.Helper()
	candidate, known := stored[relativePath]
	if !known {
		t.Fatalf("no candidate for %q, stored %v", relativePath, stored)
	}
	if candidate.ProbeStatus != status {
		t.Fatalf("candidate %q status = %q, want %q", relativePath, candidate.ProbeStatus, status)
	}
	if candidate.SafeError != nil {
		t.Fatalf("candidate %q carries the safe error %q, want none", relativePath, *candidate.SafeError)
	}
}

func TestSourceScanKeepsTheStoredStatusOfUnchangedFilesAndRechecksAFailedOne(t *testing.T) {
	fixture := newSourceScanFixture(t)
	fixture.write(t, "album/track.flac", "audio bytes")
	fixture.write(t, "album/silent.mka", "video only bytes")
	fixture.write(t, "album/broken.wav", "unreadable bytes")
	fixture.storeLocation(t, "album/track.flac", persistence.SourceProbeStatusAudio)
	fixture.storeLocation(t, "album/silent.mka", persistence.SourceProbeStatusNoAudio)
	fixture.storeLocation(t, "album/broken.wav", persistence.SourceProbeStatusProbeError)
	// A probe of the two confirmed files would answer the opposite of their
	// stored status: the scan must reuse the stored one instead.
	fixture.probe.answer("track.flac", false)
	fixture.probe.answer("silent.mka", true)
	fixture.probe.answer("broken.wav", false)

	err := fixture.run(t)

	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got, want := fixture.probe.probes(), []string{"broken.wav"}; !slices.Equal(got, want) {
		t.Errorf("probed %v, want only %v: an unchanged audio or no_audio file is not probed again", got, want)
	}
	stored := fixture.statuses(t)
	requireSourceScanStatus(t, stored, "album/track.flac", persistence.SourceProbeStatusAudio)
	requireSourceScanStatus(t, stored, "album/silent.mka", persistence.SourceProbeStatusNoAudio)
	requireSourceScanStatus(t, stored, "album/broken.wav", persistence.SourceProbeStatusNoAudio)
}

func TestSourceScanProbesNewFilesAndFilesWhoseSizeOrMtimeChanged(t *testing.T) {
	fixture := newSourceScanFixture(t)
	fixture.write(t, "album/kept.ogg", "unchanged bytes")
	fixture.write(t, "album/grown.wav", "short")
	fixture.write(t, "album/touched.ape", "same size bytes")
	fixture.write(t, "album/new.flac", "brand new bytes")
	fixture.storeLocation(t, "album/kept.ogg", persistence.SourceProbeStatusAudio)
	fixture.storeLocation(t, "album/grown.wav", persistence.SourceProbeStatusAudio)
	fixture.storeLocation(t, "album/touched.ape", persistence.SourceProbeStatusNoAudio)
	fixture.probe.answer("grown.wav", false)
	fixture.probe.answer("touched.ape", true)
	fixture.probe.answer("new.flac", true)
	// The file grows after its location was recorded...
	fixture.write(t, "album/grown.wav", "a payload that is longer than the recorded one")
	// ...while the other keeps its size and only gets another mtime.
	moved := time.Now().Add(-2 * time.Hour).Truncate(time.Microsecond)
	if err := os.Chtimes(filepath.Join(fixture.tree, "album", "touched.ape"), moved, moved); err != nil {
		t.Fatalf("stamp the changed file: %v", err)
	}

	err := fixture.run(t)

	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got, want := fixture.probe.probes(), []string{"grown.wav", "new.flac", "touched.ape"}; !slices.Equal(got, want) {
		t.Fatalf("probed %v, want %v", got, want)
	}
	stored := fixture.statuses(t)
	requireSourceScanStatus(t, stored, "album/kept.ogg", persistence.SourceProbeStatusAudio)
	requireSourceScanStatus(t, stored, "album/grown.wav", persistence.SourceProbeStatusNoAudio)
	requireSourceScanStatus(t, stored, "album/touched.ape", persistence.SourceProbeStatusAudio)
	requireSourceScanStatus(t, stored, "album/new.flac", persistence.SourceProbeStatusAudio)
}

func TestSourceScanRecordsAProbeFailureAsAProbeErrorAndKeepsScanning(t *testing.T) {
	fixture := newSourceScanFixture(t)
	fixture.write(t, "album/broken.wav", "unreadable bytes")
	fixture.write(t, "album/good.flac", "audio bytes")
	diagnostic := errors.New("ffprobe failed with exit status 1 and an upstream diagnostic")
	fixture.probe.fail("broken.wav", diagnostic)

	err := fixture.run(t)

	if err != nil {
		t.Fatalf("a failed probe of one file must not fail the whole scan: %v", err)
	}
	stored := fixture.statuses(t)
	broken, known := stored["album/broken.wav"]
	if !known {
		t.Fatalf("no candidate for the unprobeable file, stored %v", stored)
	}
	if broken.ProbeStatus != persistence.SourceProbeStatusProbeError {
		t.Fatalf("status of the unprobeable file = %q, want %q", broken.ProbeStatus, persistence.SourceProbeStatusProbeError)
	}
	if broken.SafeError == nil {
		t.Fatal("the probe_error candidate carries no safe reason")
	}
	for _, fragment := range []string{diagnostic.Error(), "broken.wav", fixture.tree} {
		if strings.Contains(*broken.SafeError, fragment) {
			t.Errorf("safe reason %q leaks %q", *broken.SafeError, fragment)
		}
	}
	requireSourceScanStatus(t, stored, "album/good.flac", persistence.SourceProbeStatusAudio)
	if got, want := fixture.probe.probes(), []string{"broken.wav", "good.flac"}; !slices.Equal(got, want) {
		t.Errorf("probed %v, want every file of the tree", got)
	}
}

func TestSourceScanFailsWhenAFileChangesUnderItsProbe(t *testing.T) {
	fixture := newSourceScanFixture(t)
	path := fixture.write(t, "album/track.flac", "audio bytes")
	fixture.repository.candidates[fixture.operationID] = []persistence.SourceScanCandidateInput{{
		RelativePath: "album/leftover.flac", SizeBytes: 1, Mtime: time.Now().UTC().Truncate(time.Microsecond),
		ProbeStatus: persistence.SourceProbeStatusAudio,
	}}
	fixture.probe.onProbe = func(name, _ string) {
		if name != "track.flac" {
			return
		}
		if err := os.WriteFile(path, []byte("a payload written while the probe was running"), 0o644); err != nil {
			t.Errorf("rewrite the probed file: %v", err)
		}
	}

	err := fixture.run(t)

	if err == nil {
		t.Fatal("a file that changed under its probe did not fail the scan")
	}
	if !strings.Contains(err.Error(), "changed while it was being scanned") {
		t.Fatalf("scan error = %v, want a changing-file failure", err)
	}
	if stored := fixture.candidates(t); len(stored) != 0 {
		t.Fatalf("the failed scan left the candidates %v", sourceScanCandidatePaths(stored))
	}
}

func TestSourceScanFailsWhenATraversedFileChangedBeforeTheSnapshotCompleted(t *testing.T) {
	fixture := newSourceScanFixture(t)
	// The victim is walked before the file whose probe changes it, which is what
	// makes the change land after the traversal read the victim.
	untouched := fixture.write(t, "album/untouched.wav", "untouched bytes")
	fixture.storeLocation(t, "album/untouched.wav", persistence.SourceProbeStatusAudio)
	fixture.write(t, "zebra/probed.flac", "audio bytes")
	fixture.probe.onProbe = func(name, _ string) {
		if name != "probed.flac" {
			return
		}
		if err := os.WriteFile(untouched, []byte("a payload written while another file was probed"), 0o644); err != nil {
			t.Errorf("rewrite the file that is not probed: %v", err)
		}
	}

	err := fixture.run(t)

	if err == nil {
		t.Fatal("a file that changed after the traversal did not fail the scan")
	}
	if !strings.Contains(err.Error(), "untouched.wav") {
		t.Fatalf("scan error = %v, want the changed file named", err)
	}
	if got, want := fixture.stages.stages, []string{service.SourceScanStageTraversing, service.SourceScanStageApplying}; !slices.Equal(got, want) {
		t.Fatalf("stages = %v, want %v", got, want)
	}
	if stored := fixture.candidates(t); len(stored) != 0 {
		t.Fatalf("the failed scan left the candidates %v", sourceScanCandidatePaths(stored))
	}
}

func TestSourceScanFailsWhenATraversedFileBecomesASymlink(t *testing.T) {
	fixture := newSourceScanFixture(t)
	replaced := fixture.write(t, "album/replaced.wav", "audio bytes")
	fixture.storeLocation(t, "album/replaced.wav", persistence.SourceProbeStatusAudio)
	fixture.write(t, "zebra/probed.flac", "audio bytes")
	outside := filepath.Join(t.TempDir(), "outside.wav")
	if err := os.WriteFile(outside, []byte("audio bytes"), 0o644); err != nil {
		t.Fatalf("write the file outside the root: %v", err)
	}
	fixture.probe.onProbe = func(name, _ string) {
		if name != "probed.flac" {
			return
		}
		if err := os.Remove(replaced); err != nil {
			t.Errorf("remove the traversed file: %v", err)
			return
		}
		sourceWalkSymlink(t, outside, replaced)
	}

	err := fixture.run(t)

	if err == nil {
		t.Fatal("a traversed file replaced by a symlink did not fail the scan")
	}
	if !strings.Contains(err.Error(), "no longer a regular file") {
		t.Fatalf("scan error = %v, want a symlink failure", err)
	}
}

func TestSourceScanFailsWhenTheTreeCannotBeRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	fixture := newSourceScanFixture(t)
	fixture.write(t, "album/track.flac", "audio bytes")
	fixture.storeLocation(t, "album/track.flac", persistence.SourceProbeStatusAudio)
	fixture.write(t, "locked/hidden.flac", "behind an unreadable directory")
	locked := filepath.Join(fixture.tree, "locked")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("lock the subdirectory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	before := slices.Clone(fixture.repository.locations)

	err := fixture.run(t)

	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("scan error = %v, want an unreadable subtree failure", err)
	}
	if stored := fixture.candidates(t); len(stored) != 0 {
		t.Fatalf("the failed scan left the candidates %v", sourceScanCandidatePaths(stored))
	}
	if !reflect.DeepEqual(fixture.repository.locations, before) {
		t.Fatalf("the failed scan changed the inventory to %v", fixture.repository.locations)
	}
}

func TestSourceScanFailsWhenCanceledDuringAProbe(t *testing.T) {
	fixture := newSourceScanFixture(t)
	fixture.write(t, "album/track.flac", "audio bytes")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.probe.onProbe = func(string, string) { cancel() }
	fixture.probe.fail("track.flac", errors.New("ffprobe was interrupted"))

	err := fixture.scan.Run(ctx, service.SourceScanRequest{OperationID: fixture.operationID, RootID: fixture.root.ID})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("scan error = %v, want the cancellation the probe died of", err)
	}
	if errors.Is(err, service.ErrSourceRootInaccessible) {
		t.Fatalf("a canceled scan = %v, want no root access marker", err)
	}
	if stored := fixture.candidates(t); len(stored) != 0 {
		t.Fatalf("a canceled scan recorded the candidates %v", sourceScanCandidatePaths(stored))
	}
}

func TestSourceScanFailsWhenCanceledDuringTraversal(t *testing.T) {
	fixture := newSourceScanFixture(t)
	fixture.write(t, "album/a.flac", "first")
	fixture.write(t, "album/b.flac", "second")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.probe.onProbe = func(string, string) { cancel() }

	err := fixture.scan.Run(ctx, service.SourceScanRequest{OperationID: fixture.operationID, RootID: fixture.root.ID})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("scan error = %v, want a cancellation", err)
	}
	if errors.Is(err, service.ErrSourceRootInaccessible) {
		t.Fatalf("a canceled scan = %v, want no root access marker", err)
	}
	if stored := fixture.candidates(t); len(stored) != 0 {
		t.Fatalf("a canceled scan recorded the candidates %v", sourceScanCandidatePaths(stored))
	}
}

func TestSourceScanRefusesADisabledRoot(t *testing.T) {
	fixture := newSourceScanFixture(t)
	fixture.write(t, "album/track.flac", "audio bytes")
	fixture.root.Enabled = false

	err := fixture.run(t)

	if err == nil {
		t.Fatal("a disabled root was scanned")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("scan error = %v, want a disabled-root failure", err)
	}
	if len(fixture.repository.pageLimits) != 0 {
		t.Fatalf("the inventory of a disabled root was read: pages %v", fixture.repository.pageLimits)
	}
	if len(fixture.repository.events) != 0 || len(fixture.stages.stages) != 0 {
		t.Fatalf("a disabled root was scanned further: %v, %v", fixture.repository.events, fixture.stages.stages)
	}
}

func TestSourceScanProbesEveryFileOfARootWhosePathChanged(t *testing.T) {
	fixture := newSourceScanFixture(t)
	fixture.write(t, "album/track.flac", "audio bytes")
	fixture.write(t, "album/silent.mka", "video only bytes")
	fixture.storeLocation(t, "album/track.flac", persistence.SourceProbeStatusAudio)
	fixture.storeLocation(t, "album/silent.mka", persistence.SourceProbeStatusNoAudio)
	// The inventory describes the path the root carried before it was edited.
	previous := filepath.Join(t.TempDir(), "previous")
	fixture.root.InventoryPath = &previous
	fixture.probe.answer("track.flac", false)
	fixture.probe.answer("silent.mka", true)

	err := fixture.run(t)

	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got, want := fixture.probe.probes(), []string{"silent.mka", "track.flac"}; !slices.Equal(got, want) {
		t.Fatalf("probed %v, want every file of the new path", got)
	}
	stored := fixture.statuses(t)
	requireSourceScanStatus(t, stored, "album/track.flac", persistence.SourceProbeStatusNoAudio)
	requireSourceScanStatus(t, stored, "album/silent.mka", persistence.SourceProbeStatusAudio)
	if len(fixture.repository.pageLimits) != 0 {
		t.Fatalf("the inventory of the previous path was read: pages %v", fixture.repository.pageLimits)
	}
}

func TestSourceScanReplacesTheCandidatesOfAnEarlierAttempt(t *testing.T) {
	fixture := newSourceScanFixture(t)
	fixture.write(t, "album/track.flac", "audio bytes")
	fixture.repository.candidates[fixture.operationID] = []persistence.SourceScanCandidateInput{{
		RelativePath: "album/gone.flac", SizeBytes: 1, Mtime: time.Now().UTC().Truncate(time.Microsecond),
		ProbeStatus: persistence.SourceProbeStatusAudio,
	}}

	err := fixture.run(t)

	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if stored := fixture.candidates(t); len(stored) != 1 || stored[0].RelativePath != "album/track.flac" {
		t.Fatalf("candidates after the retry = %v, want the new traversal alone", sourceScanCandidatePaths(stored))
	}
	want := []string{"delete " + fixture.operationID.String(), "append " + fixture.operationID.String() + " 1"}
	if !slices.Equal(fixture.repository.events, want) {
		t.Fatalf("repository calls = %v, want the candidates of the earlier attempt dropped first", fixture.repository.events)
	}
}

func TestSourceScanStoresOnlyApprovedAudioFiles(t *testing.T) {
	fixture := newSourceScanFixture(t)
	fixture.write(t, "cover.jpg", "not audio")
	fixture.write(t, "notes.txt", "not audio")
	fixture.write(t, "README", "not audio")
	fixture.write(t, filepath.Join("album", "TRACK.FLAC"), "audio bytes")
	fixture.write(t, "album/picture.flac.bak", "not audio")

	err := fixture.run(t)

	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	stored := fixture.candidates(t)
	if got, want := sourceScanCandidatePaths(stored), []string{filepath.Join("album", "TRACK.FLAC")}; !slices.Equal(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	if got, want := fixture.probe.probes(), []string{"TRACK.FLAC"}; !slices.Equal(got, want) {
		t.Fatalf("probed %v, want only the approved file", got)
	}
}

// The scan reports stages and nothing else: SourceScanStages carries no byte
// counter, so a scan can neither publish a fake percentage nor count files as
// bytes.
func TestSourceScanReportsOnlyTheCoarseStages(t *testing.T) {
	fixture := newSourceScanFixture(t)
	fixture.write(t, "album/track.flac", "audio bytes")

	err := fixture.run(t)

	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := []string{service.SourceScanStageTraversing, service.SourceScanStageApplying}
	if !slices.Equal(fixture.stages.stages, want) {
		t.Fatalf("stages = %v, want %v", fixture.stages.stages, want)
	}
	if fixture.stages.operationID != fixture.operationID {
		t.Fatalf("stages were reported for operation %s, want %s", fixture.stages.operationID, fixture.operationID)
	}
}

func TestSourceScanReadsEveryPageOfThePreviousInventory(t *testing.T) {
	fixture := newSourceScanFixture(t)
	for index := 0; index < 501; index++ {
		relative := filepath.Join("album", fmt.Sprintf("file-%03d.flac", index))
		fixture.write(t, relative, "audio bytes")
		fixture.storeLocation(t, relative, persistence.SourceProbeStatusAudio)
	}

	err := fixture.run(t)

	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if probed := fixture.probe.probes(); len(probed) != 0 {
		t.Fatalf("probed %d files, want none: every file already carries a confirmed status", len(probed))
	}
	if want := []int{500, 500}; !slices.Equal(fixture.repository.pageLimits, want) {
		t.Fatalf("inventory pages = %v, want %v", fixture.repository.pageLimits, want)
	}
}

func TestSourceScanStoresCandidatesInBoundedBatches(t *testing.T) {
	fixture := newSourceScanFixture(t)
	for index := 0; index < 501; index++ {
		fixture.write(t, filepath.Join("album", fmt.Sprintf("file-%03d.flac", index)), "audio bytes")
	}

	err := fixture.run(t)

	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got, want := len(fixture.candidates(t)), 501; got != want {
		t.Fatalf("stored %d candidates, want %d", got, want)
	}
	if want := []int{500, 1}; !slices.Equal(fixture.repository.batchSizes, want) {
		t.Fatalf("candidate batches = %v, want %v", fixture.repository.batchSizes, want)
	}
}

func TestSourceScanClearsStoredBatchesWhenTheTraversalFails(t *testing.T) {
	fixture := newSourceScanFixture(t)
	for index := 0; index < 501; index++ {
		fixture.write(t, filepath.Join("album", fmt.Sprintf("file-%03d.flac", index)), "audio bytes")
	}
	last := filepath.Join(fixture.tree, "album", "file-500.flac")
	fixture.probe.onProbe = func(name, _ string) {
		if name != "file-500.flac" {
			return
		}
		if err := os.WriteFile(last, []byte("a payload written after the traversal read the file"), 0o644); err != nil {
			t.Errorf("rewrite the last traversed file: %v", err)
		}
	}

	err := fixture.run(t)

	if err == nil {
		t.Fatal("a file that changed under its probe did not fail the scan")
	}
	if len(fixture.repository.batchSizes) == 0 {
		t.Fatal("the traversal stored no batch before it failed, so the failing attempt already had candidates was not exercised")
	}
	if stored := fixture.candidates(t); len(stored) != 0 {
		t.Fatalf("the failed scan left %d stored candidates", len(stored))
	}
}

// A probe that fails must still find the file it read. The check right after the
// probe is the only place that can see a file which was being written while that
// probe ran: here the file is put back byte for byte and stamp for stamp while a
// later file is probed, so the confirmation that runs once the traversal is over
// reads a file that looks untouched. The scan must fail instead of remembering a
// probe_error for a file that was in flux.
func TestSourceScanFailsWhenAFileChangesUnderAFailingProbe(t *testing.T) {
	fixture := newSourceScanFixture(t)
	victim := fixture.write(t, "album/a-broken.wav", "unreadable bytes")
	fixture.probe.fail("a-broken.wav", errors.New("ffprobe failed with exit status 1 and an upstream diagnostic"))
	original, err := os.Stat(victim)
	if err != nil {
		t.Fatalf("stat the probed file: %v", err)
	}
	fixture.probe.onProbe = func(name, _ string) {
		switch name {
		case "a-broken.wav":
			writeSourceWalkFile(t, victim, "a payload written while the probe was running")
		case "z-later.flac":
			writeSourceWalkFile(t, victim, "unreadable bytes")
			if err := os.Chtimes(victim, original.ModTime(), original.ModTime()); err != nil {
				t.Errorf("restore the stamp of the probed file: %v", err)
			}
		}
	}
	fixture.write(t, "album/z-later.flac", "audio bytes")

	scanErr := fixture.run(t)

	if scanErr == nil {
		t.Fatal("a file that changed under a failing probe was accepted as a probe_error")
	}
	if !strings.Contains(scanErr.Error(), "changed while it was being scanned") {
		t.Fatalf("scan error = %v, want a changing-file failure", scanErr)
	}
	if stored := fixture.candidates(t); len(stored) != 0 {
		t.Fatalf("the failed scan left the candidates %v", sourceScanCandidatePaths(stored))
	}
}

// A traversal stores its full batches while it walks, so the stage report that
// closes the traversal can fail with candidates already durable. Such a scan
// never reported its snapshot complete: the batches must be dropped, exactly as
// they are when the traversal itself fails.
func TestSourceScanDropsStoredBatchesWhenTheApplyingStageFails(t *testing.T) {
	fixture := newSourceScanFixture(t)
	for index := 0; index < 501; index++ {
		fixture.write(t, filepath.Join("album", fmt.Sprintf("file-%03d.flac", index)), "audio bytes")
	}
	fixture.stages.failAt = service.SourceScanStageApplying

	scanErr := fixture.run(t)

	if !errors.Is(scanErr, errSourceScanStageReport) {
		t.Fatalf("scan error = %v, want the failed %s stage report", scanErr, service.SourceScanStageApplying)
	}
	if want := []int{500}; !slices.Equal(fixture.repository.batchSizes, want) {
		t.Fatalf("candidate batches = %v, want %v: the stage must fail after a batch is already stored", fixture.repository.batchSizes, want)
	}
	if stored := fixture.candidates(t); len(stored) != 0 {
		t.Fatalf("the failed scan left %d stored candidates", len(stored))
	}
	if want := []string{service.SourceScanStageTraversing, service.SourceScanStageApplying}; !slices.Equal(fixture.stages.stages, want) {
		t.Fatalf("stages = %v, want %v", fixture.stages.stages, want)
	}
}
