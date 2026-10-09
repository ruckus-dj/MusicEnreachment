package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type SourcesOutput struct {
	Body SourcesBody `json:"body"`
}

type SourcesBody struct {
	Sources []SourceRootResponse `json:"sources"`
}

// SourceRootResponse is the operator-facing state of one registered source root.
// The availability, the stale flag and the location count are derived by the
// service, so the client never compares paths or counts rows itself.
type SourceRootResponse struct {
	ID                   uuid.UUID  `json:"id"`
	DisplayName          string     `json:"display_name"`
	ConfiguredPath       string     `json:"configured_path"`
	ProcessingMode       string     `json:"processing_mode" enum:"in_place,staged"`
	Enabled              bool       `json:"enabled"`
	Status               string     `json:"status" enum:"unknown,available,unavailable"`
	SafeError            *string    `json:"safe_error,omitempty"`
	InventoryPath        *string    `json:"inventory_path,omitempty"`
	Stale                bool       `json:"stale"`
	ScanGeneration       int64      `json:"scan_generation"`
	LastSuccessfulScanAt *time.Time `json:"last_successful_scan_at,omitempty"`
	LocationCount        int64      `json:"location_count"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type SourceRootInput struct {
	ID uuid.UUID `path:"source_id"`
}

type SourceRootOutput struct {
	Body SourceRootResponse `json:"body"`
}

type CreateSourceInput struct {
	Body CreateSourceBody `json:"body"`
}

type CreateSourceBody struct {
	DisplayName    string `json:"display_name" minLength:"1" maxLength:"256"`
	ConfiguredPath string `json:"configured_path" minLength:"1" maxLength:"4096"`
	ProcessingMode string `json:"processing_mode" enum:"in_place,staged"`
}

type UpdateSourceInput struct {
	ID   uuid.UUID        `path:"source_id"`
	Body UpdateSourceBody `json:"body"`
}

// UpdateSourceBody carries the fields an edit changes. The pointers keep an
// absent field apart from an empty value.
type UpdateSourceBody struct {
	DisplayName    *string `json:"display_name,omitempty" minLength:"1" maxLength:"256"`
	ConfiguredPath *string `json:"configured_path,omitempty" minLength:"1" maxLength:"4096"`
	ProcessingMode *string `json:"processing_mode,omitempty" enum:"in_place,staged"`
}

type DeleteSourceInput struct {
	ID   uuid.UUID        `path:"source_id"`
	Body DeleteSourceBody `json:"body"`
}

// DeleteSourceBody is the explicit confirmation of a deletion: the exact
// configured path and the number of locations the operator was shown. The
// service compares both against the root as it is now.
type DeleteSourceBody struct {
	ConfirmedPath          string `json:"confirmed_path" minLength:"1" maxLength:"4096"`
	ConfirmedLocationCount int64  `json:"confirmed_location_count" minimum:"0"`
}

// SourceLocationsInput carries the page a caller asks for. The default of limit
// mirrors service.SourceLocationsDefaultPageSize, which is what a direct service
// caller without a limit falls back to; Huma refuses a limit outside the bounds
// before a handler runs.
type SourceLocationsInput struct {
	ID     uuid.UUID `path:"source_id"`
	Cursor string    `query:"cursor" maxLength:"512"`
	Limit  int       `query:"limit" default:"50" minimum:"1" maximum:"200"`
}

type SourceLocationsOutput struct {
	Body SourceLocationsBody `json:"body"`
}

type SourceLocationsBody struct {
	Locations  []SourceLocationResponse `json:"locations"`
	NextCursor *string                  `json:"next_cursor,omitempty"`
}

// SourceLocationResponse is one file of the last successful inventory of a root.
// It describes that inventory alone: the candidates of a scan that has not been
// applied are never served here.
type SourceLocationResponse struct {
	ID           uuid.UUID `json:"id"`
	RelativePath string    `json:"relative_path"`
	SizeBytes    int64     `json:"size_bytes"`
	Mtime        time.Time `json:"mtime"`
	ProbeStatus  string    `json:"probe_status" enum:"audio,no_audio,probe_error"`
	SafeError    *string   `json:"safe_error,omitempty"`
	// MediaVariantID is the stored technical result of this file, or absent when
	// it was never analyzed. The list never carries the result itself: an
	// inspector reads it from the location detail endpoint.
	MediaVariantID *uuid.UUID `json:"media_variant_id,omitempty"`
	// HasResult is the availability of a stored result, exposed separately so a
	// client does not have to treat the variant identifier as a boolean.
	HasResult bool `json:"has_result"`
}

// registerSources binds the source root inventory endpoints. Every handler
// refuses a missing dependency instead of panicking, because the OpenAPI export
// registers the routes with empty Dependencies.
func registerSources(api huma.API, dependencies Dependencies) {
	huma.Register(api, huma.Operation{
		OperationID: "list-sources", Method: http.MethodGet, Path: "/sources",
		Summary: "List registered source roots", Tags: []string{"Sources"},
	}, func(ctx context.Context, _ *struct{}) (*SourcesOutput, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.SourceRoots == nil {
			return nil, huma.Error503ServiceUnavailable("source service is unavailable")
		}
		roots, err := dependencies.SourceRoots.List(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to list source roots")
		}
		response := make([]SourceRootResponse, 0, len(roots))
		for _, root := range roots {
			response = append(response, sourceRootResponse(root))
		}
		return &SourcesOutput{Body: SourcesBody{Sources: response}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-source", Method: http.MethodPost, Path: "/sources",
		Summary: "Register a source root", Tags: []string{"Sources"},
	}, func(ctx context.Context, input *CreateSourceInput) (*SourceRootOutput, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.SourceRoots == nil {
			return nil, huma.Error503ServiceUnavailable("source service is unavailable")
		}
		root, err := dependencies.SourceRoots.Create(ctx, input.Body.DisplayName, input.Body.ConfiguredPath, input.Body.ProcessingMode)
		if err != nil {
			return nil, huma.Error400BadRequest("source root could not be registered")
		}
		return &SourceRootOutput{Body: sourceRootResponse(root)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-source", Method: http.MethodGet, Path: "/sources/{source_id}",
		Summary: "Read a source root", Tags: []string{"Sources"},
	}, func(ctx context.Context, input *SourceRootInput) (*SourceRootOutput, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.SourceRoots == nil {
			return nil, huma.Error503ServiceUnavailable("source service is unavailable")
		}
		root, err := dependencies.SourceRoots.Get(ctx, input.ID)
		if err != nil {
			if service.IsSourceRootNotFound(err) {
				return nil, huma.Error404NotFound("source root not found")
			}
			return nil, huma.Error500InternalServerError("failed to read source root")
		}
		return &SourceRootOutput{Body: sourceRootResponse(root)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-source", Method: http.MethodPatch, Path: "/sources/{source_id}",
		Summary: "Edit a source root", Tags: []string{"Sources"},
	}, func(ctx context.Context, input *UpdateSourceInput) (*SourceRootOutput, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.SourceRoots == nil {
			return nil, huma.Error503ServiceUnavailable("source service is unavailable")
		}
		edit := service.SourceRootEdit{
			DisplayName: input.Body.DisplayName, ConfiguredPath: input.Body.ConfiguredPath,
			ProcessingMode: input.Body.ProcessingMode,
		}
		root, err := dependencies.SourceRoots.Edit(ctx, input.ID, edit)
		if err != nil {
			switch {
			case service.IsSourceRootNotFound(err):
				return nil, huma.Error404NotFound("source root not found")
			case errors.Is(err, service.ErrSourceRootBusy):
				return nil, huma.Error409Conflict("source root has an active scan")
			default:
				return nil, huma.Error400BadRequest("source root could not be edited")
			}
		}
		return &SourceRootOutput{Body: sourceRootResponse(root)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-source", Method: http.MethodDelete, Path: "/sources/{source_id}",
		Summary: "Delete a source root and its inventory", Tags: []string{"Sources"},
	}, func(ctx context.Context, input *DeleteSourceInput) (*struct{}, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.SourceRoots == nil {
			return nil, huma.Error503ServiceUnavailable("source service is unavailable")
		}
		err := dependencies.SourceRoots.Delete(ctx, input.ID, input.Body.ConfirmedPath, input.Body.ConfirmedLocationCount)
		switch {
		case err == nil:
			return nil, nil
		case service.IsSourceRootNotFound(err):
			return nil, huma.Error404NotFound("source root not found")
		case errors.Is(err, service.ErrSourceRootBusy):
			return nil, huma.Error409Conflict("source root has an active scan")
		case errors.Is(err, service.ErrSourceRootConfirmation):
			return nil, huma.Error400BadRequest("deletion confirmation does not match the source root")
		default:
			return nil, huma.Error500InternalServerError("source root could not be deleted")
		}
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-source-locations", Method: http.MethodGet, Path: "/sources/{source_id}/locations",
		Summary: "List the last successful inventory of a source root", Tags: []string{"Sources"},
	}, func(ctx context.Context, input *SourceLocationsInput) (*SourceLocationsOutput, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.SourceLocations == nil {
			return nil, huma.Error503ServiceUnavailable("source inventory service is unavailable")
		}
		page, err := dependencies.SourceLocations.List(ctx, input.ID, input.Cursor, input.Limit)
		if err != nil {
			switch {
			case service.IsSourceRootNotFound(err):
				return nil, huma.Error404NotFound("source root not found")
			case errors.Is(err, service.ErrSourceLocationCursor):
				return nil, huma.Error400BadRequest("pagination cursor is invalid")
			default:
				return nil, huma.Error500InternalServerError("failed to list source locations")
			}
		}
		body := SourceLocationsBody{Locations: make([]SourceLocationResponse, 0, len(page.Locations))}
		for _, location := range page.Locations {
			body.Locations = append(body.Locations, SourceLocationResponse{
				ID: location.ID, RelativePath: location.RelativePath, SizeBytes: location.SizeBytes,
				Mtime: location.Mtime, ProbeStatus: location.ProbeStatus, SafeError: location.SafeError,
				MediaVariantID: location.MediaVariantID, HasResult: location.MediaVariantID != nil,
			})
		}
		if page.NextCursor != "" {
			cursor := page.NextCursor
			body.NextCursor = &cursor
		}
		return &SourceLocationsOutput{Body: body}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "start-source-scan", Method: http.MethodPost, Path: "/sources/{source_id}/scan",
		Summary: "Start a scan of a source root", Tags: []string{"Sources"},
	}, func(ctx context.Context, input *SourceRootInput) (*OperationOutput, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.SourceScan == nil || dependencies.Operations == nil {
			return nil, huma.Error503ServiceUnavailable("source scanning is unavailable")
		}
		operation, err := dependencies.SourceScan.Start(ctx, input.ID)
		if err != nil {
			switch {
			case service.IsSourceRootNotFound(err):
				return nil, huma.Error404NotFound("source root not found")
			case errors.Is(err, service.ErrSourceScanDisabled):
				return nil, huma.Error409Conflict("source root is disabled")
			case errors.Is(err, service.ErrSourceRootBusy):
				return nil, huma.Error409Conflict("source root has an active scan")
			case errors.Is(err, service.ErrSourceScanNotReady):
				return nil, huma.Error503ServiceUnavailable("source scanning requires a completed setup on a supported instance platform")
			default:
				return nil, huma.Error500InternalServerError("scan could not be started")
			}
		}
		snapshot, err := dependencies.Operations.Snapshot(ctx, operation.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read the started scan")
		}
		return operationOutput(snapshot), nil
	})
}

func sourceRootResponse(root service.SourceRoot) SourceRootResponse {
	return SourceRootResponse{
		ID: root.ID, DisplayName: root.DisplayName, ConfiguredPath: root.ConfiguredPath,
		ProcessingMode: root.ProcessingMode,
		Enabled:        root.Enabled, Status: root.Status, SafeError: root.SafeError,
		InventoryPath: root.InventoryPath, Stale: root.Stale, ScanGeneration: root.ScanGeneration,
		LastSuccessfulScanAt: root.LastSuccessfulScanAt, LocationCount: root.LocationCount,
		CreatedAt: root.CreatedAt, UpdatedAt: root.UpdatedAt,
	}
}
