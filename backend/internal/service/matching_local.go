package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

var (
	ErrMatchingLocalInvalid  = errors.New("local matching request is invalid")
	ErrMatchingLocalConflict = errors.New("local matching snapshot changed")
)

type MatchingLocalArtist struct {
	Name string `json:"name"`
}
type MatchingLocalAssignment struct {
	FileMemberID    uuid.UUID `json:"file_member_id"`
	ProviderTrackID uuid.UUID `json:"provider_track_id"`
}
type MatchingLocalTrack struct {
	ProviderTrackID     uuid.UUID             `json:"provider_track_id"`
	RecordingID         uuid.UUID             `json:"recording_id"`
	RecordingProviderID uuid.UUID             `json:"recording_provider_id"`
	Medium              int                   `json:"medium"`
	Position            int                   `json:"position"`
	DisplayedNumber     string                `json:"displayed_number"`
	Title               string                `json:"title"`
	TitleOverride       *string               `json:"title_override,omitempty"`
	RecordingDurationMS *int64                `json:"recording_duration_ms,omitempty"`
	DurationOverrideMS  *int64                `json:"duration_override_ms,omitempty"`
	Artists             []MatchingLocalArtist `json:"artists"`
}
type MatchingLocalIntent struct {
	ProviderReleaseID         uuid.UUID                 `json:"provider_release_id"`
	ExpectedGroupRevision     string                    `json:"expected_group_revision"`
	ExpectedAuthorityRevision int64                     `json:"expected_authority_revision"`
	Assignments               []MatchingLocalAssignment `json:"assignments"`
}
type MatchingLocalSource struct {
	FileMemberID         uuid.UUID   `json:"file_member_id"`
	MediaIDs             []uuid.UUID `json:"media_ids"`
	Locations            []string    `json:"locations"`
	CurrentLocalTrackIDs []uuid.UUID `json:"current_local_track_ids"`
}
type MatchingLocalPreview struct {
	GroupID              uuid.UUID             `json:"group_id"`
	GroupRevision        string                `json:"group_revision"`
	AuthorityRevision    int64                 `json:"authority_revision"`
	ProviderReleaseID    uuid.UUID             `json:"provider_release_id"`
	LocalReleaseID       *uuid.UUID            `json:"local_release_id,omitempty"`
	ReleaseTitle         string                `json:"release_title"`
	ReleaseArtists       []MatchingLocalArtist `json:"release_artists"`
	Tracks               []MatchingLocalTrack  `json:"tracks"`
	Sources              []MatchingLocalSource `json:"sources"`
	OmittedFileMemberIDs []uuid.UUID           `json:"omitted_file_member_ids"`
	PreviewDigest        string                `json:"preview_digest"`
	LocalCatalog         *MatchingLocalRelease `json:"local_catalog,omitempty"`
}

type MatchingLocalRelease struct {
	ID      uuid.UUID             `json:"id"`
	Title   string                `json:"title"`
	Artists []MatchingLocalArtist `json:"artists"`
	Tracks  []MatchingLocalTrack  `json:"tracks"`
}

type MatchingLocalCatalog struct {
	Releases []MatchingLocalRelease `json:"releases"`
}

type MatchingLocalRepository interface {
	PreviewMatchingLocal(context.Context, uuid.UUID, persistence.MatchingLocalIntent) (persistence.MatchingLocalPreview, error)
	ConfirmMatchingLocal(context.Context, uuid.UUID, persistence.MatchingLocalIntent, string, uuid.UUID) (persistence.MatchingLocalPreview, error)
	ListLocalReleases(context.Context) (persistence.MatchingLocalReleaseCatalog, error)
}

func (s *MatchingLocalService) List(ctx context.Context) (MatchingLocalCatalog, error) {
	if s == nil || s.repository == nil {
		return MatchingLocalCatalog{}, errors.New("local matching is unavailable")
	}
	catalog, err := s.repository.ListLocalReleases(ctx)
	if err != nil {
		return MatchingLocalCatalog{}, err
	}
	result := MatchingLocalCatalog{Releases: make([]MatchingLocalRelease, 0, len(catalog.Releases))}
	for _, release := range catalog.Releases {
		item := MatchingLocalRelease{ID: release.ID, Title: release.Title, Artists: localArtists(release.Artists), Tracks: make([]MatchingLocalTrack, 0, len(release.Tracks))}
		for _, track := range release.Tracks {
			item.Tracks = append(item.Tracks, localTrack(track))
		}
		result.Releases = append(result.Releases, item)
	}
	return result, nil
}

type MatchingLocalService struct{ repository MatchingLocalRepository }

func NewMatchingLocalService(repository MatchingLocalRepository) *MatchingLocalService {
	return &MatchingLocalService{repository: repository}
}

func (s *MatchingLocalService) Preview(ctx context.Context, groupID uuid.UUID, intent MatchingLocalIntent) (MatchingLocalPreview, error) {
	if s == nil || s.repository == nil {
		return MatchingLocalPreview{}, errors.New("local matching is unavailable")
	}
	canonical, err := validateMatchingLocalIntent(groupID, intent)
	if err != nil {
		return MatchingLocalPreview{}, err
	}
	result, err := s.repository.PreviewMatchingLocal(ctx, groupID, toPersistenceMatchingLocalIntent(canonical))
	return matchingLocalResult(result), matchingLocalError(err)
}

