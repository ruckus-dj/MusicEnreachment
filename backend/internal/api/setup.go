package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type SetupStateOutput struct {
	Body SetupStateBody `json:"body"`
}

type SetupStateBody struct {
	Completed           bool                        `json:"completed"`
	ConfigurationHealth ConfigurationHealthResponse `json:"configuration_health"`
}

type ConfigurationHealthResponse struct {
	Healthy  bool     `json:"healthy"`
	Problems []string `json:"problems"`
}

type SaveRuntimeInput struct {
	Body SaveRuntimeBody `json:"body"`
}

type SaveRuntimeBody struct {
	ToolsDirectory    string `json:"tools_directory,omitempty" maxLength:"4096"`
	OutputDirectory   string `json:"output_directory,omitempty" maxLength:"4096"`
	PublicationFormat string `json:"publication_format,omitempty" enum:"source,mka"`
}

type CheckMusicBrainzInput struct{}

type CheckMusicBrainzOutput struct {
	Body CheckMusicBrainzBody `json:"body"`
}

type CheckMusicBrainzBody struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type CompleteSetupInput struct{}

func RegisterSetup(api huma.API, setup *service.SetupService) {
	huma.Register(api, huma.Operation{
		OperationID: "get-setup-state",
		Method:      http.MethodGet,
		Path:        "/setup",
		Summary:     "Get current setup state",
		Tags:        []string{"Setup"},
	}, func(ctx context.Context, _ *struct{}) (*SetupStateOutput, error) {
		state, err := setup.State(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to load setup state", err)
		}
		return &SetupStateOutput{
			Body: SetupStateBody{
				Completed: state.Completed,
				ConfigurationHealth: ConfigurationHealthResponse{
					Healthy:  state.ConfigurationHealth.Healthy,
					Problems: state.ConfigurationHealth.Problems,
				},
			},
		}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "save-setup-runtime",
		Method:      http.MethodPut,
		Path:        "/setup/runtime",
		Summary:     "Save runtime settings",
		Tags:        []string{"Setup"},
	}, func(ctx context.Context, input *SaveRuntimeInput) (*struct{}, error) {
		if err := setup.SaveRuntime(ctx, input.Body.ToolsDirectory, input.Body.OutputDirectory, input.Body.PublicationFormat); err != nil {
			return nil, huma.Error400BadRequest("invalid runtime settings", err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "check-musicbrainz",
		Method:      http.MethodPost,
		Path:        "/setup/check-musicbrainz",
		Summary:     "Check MusicBrainz connectivity",
		Tags:        []string{"Setup"},
	}, func(ctx context.Context, _ *CheckMusicBrainzInput) (*CheckMusicBrainzOutput, error) {
		if err := setup.CheckMusicBrainz(ctx); err != nil {
			return &CheckMusicBrainzOutput{
				Body: CheckMusicBrainzBody{
					Success: false,
					Error:   err.Error(),
				},
			}, nil
		}
		return &CheckMusicBrainzOutput{
			Body: CheckMusicBrainzBody{
				Success: true,
			},
		}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "complete-setup",
		Method:      http.MethodPost,
		Path:        "/setup/complete",
		Summary:     "Complete setup wizard",
		Tags:        []string{"Setup"},
	}, func(ctx context.Context, _ *CompleteSetupInput) (*struct{}, error) {
		if err := setup.Complete(ctx); err != nil {
			return nil, huma.Error409Conflict("setup requirements are not met", err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-configuration-health",
		Method:      http.MethodGet,
		Path:        "/setup/health",
		Summary:     "Get configuration health status",
		Tags:        []string{"Setup"},
	}, func(ctx context.Context, _ *struct{}) (*SetupStateOutput, error) {
		state, err := setup.State(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to compute health", err)
		}
		return &SetupStateOutput{
			Body: SetupStateBody{
				Completed: state.Completed,
				ConfigurationHealth: ConfigurationHealthResponse{
					Healthy:  state.ConfigurationHealth.Healthy,
					Problems: state.ConfigurationHealth.Problems,
				},
			},
		}, nil
	})
}
