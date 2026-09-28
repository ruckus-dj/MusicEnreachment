package api

import (
	"context"
	"github.com/danielgtaylor/huma/v2"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"net/http"
)

type SetupStateOutput struct{ Body setupStateBody }
type setupStateBody struct {
	Completed           bool     `json:"completed"`
	ConfigurationHealth bool     `json:"configuration_health"`
	Problems            []string `json:"problems"`
}
type SaveSetupInput struct{ Body saveSetupBody }
type saveSetupBody struct {
	ToolsDirectory    string `json:"tools_directory"`
	OutputDirectory   string `json:"output_directory"`
	PublicationFormat string `json:"publication_format"`
}

func RegisterSetup(api huma.API, setup *service.SetupService) {
	huma.Register(api, huma.Operation{OperationID: "get-setup-state", Method: http.MethodGet, Path: "/setup"}, func(ctx context.Context, _ *struct{}) (*SetupStateOutput, error) {
		state, err := setup.State(ctx)
		if err != nil {
			return nil, err
		}
		return &SetupStateOutput{Body: setupStateBody{Completed: state.Completed, ConfigurationHealth: state.ConfigurationHealth.Healthy, Problems: state.ConfigurationHealth.Problems}}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "save-setup-runtime", Method: http.MethodPut, Path: "/setup/runtime"}, func(ctx context.Context, input *SaveSetupInput) (*struct{}, error) {
		if err := setup.SaveRuntime(ctx, input.Body.ToolsDirectory, input.Body.OutputDirectory, input.Body.PublicationFormat); err != nil {
			return nil, huma.Error400BadRequest("invalid runtime settings", err)
		}
		return nil, nil
	})
	huma.Register(api, huma.Operation{OperationID: "complete-setup", Method: http.MethodPost, Path: "/setup/complete"}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		if err := setup.Complete(ctx); err != nil {
			return nil, huma.Error409Conflict("setup requirements are not met", err)
		}
		return nil, nil
	})
}
