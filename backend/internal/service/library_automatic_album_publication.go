package service

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func firstPublicationAutomaticChoice(snapshot *persistence.LibraryFirstPublicationContext, format publication.AudioPublicationFormat, album *persistence.PublicationAutoAlbumRequest, positionID uuid.UUID) (FirstPublicationSourceChoice, error) {
	if album == nil || snapshot == nil {
		return automaticFirstSourceChoice(snapshot, format)
	}
	allowed := album.PositionWorkIDs(positionID)
	if len(allowed) == 0 {
		return FirstPublicationSourceChoice{}, persistence.ErrPublicationAdmissionConflict
	}
	filtered := *snapshot
	filtered.Sources = make([]persistence.FirstPublicationSource, 0, len(snapshot.Sources))
	for _, source := range snapshot.Sources {
		if !slices.Contains(allowed, source.Input.WorkID) {
			continue
		}
		association := slices.IndexFunc(snapshot.SourceAssociations, func(candidate persistence.PublicationSourceAssociation) bool {
			return candidate.ID == source.AssociationID
		})
		if association < 0 || snapshot.SourceAssociations[association].DecisionMethod != "automatic" {
			continue
		}
		// Unattended admission must not replace an already-selected source.
		if slices.ContainsFunc(snapshot.SourceAssociations, func(candidate persistence.PublicationSourceAssociation) bool {
			return candidate.Selected && candidate.ID != source.AssociationID
		}) {
			continue
		}
		filtered.Sources = append(filtered.Sources, source)
	}
	return automaticFirstSourceChoice(&filtered, format)
}

func (service *LibraryPublications) AdmitAutomaticAlbum(ctx context.Context, request persistence.PublicationAutoAlbumRequest) error {
	for _, position := range request.Scope {
		_, err := service.admitFirst(ctx, request.ReleaseID, position.PositionID, "", request.OutputEpoch, nil, &request)
		if err == nil || errors.Is(err, persistence.ErrPublicationAdmissionConflict) || errors.Is(err, persistence.ErrPublicationPositionNotFound) {
			continue
		}
		return err
	}
	return nil
}
