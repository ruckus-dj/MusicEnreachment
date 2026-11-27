package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryReleaseCoverCandidatesInput struct {
	ReleaseID string `path:"release_id"`
}

type libraryReleaseCoverCandidateSelectBody struct {
	ExpectedRevision int64 `json:"expected_revision" minimum:"1"`
}

type libraryReleaseCoverCandidateSelectInput struct {
	ReleaseID string `path:"release_id"`
	ImageID   string `path:"image_id"`
	Body      libraryReleaseCoverCandidateSelectBody
}

type libraryReleaseCoverCandidatesOutput struct {
	Body struct {
		Items []service.ReleaseCoverCandidate `json:"items"`
	}
}

func registerLibraryReleaseCoverCandidates(api huma.API, candidates *service.ReleaseCoverCandidates) {
	huma.Register(api, huma.Operation{
		OperationID: "get-library-release-cover-candidates", Method: http.MethodGet,
		Path: "/library/releases/{release_id}/cover/candidates", Summary: "List alternative release covers", Tags: []string{"Library"},
	}, func(ctx context.Context, input *libraryReleaseCoverCandidatesInput) (*libraryReleaseCoverCandidatesOutput, error) {
		if candidates == nil {
			return nil, huma.Error503ServiceUnavailable("library release cover candidates are unavailable")
		}
		releaseID, err := uuid.Parse(input.ReleaseID)
		if err != nil || releaseID == uuid.Nil {
			return nil, huma.Error400BadRequest("invalid release ID")
		}
		items, err := candidates.List(ctx, releaseID)
		if err != nil {
			if errors.Is(err, service.ErrProviderArtworkPending) {
				return nil, huma.Error503ServiceUnavailable("release artwork is still being downloaded")
			}
			return nil, libraryReleaseCoverError(err)
		}
		if items == nil {
			items = []service.ReleaseCoverCandidate{}
		}
		output := &libraryReleaseCoverCandidatesOutput{}
		output.Body.Items = items
		return output, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "select-library-release-cover-candidate", Method: http.MethodPost,
		Path: "/library/releases/{release_id}/cover/candidates/{image_id}/select", Summary: "Select an alternative release cover", Tags: []string{"Library"},
	}, func(ctx context.Context, input *libraryReleaseCoverCandidateSelectInput) (*libraryReleaseCoverOutput, error) {
		if candidates == nil {
			return nil, huma.Error503ServiceUnavailable("library release cover candidates are unavailable")
		}
		releaseID, err := uuid.Parse(input.ReleaseID)
		if err != nil || releaseID == uuid.Nil {
			return nil, huma.Error400BadRequest("invalid release ID")
		}
		result, err := candidates.Select(ctx, releaseID, input.ImageID, input.Body.ExpectedRevision)
		if err != nil {
			return nil, libraryReleaseCoverError(err)
		}
		return &libraryReleaseCoverOutput{Body: result}, nil
	})
}
