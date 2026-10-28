package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryReleaseOptionsInput struct {
	Query  string `query:"q"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" required:"true"`
	Offset int    `query:"offset" minimum:"0" required:"true"`
}
type libraryCoverCandidateImageInput struct {
	MetadataResultID string `path:"metadata_result_id"`
	ExpectedSHA256   string `query:"expected_sha256" required:"true" pattern:"^[0-9a-f]{64}$"`
}
type libraryReleaseOptionsRecordingsOutput struct {
	Body service.LibraryReleaseOptionsPage[service.LibraryRecordingOption] `json:"body"`
}
type libraryReleaseOptionsVariantsOutput struct {
	Body service.LibraryReleaseOptionsPage[service.LibrarySourceVariantOption] `json:"body"`
}
type libraryReleaseOptionsCoversOutput struct {
	Body service.LibraryReleaseOptionsPage[service.LibraryCoverCandidateOption] `json:"body"`
}
type libraryCoverCandidateImageOutput struct {
	ContentType string `header:"Content-Type"`
	Body        []byte
}

// RegisterLibraryReleaseOptions adds read-only manual-create picker endpoints.
func RegisterLibraryReleaseOptions(api huma.API, options *service.LibraryReleaseOptions) {
	huma.Register(api, huma.Operation{OperationID: "list-library-recordings", Method: http.MethodGet, Path: "/library/recordings", Summary: "Search saved local recordings", Tags: []string{"Library"}}, func(ctx context.Context, input *libraryReleaseOptionsInput) (*libraryReleaseOptionsRecordingsOutput, error) {
		if options == nil {
			return nil, huma.Error503ServiceUnavailable("library release options service is unavailable")
		}
		page, err := options.Recordings(ctx, service.LibraryReleaseOptionsFilter{Query: input.Query, Limit: input.Limit, Offset: input.Offset})
		if err != nil {
			return nil, libraryReleaseOptionsError(err)
		}
		return &libraryReleaseOptionsRecordingsOutput{Body: page}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "list-library-source-variants", Method: http.MethodGet, Path: "/library/source-variants", Summary: "Search analyzed source variants", Tags: []string{"Library"}}, func(ctx context.Context, input *libraryReleaseOptionsInput) (*libraryReleaseOptionsVariantsOutput, error) {
		if options == nil {
			return nil, huma.Error503ServiceUnavailable("library release options service is unavailable")
		}
		page, err := options.SourceVariants(ctx, service.LibraryReleaseOptionsFilter{Query: input.Query, Limit: input.Limit, Offset: input.Offset})
		if err != nil {
			return nil, libraryReleaseOptionsError(err)
		}
		return &libraryReleaseOptionsVariantsOutput{Body: page}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "list-library-cover-candidates", Method: http.MethodGet, Path: "/library/cover-candidates", Summary: "Search saved metadata cover candidates", Tags: []string{"Library"}}, func(ctx context.Context, input *libraryReleaseOptionsInput) (*libraryReleaseOptionsCoversOutput, error) {
		if options == nil {
			return nil, huma.Error503ServiceUnavailable("library release options service is unavailable")
		}
		page, err := options.CoverCandidates(ctx, service.LibraryReleaseOptionsFilter{Query: input.Query, Limit: input.Limit, Offset: input.Offset})
		if err != nil {
			return nil, libraryReleaseOptionsError(err)
		}
		return &libraryReleaseOptionsCoversOutput{Body: page}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "get-library-cover-candidate-image", Method: http.MethodGet, Path: "/library/cover-candidates/{metadata_result_id}/image", Summary: "Read an exact saved metadata cover candidate", Tags: []string{"Library"}, Responses: map[string]*huma.Response{"200": {Description: "Saved cover image", Content: map[string]*huma.MediaType{"image/*": {}}}}}, func(ctx context.Context, input *libraryCoverCandidateImageInput) (*libraryCoverCandidateImageOutput, error) {
		if options == nil {
			return nil, huma.Error503ServiceUnavailable("library release options service is unavailable")
		}
		id, err := uuid.Parse(input.MetadataResultID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid metadata result ID")
		}
		image, err := options.CoverCandidateImage(ctx, id, input.ExpectedSHA256)
		if err != nil {
			return nil, libraryReleaseOptionsError(err)
		}
		return &libraryCoverCandidateImageOutput{ContentType: image.MIME, Body: image.Data}, nil
	})
}

func libraryReleaseOptionsError(err error) error {
	switch {
	case service.IsLibraryReleaseOptionsInvalid(err):
		return huma.Error400BadRequest("invalid library picker request")
	case service.IsLibraryCoverCandidateNotFound(err):
		return huma.Error404NotFound("saved cover candidate not found")
	case service.IsLibraryCoverCandidateStale(err):
		return huma.Error409Conflict("saved cover candidate changed")
	}
	return huma.Error500InternalServerError("failed to read library picker options")
}
