package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryMetadataBody struct {
	ExpectedRevision int64                      `json:"expected_revision"`
	Changes          map[string]json.RawMessage `json:"changes"`
}
type libraryMetadataResponse struct {
	Envelope           librarymetadata.Envelope `json:"envelope"`
	Revision           int64                    `json:"revision"`
	PublicationApplied bool                     `json:"publication_applied"`
}
type libraryMetadataOutput struct {
	Body libraryMetadataResponse `json:"body"`
}
type libraryMetadataReleaseInput struct {
	ReleaseID string              `path:"release_id"`
	Body      libraryMetadataBody `json:"body"`
}
type libraryMetadataPositionInput struct {
	ReleaseID  string              `path:"release_id"`
	PositionID string              `path:"position_id"`
	Body       libraryMetadataBody `json:"body"`
}
type libraryMetadataTrackInput struct {
	TrackID string              `path:"track_id"`
	Body    libraryMetadataBody `json:"body"`
}
type libraryMetadataRecordingInput struct {
	RecordingID string              `path:"recording_id"`
	Body        libraryMetadataBody `json:"body"`
}
type libraryMetadataReleaseGetInput struct {
	ReleaseID string `path:"release_id"`
}
type libraryMetadataPositionGetInput struct {
	ReleaseID  string `path:"release_id"`
	PositionID string `path:"position_id"`
}
type libraryMetadataTrackGetInput struct {
	TrackID string `path:"track_id"`
}
type libraryMetadataRecordingGetInput struct {
	RecordingID string `path:"recording_id"`
}

