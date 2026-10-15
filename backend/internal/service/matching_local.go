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
type MatchingLocalTrack struct {
	LocalTrackID    *uuid.UUID            `json:"local_track_id,omitempty"`
	SourceMediaIDs  []uuid.UUID           `json:"source_media_ids,omitempty"`
	Medium          int                   `json:"medium"`
	Position        int                   `json:"position"`
	DisplayedNumber string                `json:"displayed_number"`
	Title           string                `json:"title"`
	Artists         []MatchingLocalArtist `json:"artists"`
	FileMemberIDs   []uuid.UUID           `json:"file_member_ids"`
}
type MatchingLocalIntent struct {
	LocalReleaseID            *uuid.UUID            `json:"local_release_id,omitempty"`
	ExpectedGroupRevision     string                `json:"expected_group_revision"`
	ExpectedAuthorityRevision int64                 `json:"expected_authority_revision"`
	ReleaseTitle              string                `json:"release_title"`
	ReleaseArtists            []MatchingLocalArtist `json:"release_artists"`
	Tracks                    []MatchingLocalTrack  `json:"tracks"`
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
	ReleaseTitle         string                `json:"release_title"`
	ReleaseArtists       []MatchingLocalArtist `json:"release_artists"`
	Tracks               []MatchingLocalTrack  `json:"tracks"`
	Sources              []MatchingLocalSource `json:"sources"`
	OmittedFileMemberIDs []uuid.UUID           `json:"omitted_file_member_ids"`
	PreviewDigest        string                `json:"preview_digest"`
	LocalReleaseID       *uuid.UUID            `json:"local_release_id,omitempty"`
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
	intent.ReleaseArtists = append([]MatchingLocalArtist(nil), intent.ReleaseArtists...)
	intent.Tracks = append([]MatchingLocalTrack(nil), intent.Tracks...)
	if intent.LocalReleaseID != nil {
		id := *intent.LocalReleaseID
		intent.LocalReleaseID = &id
	}
	if intent.LocalReleaseID != nil && *intent.LocalReleaseID == uuid.Nil {
		return intent, ErrMatchingLocalInvalid
	}
	for i := range intent.Tracks {
		intent.Tracks[i].Artists = append([]MatchingLocalArtist(nil), intent.Tracks[i].Artists...)
		intent.Tracks[i].FileMemberIDs = append([]uuid.UUID(nil), intent.Tracks[i].FileMemberIDs...)
		intent.Tracks[i].SourceMediaIDs = nil
		if intent.Tracks[i].LocalTrackID != nil {
			if *intent.Tracks[i].LocalTrackID == uuid.Nil {
				return intent, ErrMatchingLocalInvalid
			}
			id := *intent.Tracks[i].LocalTrackID
			intent.Tracks[i].LocalTrackID = &id
		}
	}
	if groupID == uuid.Nil || strings.TrimSpace(intent.ExpectedGroupRevision) == "" || intent.ExpectedAuthorityRevision < 0 || len(intent.Tracks) == 0 {
		return intent, ErrMatchingLocalInvalid
	}
	if intent.LocalReleaseID == nil {
		for _, track := range intent.Tracks {
			if track.LocalTrackID != nil {
				return intent, ErrMatchingLocalInvalid
			}
		}
	}
	coordinates := map[string]bool{}
	localTrackIDs := map[uuid.UUID]bool{}
	for i := range intent.Tracks {
		track := &intent.Tracks[i]
		if track.Medium < 1 || track.Position < 1 || len(track.FileMemberIDs) == 0 {
			return intent, ErrMatchingLocalInvalid
		}
		coordinate := fmt.Sprintf("%d/%d", track.Medium, track.Position)
		if coordinates[coordinate] {
			return intent, ErrMatchingLocalInvalid
		}
		coordinates[coordinate] = true
		seen := map[uuid.UUID]bool{}
		for _, id := range track.FileMemberIDs {
			if id == uuid.Nil || seen[id] {
				return intent, ErrMatchingLocalInvalid
			}
			seen[id] = true
		}
		if track.LocalTrackID != nil {
			if localTrackIDs[*track.LocalTrackID] {
				return intent, ErrMatchingLocalInvalid
			}
			localTrackIDs[*track.LocalTrackID] = true
		}
		sort.Slice(track.FileMemberIDs, func(a, b int) bool { return track.FileMemberIDs[a].String() < track.FileMemberIDs[b].String() })
	}
	return intent, nil
}

func toPersistenceMatchingLocalIntent(intent MatchingLocalIntent) persistence.MatchingLocalIntent {
	var releaseID *uuid.UUID
	if intent.LocalReleaseID != nil {
		id := *intent.LocalReleaseID
		releaseID = &id
	}
	result := persistence.MatchingLocalIntent{LocalReleaseID: releaseID, ExpectedGroupRevision: intent.ExpectedGroupRevision, ExpectedAuthorityRevision: intent.ExpectedAuthorityRevision,
		ReleaseTitle: intent.ReleaseTitle, ReleaseArtists: make([]persistence.MatchingLocalArtist, 0, len(intent.ReleaseArtists)), Tracks: make([]persistence.MatchingLocalTrack, 0, len(intent.Tracks))}
	for _, artist := range intent.ReleaseArtists {
		result.ReleaseArtists = append(result.ReleaseArtists, persistence.MatchingLocalArtist{Name: artist.Name})
	}
	for _, track := range intent.Tracks {
		var localTrackID *uuid.UUID
		if track.LocalTrackID != nil {
			id := *track.LocalTrackID
			localTrackID = &id
		}
		item := persistence.MatchingLocalTrack{LocalTrackID: localTrackID, Medium: track.Medium, Position: track.Position, DisplayedNumber: track.DisplayedNumber, Title: track.Title, FileMemberIDs: append([]uuid.UUID(nil), track.FileMemberIDs...)}
		for _, artist := range track.Artists {
			item.Artists = append(item.Artists, persistence.MatchingLocalArtist{Name: artist.Name})
		}
		result.Tracks = append(result.Tracks, item)
	}
	return result
}

func matchingLocalResult(value persistence.MatchingLocalPreview) MatchingLocalPreview {
	result := MatchingLocalPreview{GroupID: value.GroupID, GroupRevision: value.GroupRevision, AuthorityRevision: value.AuthorityRevision,
		ReleaseTitle: value.ReleaseTitle, PreviewDigest: value.PreviewDigest, LocalReleaseID: value.LocalReleaseID,
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
	item := MatchingLocalTrack{LocalTrackID: track.LocalTrackID, SourceMediaIDs: append([]uuid.UUID(nil), track.SourceMediaIDs...), Medium: track.Medium, Position: track.Position,
		DisplayedNumber: track.DisplayedNumber, Title: track.Title, FileMemberIDs: append([]uuid.UUID(nil), track.FileMemberIDs...)}
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
