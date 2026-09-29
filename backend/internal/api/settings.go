package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type UpdateSettingsInput struct {
	Body UpdateSettingsBody `json:"body"`
}

type UpdateSettingsBody struct {
	OutputDirectory   string `json:"output_directory,omitempty" maxLength:"4096"`
	PublicationFormat string `json:"publication_format,omitempty" enum:"source,mka"`
}

type UpdateMusicBrainzInput struct {
	Body UpdateMusicBrainzBody `json:"body"`
}

type UpdateMusicBrainzBody struct {
	Mode    string `json:"mode" enum:"public,self-hosted"`
	BaseURL string `json:"base_url,omitempty" maxLength:"2048"`
}

type UpdateLRCLIBInput struct {
	Body UpdateLRCLIBBody `json:"body"`
}

type UpdateLRCLIBBody struct {
	Enabled bool `json:"enabled"`
}

type UpdateLogLevelInput struct {
	Body UpdateLogLevelBody `json:"body"`
}

type UpdateLogLevelBody struct {
	Level string `json:"level" enum:"debug,info,warn,error"`
}

type CheckSettingsMusicBrainzOutput struct {
	Body CheckMusicBrainzBody `json:"body"`
}

func registerSettings(api huma.API, setup *service.SetupService) {
	huma.Register(api, huma.Operation{
		OperationID: "get-settings", Method: http.MethodGet, Path: "/settings",
		Summary: "Read runtime settings and health", Tags: []string{"Settings"},
	}, func(ctx context.Context, _ *struct{}) (*SetupStateOutput, error) {
		if setup == nil {
			return nil, huma.Error503ServiceUnavailable("settings service is unavailable")
		}
		state, err := setup.State(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read settings")
		}
		return setupStateOutput(state), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-settings", Method: http.MethodPut, Path: "/settings/runtime",
		Summary: "Update output directory and publication format", Tags: []string{"Settings"},
	}, func(ctx context.Context, input *UpdateSettingsInput) (*struct{}, error) {
		if err := requireSetupComplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		if err := setup.SaveRuntime(ctx, "", input.Body.OutputDirectory, input.Body.PublicationFormat); err != nil {
			return nil, huma.Error400BadRequest("settings could not be saved")
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-musicbrainz-settings", Method: http.MethodPut, Path: "/settings/musicbrainz",
		Summary: "Update MusicBrainz mode and endpoint", Tags: []string{"Settings"},
	}, func(ctx context.Context, input *UpdateMusicBrainzInput) (*struct{}, error) {
		if err := requireSetupComplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		if err := setup.SaveMusicBrainz(ctx, input.Body.Mode, input.Body.BaseURL); err != nil {
			return nil, huma.Error400BadRequest("MusicBrainz settings are invalid")
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "check-settings-musicbrainz", Method: http.MethodPost, Path: "/settings/musicbrainz/check",
		Summary: "Check MusicBrainz connectivity", Tags: []string{"Settings"},
	}, func(ctx context.Context, _ *struct{}) (*CheckSettingsMusicBrainzOutput, error) {
		if err := requireSetupComplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		if err := setup.CheckMusicBrainz(ctx); err != nil {
			return &CheckSettingsMusicBrainzOutput{Body: CheckMusicBrainzBody{Success: false, Error: "MusicBrainz connectivity check failed. Retry the check."}}, nil
		}
		return &CheckSettingsMusicBrainzOutput{Body: CheckMusicBrainzBody{Success: true}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-lrclib-setting", Method: http.MethodPut, Path: "/settings/lrclib",
		Summary: "Enable or disable LRCLIB", Tags: []string{"Settings"},
	}, func(ctx context.Context, input *UpdateLRCLIBInput) (*struct{}, error) {
		if err := requireSetupComplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		if err := setup.SetLRCLIBEnabled(ctx, input.Body.Enabled); err != nil {
			return nil, huma.Error500InternalServerError("LRCLIB setting could not be saved")
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-log-level", Method: http.MethodPut, Path: "/settings/log-level",
		Summary: "Set runtime log level", Tags: []string{"Settings"},
	}, func(ctx context.Context, input *UpdateLogLevelInput) (*struct{}, error) {
		if err := requireSetupComplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		if err := setup.SetLogLevel(ctx, input.Body.Level); err != nil {
			return nil, huma.Error400BadRequest("log level could not be saved")
		}
		return nil, nil
	})
}