func registerLibraryMetadata(api huma.API, metadata *service.LibraryMetadataService) {
	register := func(ctx context.Context, scope librarymetadata.Scope, releaseID, ownerID uuid.UUID, body *libraryMetadataBody, get bool) (*libraryMetadataOutput, error) {
		if metadata == nil {
			return nil, huma.Error503ServiceUnavailable("library metadata is unavailable")
		}
		var result service.LibraryMetadataResult
		var err error
		if get {
			result, err = metadata.Read(ctx, scope, releaseID, ownerID)
		} else {
			result, err = metadata.Update(ctx, scope, releaseID, ownerID, body.ExpectedRevision, body.Changes)
		}
		switch {
		case errors.Is(err, service.ErrLibraryMetadataInvalidRequest):
			return nil, huma.Error400BadRequest("library metadata request is invalid")
		case errors.Is(err, service.ErrLibraryMetadataNotFound):
			return nil, huma.Error404NotFound("library metadata owner not found")
		case errors.Is(err, service.ErrLibraryMetadataConflict):
			return nil, huma.Error409Conflict("library metadata revision is stale")
		case err != nil:
			return nil, huma.Error503ServiceUnavailable("library metadata is unavailable")
		default:
			return &libraryMetadataOutput{Body: libraryMetadataResponse{Envelope: result.Envelope, Revision: result.Revision, PublicationApplied: false}}, nil
		}
	}
	parse := func(value string) (uuid.UUID, error) {
		id, err := uuid.Parse(value)
		if err != nil {
			return uuid.Nil, err
		}
		if id == uuid.Nil {
			return uuid.Nil, fmt.Errorf("empty UUID")
		}
		return id, nil
	}
	for _, spec := range []struct {
		scope     librarymetadata.Scope
		path      string
		operation string
	}{{librarymetadata.ScopeRelease, "/library/releases/{release_id}/metadata", "library-release-metadata"}, {librarymetadata.ScopeRecording, "/library/recordings/{recording_id}/metadata", "library-recording-metadata"}, {librarymetadata.ScopeTrack, "/library/tracks/{track_id}/metadata", "library-track-metadata"}, {librarymetadata.ScopePosition, "/library/releases/{release_id}/positions/{position_id}/metadata", "library-position-metadata"}} {
		s := spec
		switch s.scope {
		case librarymetadata.ScopeRelease:
			huma.Register(api, huma.Operation{OperationID: "get-" + s.operation, Method: http.MethodGet, Path: s.path, Summary: "Read desired local metadata", Tags: []string{"Library"}}, func(ctx context.Context, in *libraryMetadataReleaseGetInput) (*libraryMetadataOutput, error) {
				id, e := parse(in.ReleaseID)
				if e != nil {
					return nil, huma.Error400BadRequest("library release ID is invalid")
				}
				return register(ctx, s.scope, id, id, nil, true)
			})
			huma.Register(api, huma.Operation{OperationID: "patch-" + s.operation, Method: http.MethodPatch, Path: s.path, Summary: "Update desired local metadata", Tags: []string{"Library"}}, func(ctx context.Context, in *libraryMetadataReleaseInput) (*libraryMetadataOutput, error) {
				id, e := parse(in.ReleaseID)
				if e != nil {
					return nil, huma.Error400BadRequest("library release ID is invalid")
				}
				return register(ctx, s.scope, id, id, &in.Body, false)
			})
		case librarymetadata.ScopeRecording:
			huma.Register(api, huma.Operation{OperationID: "get-" + s.operation, Method: http.MethodGet, Path: s.path, Summary: "Read desired local metadata", Tags: []string{"Library"}}, func(ctx context.Context, in *libraryMetadataRecordingGetInput) (*libraryMetadataOutput, error) {
				id, e := parse(in.RecordingID)
				if e != nil {
					return nil, huma.Error400BadRequest("library recording ID is invalid")
				}
				return register(ctx, s.scope, uuid.Nil, id, nil, true)
			})
			huma.Register(api, huma.Operation{OperationID: "patch-" + s.operation, Method: http.MethodPatch, Path: s.path, Summary: "Update desired local metadata", Tags: []string{"Library"}}, func(ctx context.Context, in *libraryMetadataRecordingInput) (*libraryMetadataOutput, error) {
				id, e := parse(in.RecordingID)
				if e != nil {
					return nil, huma.Error400BadRequest("library recording ID is invalid")
				}
				return register(ctx, s.scope, uuid.Nil, id, &in.Body, false)
			})
		case librarymetadata.ScopeTrack:
			huma.Register(api, huma.Operation{OperationID: "get-" + s.operation, Method: http.MethodGet, Path: s.path, Summary: "Read desired local metadata", Tags: []string{"Library"}}, func(ctx context.Context, in *libraryMetadataTrackGetInput) (*libraryMetadataOutput, error) {
				id, e := parse(in.TrackID)
				if e != nil {
					return nil, huma.Error400BadRequest("library track ID is invalid")
				}
				return register(ctx, s.scope, uuid.Nil, id, nil, true)
			})
			huma.Register(api, huma.Operation{OperationID: "patch-" + s.operation, Method: http.MethodPatch, Path: s.path, Summary: "Update desired local metadata", Tags: []string{"Library"}}, func(ctx context.Context, in *libraryMetadataTrackInput) (*libraryMetadataOutput, error) {
				id, e := parse(in.TrackID)
				if e != nil {
					return nil, huma.Error400BadRequest("library track ID is invalid")
				}
				return register(ctx, s.scope, uuid.Nil, id, &in.Body, false)
			})
		case librarymetadata.ScopePosition:
			huma.Register(api, huma.Operation{OperationID: "get-" + s.operation, Method: http.MethodGet, Path: s.path, Summary: "Read desired local metadata", Tags: []string{"Library"}}, func(ctx context.Context, in *libraryMetadataPositionGetInput) (*libraryMetadataOutput, error) {
				r, e := parse(in.ReleaseID)
				if e != nil {
					return nil, huma.Error400BadRequest("library release ID is invalid")
				}
				p, e := parse(in.PositionID)
				if e != nil {
					return nil, huma.Error400BadRequest("library position ID is invalid")
				}
				return register(ctx, s.scope, r, p, nil, true)
			})
			huma.Register(api, huma.Operation{OperationID: "patch-" + s.operation, Method: http.MethodPatch, Path: s.path, Summary: "Update desired local metadata", Tags: []string{"Library"}}, func(ctx context.Context, in *libraryMetadataPositionInput) (*libraryMetadataOutput, error) {
				r, e := parse(in.ReleaseID)
				if e != nil {
					return nil, huma.Error400BadRequest("library release ID is invalid")
				}
				p, e := parse(in.PositionID)
				if e != nil {
					return nil, huma.Error400BadRequest("library position ID is invalid")
				}
				return register(ctx, s.scope, r, p, &in.Body, false)
			})
		}
	}
}
