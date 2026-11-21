package service

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type matchingLocalLayoutStageRepository struct {
	headers      persistence.MatchingLocalSelectionCatalog
	layouts      map[uuid.UUID]persistence.MatchingRelease
	digest       string
	hydrated     []uuid.UUID
	hydrateCalls int
}

func (repository *matchingLocalLayoutStageRepository) ListMatchingLocalSelectionHeaders(context.Context) (persistence.MatchingLocalSelectionCatalog, error) {
	return repository.headers, nil
}

func (repository *matchingLocalLayoutStageRepository) HydrateMatchingLocalSelection(_ context.Context, releaseIDs []uuid.UUID) (persistence.MatchingLocalSelectionSnapshot, error) {
	repository.hydrateCalls++
	repository.hydrated = append([]uuid.UUID(nil), releaseIDs...)
	releases := make([]persistence.MatchingRelease, 0, len(releaseIDs))
	for _, releaseID := range releaseIDs {
		release, ok := repository.layouts[releaseID]
		if !ok {
			return persistence.MatchingLocalSelectionSnapshot{}, fmt.Errorf("unexpected Local hydration of %s", releaseID)
		}
		releases = append(releases, release)
	}
	return persistence.MatchingLocalSelectionSnapshot{Releases: releases, Digest: repository.digest}, nil
}

func TestMatchingLocalCatalogSnapshotServiceCalculateLayoutHydratesOnlyShortlist(t *testing.T) {
	t.Parallel()
	root := uuid.New()
	firstFile, secondFile := uuid.New(), uuid.New()
	captures := []persistence.IncomingGroupingCapture{
		matchingLayoutRecheckCapture(firstFile, root, "One", 1),
		matchingLayoutRecheckCapture(secondFile, root, "Two", 2),
	}
	providerKey := func(index int) string {
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", index)
	}
	header := func(key string, releaseID uuid.UUID, title string) persistence.MatchingLocalSelectionHeader {
		return persistence.MatchingLocalSelectionHeader{
			ProviderID: uuid.New(), ReleaseID: releaseID, ProviderKey: key, Title: title,
			Credits:       []persistence.MatchingReleaseCredit{{Position: 0, Name: "Artist"}},
			PositionCount: 2,
		}
	}

	firstTrack, secondTrack := uuid.New(), uuid.New()
	rankedReleaseIDs := make([]uuid.UUID, 10)
	layouts := make(map[uuid.UUID]persistence.MatchingRelease, 10)
	headers := make([]persistence.MatchingLocalSelectionHeader, 0, 12)
	for index := 10; index >= 1; index-- {
		rankedReleaseIDs[index-1] = uuid.New()
		headers = append(headers, header(providerKey(index), rankedReleaseIDs[index-1], "Album"))
		first, second := uuid.New(), uuid.New()
		firstTitle, secondTitle := "Banana", "Apricot"
		if index == 1 {
			first, second = firstTrack, secondTrack
			firstTitle, secondTitle = "One", "Two"
		}
		release := matchingLayoutRecheckRelease(providerKey(index), first, second, firstTitle, secondTitle)
		release.ID = rankedReleaseIDs[index-1]
		layouts[release.ID] = release
	}
	// These two headers rank below every populated edition because their album
	// text does not match the frozen captures.
	for index := 11; index <= 12; index++ {
		headers = append(headers, header(providerKey(index), uuid.New(), "Elsewhere"))
	}

	repository := &matchingLocalLayoutStageRepository{
		headers: persistence.MatchingLocalSelectionCatalog{
			Headers: headers,
			Digest:  "local-header-digest",
		},
		layouts: layouts,
		digest:  "local-layout-digest",
	}
	service := NewMatchingLocalCatalogSnapshotService(repository)
	stage, err := service.CalculateLayout(t.Context(), captures, 0.99)
	if err != nil {
		t.Fatal(err)
	}
	if repository.hydrateCalls != 1 {
		t.Fatalf("hydration calls = %d, want 1", repository.hydrateCalls)
	}
	if !slices.Equal(repository.hydrated, rankedReleaseIDs) {
		t.Fatalf("hydrated release IDs = %v, want the ten ranked headers %v", repository.hydrated, rankedReleaseIDs)
	}
	if !slices.Equal(stage.ReleaseIDs, rankedReleaseIDs) {
		t.Fatalf("stage release IDs = %v, want %v", stage.ReleaseIDs, rankedReleaseIDs)
	}
	if stage.HeaderDigest != "local-header-digest" || stage.LayoutDigest != "local-layout-digest" {
		t.Fatalf("stage digests = %q / %q", stage.HeaderDigest, stage.LayoutDigest)
	}
	if stage.Stage.Decision.Status != MatchingLayoutSelectionSelected ||
		stage.Stage.ProviderReleaseID == nil || *stage.Stage.ProviderReleaseID != rankedReleaseIDs[0] {
		t.Fatalf("selected layout = %+v, want complete release %s", stage.Stage.Decision, rankedReleaseIDs[0])
	}
	expectedTracks := map[uuid.UUID]uuid.UUID{firstFile: firstTrack, secondFile: secondTrack}
	if len(stage.Stage.Assignments) != len(expectedTracks) {
		t.Fatalf("selected layout has %d assignments, want %d", len(stage.Stage.Assignments), len(expectedTracks))
	}
	gotTracks := make(map[uuid.UUID]uuid.UUID, len(stage.Stage.Assignments))
	for _, assignment := range stage.Stage.Assignments {
		gotTracks[assignment.FileMemberID] = assignment.ProviderTrackID
	}
	if !maps.Equal(gotTracks, expectedTracks) {
		t.Fatalf("selected provider track IDs = %v, want %v", gotTracks, expectedTracks)
	}
	sealed, err := stage.Stage.DurableDecision()
	if err != nil {
		t.Fatal(err)
	}
	ready := []uuid.UUID{firstFile, secondFile}
	projection := []persistence.MatchingRelease{layouts[rankedReleaseIDs[0]]}
	if err := sealed.Validate(MatchingLayoutPolicyVersion, 0.99, ready, projection); err != nil {
		t.Fatalf("validate exact selected layout: %v", err)
	}
	sealed.Assignments = sealed.Assignments[:1]
	if err := sealed.Validate(MatchingLayoutPolicyVersion, 0.99, ready, projection); err == nil {
		t.Fatal("durable decision accepted a partial layout")
	}

	t.Run("empty catalogue hydrates nothing", func(t *testing.T) {
		empty := &matchingLocalLayoutStageRepository{
			headers: persistence.MatchingLocalSelectionCatalog{Digest: "empty-header-digest"},
			layouts: map[uuid.UUID]persistence.MatchingRelease{},
		}
		stage, err := NewMatchingLocalCatalogSnapshotService(empty).CalculateLayout(t.Context(), captures, 0.99)
		if err != nil {
			t.Fatal(err)
		}
		if empty.hydrateCalls != 0 || len(stage.ReleaseIDs) != 0 ||
			stage.LayoutDigest != "" || stage.Stage.Decision.Status != MatchingLayoutSelectionNone {
			t.Fatalf("empty catalogue = %+v (hydrated %v)", stage, empty.hydrated)
		}
	})
}