func (s *MatchingLocalService) Confirm(ctx context.Context, groupID uuid.UUID, intent MatchingLocalIntent, digest string, requestID uuid.UUID) (MatchingLocalPreview, error) {
	if s == nil || s.repository == nil {
		return MatchingLocalPreview{}, errors.New("local matching is unavailable")
	}
	canonical, err := validateMatchingLocalIntent(groupID, intent)
	if err != nil {
		return MatchingLocalPreview{}, err
	}
	if strings.TrimSpace(digest) == "" || requestID == uuid.Nil {
		return MatchingLocalPreview{}, fmt.Errorf("%w: confirmation identity is required", ErrMatchingLocalInvalid)
	}
	result, err := s.repository.ConfirmMatchingLocal(ctx, groupID, toPersistenceMatchingLocalIntent(canonical), digest, requestID)
	return matchingLocalResult(result), matchingLocalError(err)
}

func validateMatchingLocalIntent(groupID uuid.UUID, intent MatchingLocalIntent) (MatchingLocalIntent, error) {
	intent.Assignments = append([]MatchingLocalAssignment(nil), intent.Assignments...)
	if intent.ProviderReleaseID == uuid.Nil {
		return intent, ErrMatchingLocalInvalid
	}
	if groupID == uuid.Nil || strings.TrimSpace(intent.ExpectedGroupRevision) == "" || intent.ExpectedAuthorityRevision < 0 || len(intent.Assignments) == 0 {
		return intent, ErrMatchingLocalInvalid
	}
	seenMembers := map[uuid.UUID]bool{}
	for i := range intent.Assignments {
		assignment := &intent.Assignments[i]
		if assignment.FileMemberID == uuid.Nil || assignment.ProviderTrackID == uuid.Nil || seenMembers[assignment.FileMemberID] {
			return intent, ErrMatchingLocalInvalid
		}
		seenMembers[assignment.FileMemberID] = true
	}
	sort.Slice(intent.Assignments, func(i, j int) bool {
		return intent.Assignments[i].FileMemberID.String() < intent.Assignments[j].FileMemberID.String()
	})
	return intent, nil
}

func toPersistenceMatchingLocalIntent(intent MatchingLocalIntent) persistence.MatchingLocalIntent {
	result := persistence.MatchingLocalIntent{ProviderReleaseID: intent.ProviderReleaseID, ExpectedGroupRevision: intent.ExpectedGroupRevision, ExpectedAuthorityRevision: intent.ExpectedAuthorityRevision,
		Assignments: make([]persistence.MatchingLocalAssignment, 0, len(intent.Assignments))}
	for _, assignment := range intent.Assignments {
		result.Assignments = append(result.Assignments, persistence.MatchingLocalAssignment{FileMemberID: assignment.FileMemberID, ProviderTrackID: assignment.ProviderTrackID})
	}
	return result
}

func matchingLocalResult(value persistence.MatchingLocalPreview) MatchingLocalPreview {
	result := MatchingLocalPreview{GroupID: value.GroupID, GroupRevision: value.GroupRevision, AuthorityRevision: value.AuthorityRevision,
		ProviderReleaseID: value.ProviderReleaseID, ReleaseTitle: value.ReleaseTitle, PreviewDigest: value.PreviewDigest, LocalReleaseID: value.LocalReleaseID,
		OmittedFileMemberIDs: append([]uuid.UUID(nil), value.OmittedFileMemberIDs...), Sources: make([]MatchingLocalSource, 0, len(value.Sources)),
		ReleaseArtists: make([]MatchingLocalArtist, 0, len(value.ReleaseArtists)), Tracks: make([]MatchingLocalTrack, 0, len(value.Tracks))}
	for _, artist := range value.ReleaseArtists {
		result.ReleaseArtists = append(result.ReleaseArtists, MatchingLocalArtist{Name: artist.Name})
	}
	for _, track := range value.Tracks {
		item := localTrack(track)
		result.Tracks = append(result.Tracks, item)
	}
	if value.LocalCatalog != nil {
		catalog := MatchingLocalRelease{ID: value.LocalCatalog.ID, Title: value.LocalCatalog.Title,
			Artists: localArtists(value.LocalCatalog.Artists), Tracks: make([]MatchingLocalTrack, 0, len(value.LocalCatalog.Tracks))}
		for _, track := range value.LocalCatalog.Tracks {
			catalog.Tracks = append(catalog.Tracks, localTrack(track))
		}
		result.LocalCatalog = &catalog
	}
	for _, source := range value.Sources {
		result.Sources = append(result.Sources, MatchingLocalSource{FileMemberID: source.FileMemberID, MediaIDs: append([]uuid.UUID(nil), source.MediaIDs...), Locations: append([]string(nil), source.Locations...), CurrentLocalTrackIDs: append([]uuid.UUID(nil), source.CurrentLocalTrackIDs...)})
	}
	return result
}

func localTrack(track persistence.MatchingLocalTrack) MatchingLocalTrack {
	item := MatchingLocalTrack{ProviderTrackID: track.ProviderTrackID, RecordingID: track.RecordingID, RecordingProviderID: track.RecordingProviderID, Medium: track.Medium, Position: track.Position,
		DisplayedNumber: track.DisplayedNumber, Title: track.Title, TitleOverride: track.TitleOverride, RecordingDurationMS: track.RecordingDurationMS, DurationOverrideMS: track.DurationOverrideMS}
	item.Artists = localArtists(track.Artists)
	return item
}

func localArtists(artists []persistence.MatchingLocalArtist) []MatchingLocalArtist {
	result := make([]MatchingLocalArtist, 0, len(artists))
	for _, artist := range artists {
		result = append(result, MatchingLocalArtist{Name: artist.Name})
	}
	return result
}

func matchingLocalError(err error) error {
	if errors.Is(err, persistence.ErrMatchingLocalConflict) || errors.Is(err, persistence.ErrIncomingMatchConflict) {
		return fmt.Errorf("%w: %v", ErrMatchingLocalConflict, err)
	}
	return err
}
