package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type matchingLocalTestRepository struct {
	intent       persistence.MatchingLocalIntent
	preview      persistence.MatchingLocalPreview
	err          error
	confirmCalls int
}

func (r *matchingLocalTestRepository) PreviewMatchingLocal(_ context.Context, _ uuid.UUID, intent persistence.MatchingLocalIntent) (persistence.MatchingLocalPreview, error) {
	r.intent = intent
	return r.preview, r.err
}

func (r *matchingLocalTestRepository) ConfirmMatchingLocal(_ context.Context, _ uuid.UUID, intent persistence.MatchingLocalIntent, _ string, _ uuid.UUID) (persistence.MatchingLocalPreview, error) {
	r.intent = intent
	r.confirmCalls++
	return r.preview, r.err
}

func (*matchingLocalTestRepository) ListLocalReleases(context.Context) (persistence.MatchingLocalReleaseCatalog, error) {
	return persistence.MatchingLocalReleaseCatalog{}, nil
}

func TestMatchingLocalServicePassesProviderAssignmentsInCanonicalOrder(t *testing.T) {
	groupID, releaseID, trackA, trackB := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	memberA := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	memberB := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	repository := &matchingLocalTestRepository{}
	intent := MatchingLocalIntent{ProviderReleaseID: releaseID, ExpectedGroupRevision: "group-r1", ExpectedAuthorityRevision: 2,
		Assignments: []MatchingLocalAssignment{{FileMemberID: memberB, ProviderTrackID: trackB}, {FileMemberID: memberA, ProviderTrackID: trackA}}}
	if _, err := NewMatchingLocalService(repository).Preview(context.Background(), groupID, intent); err != nil {
		t.Fatal(err)
	}
	if repository.intent.ProviderReleaseID != releaseID || len(repository.intent.Assignments) != 2 ||
		repository.intent.Assignments[0].FileMemberID != memberA || repository.intent.Assignments[1].FileMemberID != memberB {
		t.Fatalf("provider assignment intent not canonicalized: %+v", repository.intent)
	}
}

func TestMatchingLocalServiceMapsProviderReleaseGraph(t *testing.T) {
	groupID, releaseID, trackID, recordingID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	repository := &matchingLocalTestRepository{preview: persistence.MatchingLocalPreview{
		GroupID: groupID, ProviderReleaseID: releaseID,
		Tracks:       []persistence.MatchingLocalTrack{{ProviderTrackID: trackID, RecordingID: recordingID, Medium: 1, Position: 2, Title: "Track"}},
		LocalCatalog: &persistence.MatchingLocalRelease{ID: releaseID, Title: "Local", Tracks: []persistence.MatchingLocalTrack{{ProviderTrackID: trackID, RecordingID: recordingID}}},
	}}
	result, err := NewMatchingLocalService(repository).Preview(context.Background(), groupID, MatchingLocalIntent{
		ProviderReleaseID: releaseID, ExpectedGroupRevision: "group-r1", Assignments: []MatchingLocalAssignment{{FileMemberID: uuid.New(), ProviderTrackID: trackID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProviderReleaseID != releaseID || len(result.Tracks) != 1 || result.Tracks[0].ProviderTrackID != trackID ||
		result.LocalCatalog == nil || result.LocalCatalog.ID != releaseID {
		t.Fatalf("provider graph was not mapped to response: %+v", result)
	}
}

func TestMatchingLocalServiceRejectsInvalidAssignmentsBeforeRepository(t *testing.T) {
	member, trackID := uuid.New(), uuid.New()
	for name, intent := range map[string]MatchingLocalIntent{
		"missing provider release": {ExpectedGroupRevision: "group-r1", Assignments: []MatchingLocalAssignment{{FileMemberID: member, ProviderTrackID: trackID}}},
		"missing member":           {ProviderReleaseID: uuid.New(), ExpectedGroupRevision: "group-r1", Assignments: []MatchingLocalAssignment{{ProviderTrackID: trackID}}},
		"missing provider track":   {ProviderReleaseID: uuid.New(), ExpectedGroupRevision: "group-r1", Assignments: []MatchingLocalAssignment{{FileMemberID: member}}},
		"duplicate member":         {ProviderReleaseID: uuid.New(), ExpectedGroupRevision: "group-r1", Assignments: []MatchingLocalAssignment{{FileMemberID: member, ProviderTrackID: trackID}, {FileMemberID: member, ProviderTrackID: uuid.New()}}},
	} {
		t.Run(name, func(t *testing.T) {
			repository := &matchingLocalTestRepository{}
			_, err := NewMatchingLocalService(repository).Preview(context.Background(), uuid.New(), intent)
			if !errors.Is(err, ErrMatchingLocalInvalid) || repository.intent.ProviderReleaseID != uuid.Nil {
				t.Fatalf("error = %v, repository intent = %+v; want pre-repository invalid request", err, repository.intent)
			}
		})
	}
}
