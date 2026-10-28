package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type createLibraryReleaseInput struct {
	Body service.LibraryReleaseCreateRequest `json:"body"`
}

type CreateLibraryReleaseResult struct {
	ReleaseID uuid.UUID                            `json:"release_id"`
	Media     []CreateLibraryReleaseMediaResult    `json:"media"`
	Positions []CreateLibraryReleasePositionResult `json:"positions"`
}

type CreateLibraryReleaseMediaResult struct {
	MediaKey string    `json:"media_key"`
	MediaID  uuid.UUID `json:"media_id"`
}

type CreateLibraryReleasePositionResult struct {
	PositionKey string    `json:"position_key"`
	PositionID  uuid.UUID `json:"position_id"`
	TrackID     uuid.UUID `json:"track_id"`
	RecordingID *string   `json:"recording_id" nullable:"true"`
}

type createLibraryReleaseOutput struct {
	Body CreateLibraryReleaseResult `json:"body"`
}

func registerLibraryReleaseCreation(api huma.API, creates *service.LibraryReleaseCreation) {
	huma.Register(api, huma.Operation{
		OperationID: "create-library-release", Method: http.MethodPost, Path: "/library/releases",
		Summary: "Create a standalone library release", Tags: []string{"Library"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, input *createLibraryReleaseInput) (*createLibraryReleaseOutput, error) {
		if creates == nil {
			return nil, huma.Error503ServiceUnavailable("library release creation is unavailable")
		}
		created, err := creates.Create(ctx, input.Body)
		if errors.Is(err, service.ErrLibraryReleaseCreateInvalidRequest) {
			return nil, huma.Error400BadRequest("library release request is invalid")
		}
		if service.IsLibraryReleaseCreateConflict(err) {
			return nil, huma.Error409Conflict("library release identity already exists")
		}
		if err != nil {
			return nil, huma.Error503ServiceUnavailable("library release creation is unavailable")
		}
		result := CreateLibraryReleaseResult{ReleaseID: created.ReleaseID, Media: make([]CreateLibraryReleaseMediaResult, 0, len(created.Media)), Positions: make([]CreateLibraryReleasePositionResult, 0, len(created.Positions))}
		for _, medium := range created.Media {
			result.Media = append(result.Media, CreateLibraryReleaseMediaResult{MediaKey: medium.MediaKey, MediaID: medium.MediaID})
		}
		for _, position := range created.Positions {
			var recordingID *string
			if position.RecordingID != nil {
				value := position.RecordingID.String()
				recordingID = &value
			}
			result.Positions = append(result.Positions, CreateLibraryReleasePositionResult{PositionKey: position.PositionKey, PositionID: position.PositionID, TrackID: position.TrackID, RecordingID: recordingID})
		}
		return &createLibraryReleaseOutput{Body: result}, nil
	})
}
