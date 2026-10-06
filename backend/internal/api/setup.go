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
	SHA256Enabled              bool       `json:"sha256_enabled"`
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

type CheckPathsInput struct {
	Body struct {
		ToolsDirectory  string `json:"tools_directory,omitempty" maxLength:"4096"`
		OutputDirectory string `json:"output_directory,omitempty" maxLength:"4096"`
	}
}

type CheckPathsOutput struct {
	Body CheckPathsBody `json:"body"`
}

type CheckPathsBody struct {
	ToolsDirectory             string `json:"tools_directory"`
	OutputDirectory            string `json:"output_directory"`
	OutputCaseSensitive        bool   `json:"output_case_sensitive"`
	OutputUnicodeNormalization string `json:"output_unicode_normalization"`
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
		if setup == nil {
			return nil, huma.Error503ServiceUnavailable("setup service is unavailable")
		}
		state, err := setup.State(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to load setup state")
		}
		return setupStateOutput(state), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "save-setup-runtime",
		Method:      http.MethodPut,
		Path:        "/setup/runtime",
		Summary:     "Save runtime settings",
		Tags:        []string{"Setup"},
	}, func(ctx context.Context, input *SaveRuntimeInput) (*struct{}, error) {
		if err := requireSetupIncomplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		if err := setup.SaveRuntime(ctx, input.Body.ToolsDirectory, input.Body.OutputDirectory, input.Body.PublicationFormat); err != nil {
			return nil, huma.Error400BadRequest("invalid runtime settings")
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "check-setup-paths",
		Method:      http.MethodPost,
		Path:        "/setup/paths/check",
		Summary:     "Validate setup directories without saving them",
		Tags:        []string{"Setup"},
	}, func(ctx context.Context, input *CheckPathsInput) (*CheckPathsOutput, error) {
		if err := requireSetupIncomplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		paths, err := setup.ValidatePaths(ctx, input.Body.ToolsDirectory, input.Body.OutputDirectory)
		if err != nil {
			return nil, huma.Error400BadRequest("setup directories are invalid")
		}
		return &CheckPathsOutput{Body: CheckPathsBody{
			ToolsDirectory: paths.ToolsDirectory, OutputDirectory: paths.OutputDirectory,
			OutputCaseSensitive:        paths.OutputCaseSensitive,
			OutputUnicodeNormalization: paths.OutputUnicodeNormalization,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "check-musicbrainz",
		Method:      http.MethodPost,
		Path:        "/setup/check-musicbrainz",
		Summary:     "Check MusicBrainz connectivity",
		Tags:        []string{"Setup"},
	}, func(ctx context.Context, _ *CheckMusicBrainzInput) (*CheckMusicBrainzOutput, error) {
		if setup == nil {
			return nil, huma.Error503ServiceUnavailable("setup service is unavailable")
		}
		if err := requireSetupIncomplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		if err := setup.CheckMusicBrainz(ctx); err != nil {
			return &CheckMusicBrainzOutput{
				Body: CheckMusicBrainzBody{
					Success: false,
					Error:   "MusicBrainz connectivity check failed. Retry the check.",
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
		if err := requireSetupIncomplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		if err := setup.Complete(ctx); err != nil {
			return nil, huma.Error409Conflict("setup requirements are not met")
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
		if setup == nil {
			return nil, huma.Error503ServiceUnavailable("setup service is unavailable")
		}
		state, err := setup.State(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to compute health")
		}
		return setupStateOutput(state), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "save-setup-musicbrainz", Method: http.MethodPut, Path: "/setup/musicbrainz",
		Summary: "Save setup MusicBrainz configuration", Tags: []string{"Setup"},
	}, func(ctx context.Context, input *UpdateMusicBrainzInput) (*struct{}, error) {
		if err := requireSetupIncomplete(ctx, setup); err != nil {
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
		OperationID: "save-setup-lrclib", Method: http.MethodPut, Path: "/setup/lrclib",
		Summary: "Save setup LRCLIB setting", Tags: []string{"Setup"},
	}, func(ctx context.Context, input *UpdateLRCLIBInput) (*struct{}, error) {
		if err := requireSetupIncomplete(ctx, setup); err != nil {
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
}

func setupStateOutput(state service.SetupState) *SetupStateOutput {
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
				SHA256Enabled:              state.SHA256Enabled,
				LogLevel:                   state.Runtime.LogLevel,
				ActiveFFmpegInstallationID: state.Runtime.ActiveFFmpegInstallation,
				ActiveFPCalcInstallationID: state.Runtime.ActiveFPCalcInstallation,
				OutputCaseSensitive:        state.Runtime.OutputCaseSensitive,
				OutputUnicodeNormalization: state.Runtime.OutputUnicodeNormalization,
			},
		},
	}
}

func requireSetupIncomplete(ctx context.Context, setup *service.SetupService) error {
	if setup == nil {
		return huma.Error503ServiceUnavailable("setup service is unavailable")
	}
	state, err := setup.State(ctx)
	if err != nil {
		return huma.Error500InternalServerError("failed to read setup state")
	}
	if state.Completed {
		return huma.Error404NotFound("setup route is closed after completion")
	}
	return nil
}

func requireSetupComplete(ctx context.Context, setup *service.SetupService) error {
	if setup == nil {
		return huma.Error503ServiceUnavailable("setup service is unavailable")
	}
	state, err := setup.State(ctx)
	if err != nil {
		return huma.Error500InternalServerError("failed to read setup state")
	}
	if !state.Completed {
		return huma.Error409Conflict("setup is not complete")
	}
	return nil
}

func requireSupportedPlatform(ctx context.Context, setup *service.SetupService) error {
	if setup == nil {
		return huma.Error503ServiceUnavailable("setup service is unavailable")
	}
	state, err := setup.State(ctx)
	if err != nil {
		return huma.Error500InternalServerError("failed to read platform state")
	}
	if state.Platform.Diagnostic || !state.Platform.Platform.Supported() {
		return huma.Error503ServiceUnavailable("product operations are unavailable for this instance platform")
	}
	return nil
}
