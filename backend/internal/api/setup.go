package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type SetupStateOutput struct {
	Body SetupStateBody `json:"body"`
}

type SetupStateBody struct {
	Completed           bool                        `json:"completed"`
	ConfigurationHealth ConfigurationHealthResponse `json:"configuration_health"`
	Platform            PlatformResponse            `json:"platform"`
	Settings            RuntimeSettingsResponse     `json:"settings"`
}

type PlatformResponse struct {
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	Supported  bool   `json:"supported"`
	Diagnostic bool   `json:"diagnostic"`
	Reason     string `json:"reason,omitempty"`
}

type RuntimeSettingsResponse struct {
	ToolsDirectory             string     `json:"tools_directory"`
	OutputDirectory            string     `json:"output_directory"`
	PublicationFormat          string     `json:"publication_format"`
	MusicBrainzMode            string     `json:"musicbrainz_mode"`
	MusicBrainzBaseURL         string     `json:"musicbrainz_base_url"`
	MusicBrainzVerifiedAt      *time.Time `json:"musicbrainz_verified_at,omitempty"`
	LRCLIBEnabled              bool       `json:"lrclib_enabled"`
	LogLevel                   string     `json:"log_level"`
	ActiveFFmpegInstallationID string     `json:"active_ffmpeg_installation_id,omitempty"`
	ActiveFPCalcInstallationID string     `json:"active_fpcalc_installation_id,omitempty"`
	OutputCaseSensitive        *bool      `json:"output_case_sensitive,omitempty"`
	OutputUnicodeNormalization string     `json:"output_unicode_normalization,omitempty"`
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
				Platform: PlatformResponse{
					GOOS: state.Platform.Platform.GOOS, GOARCH: state.Platform.Platform.GOARCH,
					Supported: state.Platform.Platform.Supported(), Diagnostic: state.Platform.Diagnostic,
					Reason: state.Platform.Reason,
				},
				Settings: RuntimeSettingsResponse{
					ToolsDirectory:             state.Runtime.ToolsDirectory,
					OutputDirectory:            state.Runtime.OutputDirectory,
					PublicationFormat:          state.Runtime.PublicationFormat,
					MusicBrainzMode:            state.Runtime.MusicBrainzMode,
					MusicBrainzBaseURL:         state.Runtime.MusicBrainzBaseURL,
					MusicBrainzVerifiedAt:      state.Runtime.MusicBrainzVerifiedAt,
					LRCLIBEnabled:              state.Runtime.LRCLIBEnabled,
					LogLevel:                   state.Runtime.LogLevel,
					ActiveFFmpegInstallationID: state.Runtime.ActiveFFmpegInstallation,
					ActiveFPCalcInstallationID: state.Runtime.ActiveFPCalcInstallation,
					OutputCaseSensitive:        state.Runtime.OutputCaseSensitive,
					OutputUnicodeNormalization: state.Runtime.OutputUnicodeNormalization,
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
