package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type providerArtworkFrontInput struct {
	ReleaseMBID uuid.UUID `path:"release_mbid"`
}

type providerArtworkImageInput struct {
	ReleaseMBID uuid.UUID `path:"release_mbid"`
	ImageID     string    `path:"image_id" maxLength:"20"`
}

type providerArtworkOutput struct {
	ContentType string `header:"Content-Type"`
	Cache       string `header:"Cache-Control"`
	Body        []byte
}

func registerProviderArtwork(api huma.API, artwork *service.ProviderArtwork) {
	read := func(ctx context.Context, mbid uuid.UUID, imageID string) (*providerArtworkOutput, error) {
		if artwork == nil {
			return nil, huma.Error503ServiceUnavailable("provider artwork service is unavailable")
		}
		if mbid == uuid.Nil {
			return nil, huma.Error400BadRequest("invalid MusicBrainz release ID")
		}
		image, err := artwork.Thumbnail(ctx, mbid.String(), imageID)
		if errors.Is(err, persistence.ErrCoverArtArchiveImageNotFound) {
			return nil, huma.Error404NotFound("provider artwork thumbnail is not cached")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("read provider artwork thumbnail")
		}
		return &providerArtworkOutput{ContentType: image.MIME, Cache: "private, max-age=300", Body: image.Data}, nil
	}
	png := map[string]*huma.Response{
		"200": {Description: "Cached provider artwork thumbnail", Content: map[string]*huma.MediaType{"image/png": {}}},
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-provider-release-front-artwork",
		Method:      http.MethodGet,
		Path:        "/library/provider-artwork/releases/{release_mbid}/front",
		Summary:     "Read the cached front thumbnail for an exact MusicBrainz edition",
		Tags:        []string{"Library"},
		Responses:   png,
	}, func(ctx context.Context, input *providerArtworkFrontInput) (*providerArtworkOutput, error) {
		return read(ctx, input.ReleaseMBID, "")
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-provider-release-image-artwork",
		Method:      http.MethodGet,
		Path:        "/library/provider-artwork/releases/{release_mbid}/images/{image_id}",
		Summary:     "Read a cached Cover Art Archive gallery thumbnail",
		Tags:        []string{"Library"},
		Responses:   png,
	}, func(ctx context.Context, input *providerArtworkImageInput) (*providerArtworkOutput, error) {
		value, err := strconv.ParseUint(input.ImageID, 10, 64)
		if err != nil || value == 0 || strconv.FormatUint(value, 10) != input.ImageID {
			return nil, huma.Error400BadRequest("invalid Cover Art Archive image ID")
		}
		return read(ctx, input.ReleaseMBID, input.ImageID)
	})
}
