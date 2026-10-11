package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type matchingManualTestRepository struct {
	assignments  []persistence.MatchingManualAssignment
	err          error
	previewCalls int
	confirmCalls int
	preview      persistence.MatchingManualPreview
}

func (repository *matchingManualTestRepository) PreviewMatchingManual(_ context.Context, _ uuid.UUID, _ string, _ uuid.UUID, assignments []persistence.MatchingManualAssignment) (persistence.MatchingManualPreview, error) {
	repository.previewCalls++
	repository.assignments = append([]persistence.MatchingManualAssignment(nil), assignments...)
	return repository.preview, repository.err
}

func (repository *matchingManualTestRepository) ConfirmMatchingManual(context.Context, uuid.UUID, string, uuid.UUID, []persistence.MatchingManualAssignment, string, uuid.UUID) (persistence.MatchingManualPreview, error) {
	repository.confirmCalls++
	return repository.preview, repository.err
}

func TestMatchingManualServiceValidatesAndCanonicalizesExplicitAssignments(t *testing.T) {
	groupID, releaseID := uuid.New(), uuid.New()
	memberA, memberB := uuid.New(), uuid.New()
	service := NewMatchingManualService(&matchingManualTestRepository{})
	_, err := service.Preview(context.Background(), groupID, "r1", releaseID, []MatchingManualAssignment{
		{FileMemberID: memberB, ProviderTrackID: uuid.New(), Medium: 1, Position: 2},
		{FileMemberID: memberA, ProviderTrackID: uuid.New(), Medium: 1, Position: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := service.repository.(*matchingManualTestRepository)
	if len(repo.assignments) != 2 || repo.assignments[0].FileMemberID.String() > repo.assignments[1].FileMemberID.String() {
		t.Fatalf("assignments were not canonicalized: %#v", repo.assignments)
	}
	_, err = service.Preview(context.Background(), groupID, "r1", releaseID, []MatchingManualAssignment{
		{FileMemberID: memberA, ProviderTrackID: uuid.New(), Medium: 1, Position: 1},
		{FileMemberID: memberB, ProviderTrackID: uuid.New(), Medium: 1, Position: 1},
	})
	if err != nil {
		t.Fatalf("same coordinate in separate member assignments should reach repository for folder-scoped validation: %v", err)
	}
	_, err = service.Preview(context.Background(), groupID, "r1", releaseID, []MatchingManualAssignment{
		{FileMemberID: memberA, ProviderTrackID: uuid.New(), Medium: 1, Position: 1},
		{FileMemberID: memberA, ProviderTrackID: uuid.New(), Medium: 1, Position: 2},
	})
	if !errors.Is(err, ErrMatchingManualInvalid) {
		t.Fatalf("duplicate member error = %v, want invalid request", err)
	}
	if repo.previewCalls != 2 {
		t.Fatalf("invalid request reached repository: %d calls", repo.previewCalls-2)
	}
}

func TestMatchingManualServiceDoesNotMisclassifyInfrastructureErrors(t *testing.T) {
	repository := &matchingManualTestRepository{err: errors.New("database unavailable")}
	service := NewMatchingManualService(repository)
	_, err := service.Preview(context.Background(), uuid.New(), "r1", uuid.New(), nil)
	if err == nil || errors.Is(err, ErrMatchingManualInvalid) || errors.Is(err, ErrMatchingManualConflict) {
		t.Fatalf("infrastructure error was incorrectly classified: %v", err)
	}
}

func TestMatchingManualServiceClassifiesProtectedManualConfirmationAsConflict(t *testing.T) {
	repository := &matchingManualTestRepository{err: persistence.ErrMatchingManualProtection}
	_, err := NewMatchingManualService(repository).Confirm(context.Background(), uuid.New(), "r1", uuid.New(), nil, "preview-digest", uuid.New())
	if !errors.Is(err, ErrMatchingManualConflict) || errors.Is(err, ErrMatchingManualInvalid) {
		t.Fatalf("protected manual confirmation error = %v, want conflict", err)
	}
	if repository.confirmCalls != 1 {
		t.Fatalf("confirmation calls = %d, want 1", repository.confirmCalls)
	}
}

func TestMatchingManualServiceProjectsCompleteSelectedReleaseLayout(t *testing.T) {
	groupID, releaseID, trackID := uuid.New(), uuid.New(), uuid.New()
	repository := &matchingManualTestRepository{preview: persistence.MatchingManualPreview{GroupID: groupID, EvaluationRevision: "r1", ProviderReleaseID: releaseID,
		SelectedRelease: persistence.MatchingManualReleaseLayout{ProviderReleaseID: releaseID, ReleaseID: "provider-release", Title: "Release", TracklistComplete: true,
			Positions: []persistence.MatchingManualReleaseLayoutPosition{{TrackID: trackID, Medium: 0, Position: 0, DisplayedNumber: "1", RecordingID: "recording", Title: "Track"}}}}}
	result, err := NewMatchingManualService(repository).Preview(context.Background(), groupID, "r1", releaseID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.SelectedRelease.Title != "Release" || len(result.SelectedRelease.Positions) != 1 || result.SelectedRelease.Positions[0].TrackID != trackID {
		t.Fatalf("selected release layout not projected: %#v", result.SelectedRelease)
	}
	if result.SelectedRelease.Positions == nil {
		t.Fatal("selected release positions must be present in the response")
	}

	if result.SelectedRelease.Positions[0].Medium != 0 || result.SelectedRelease.Positions[0].Position != 0 || result.SelectedRelease.Positions[0].DisplayedNumber != "1" {
		t.Fatalf("selected release coordinates changed: %#v", result.SelectedRelease.Positions[0])
	}
}

func TestMatchingManualConfirmReturnsReceiptLayoutWithoutFreshReviewRead(t *testing.T) {
	groupID, releaseID, trackID := uuid.New(), uuid.New(), uuid.New()
	// The repository returns the immutable receipt from an older evaluation. The
	// service must return it directly rather than consulting current review state.
	repository := &matchingManualTestRepository{preview: persistence.MatchingManualPreview{GroupID: groupID,
		EvaluationRevision: "old-revision", ProviderReleaseID: releaseID, PreviewDigest: "receipt-digest",
		SelectedRelease: persistence.MatchingManualReleaseLayout{ProviderReleaseID: releaseID, ReleaseID: "old-release",
			Title: "Receipt release", TracklistComplete: true,
			Positions: []persistence.MatchingManualReleaseLayoutPosition{{TrackID: trackID, Medium: 0, Position: 0, RecordingID: "old-recording", Title: "Old track"}}}}}
	result, err := NewMatchingManualService(repository).Confirm(context.Background(), groupID, "old-revision", releaseID, nil, "receipt-digest", uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if repository.confirmCalls != 1 || result.EvaluationRevision != "old-revision" || result.SelectedRelease.Title != "Receipt release" || result.SelectedRelease.Positions[0].RecordingID != "old-recording" {
		t.Fatalf("confirmation did not return immutable receipt layout: calls=%d result=%#v", repository.confirmCalls, result)
	}
}
