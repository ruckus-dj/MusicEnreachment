package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type UpdateSettingsInput struct {
	Body UpdateSettingsBody `json:"body"`
}

type UpdateSettingsBody struct {
	OutputDirectory          string                      `json:"output_directory,omitempty" maxLength:"4096"`
	WorkDirectory            *string                     `json:"work_directory,omitempty" maxLength:"4096"`
	PublicationFormat        string                      `json:"publication_format,omitempty" enum:"source,mka,m4a"`
	PublicationPathTemplate  *string                     `json:"publication_path_template,omitempty" maxLength:"4096"`
	PublicationRemoveSources *bool                       `json:"publication_remove_sources,omitempty"`
	SourceFileConcurrency    *int                        `json:"source_file_concurrency,omitempty" minimum:"1"`
	Expected                 *UpdateSettingsExpectedBody `json:"expected,omitempty"`
}

type UpdateSettingsExpectedBody struct {
	WorkDirectory            *string `json:"work_directory,omitempty" maxLength:"4096"`
	OutputDirectory          *string `json:"output_directory,omitempty" maxLength:"4096"`
	PublicationFormat        *string `json:"publication_format,omitempty"`
	PublicationPathTemplate  *string `json:"publication_path_template,omitempty" maxLength:"4096"`
	PublicationRemoveSources *bool   `json:"publication_remove_sources,omitempty"`
	SourceFileConcurrency    *int    `json:"source_file_concurrency,omitempty" minimum:"1"`
}

type UpdateMusicBrainzInput struct {
	Body UpdateMusicBrainzBody `json:"body"`
}

type UpdateMusicBrainzBody struct {
	Mode                   *string                        `json:"mode,omitempty" enum:"public,self-hosted"`
	BaseURL                *string                        `json:"base_url,omitempty" maxLength:"2048"`
	SelfHostedThrottle     *bool                          `json:"self_hosted_throttle,omitempty"`
	SelfHostedDelaySeconds *float64                       `json:"self_hosted_delay_seconds,omitempty" minimum:"0" maximum:"60"`
	Expected               *UpdateMusicBrainzExpectedBody `json:"expected"`
}

type UpdateMusicBrainzExpectedBody struct {
	Mode                   *string  `json:"mode,omitempty" enum:"public,self-hosted"`
	BaseURL                *string  `json:"base_url,omitempty" maxLength:"2048"`
	SelfHostedThrottle     *bool    `json:"self_hosted_throttle,omitempty"`
	SelfHostedDelaySeconds *float64 `json:"self_hosted_delay_seconds,omitempty" minimum:"0" maximum:"60"`
}

type UpdateLRCLIBInput struct {
	Body UpdateLRCLIBBody `json:"body"`
}

type UpdateLRCLIBBody struct {
	Enabled bool `json:"enabled"`
}

type UpdateSHA256Input struct {
	Body UpdateSHA256Body `json:"body"`
}

type UpdateSHA256Body struct {
	Enabled bool `json:"enabled"`
}

type UpdateLogLevelInput struct {
	Body UpdateLogLevelBody `json:"body"`
}

type UpdateLogLevelBody struct {
	Level string `json:"level" enum:"debug,info,warn,error"`
}

type UpdateAcoustIDApplicationKeyInput struct {
	Body UpdateAcoustIDApplicationKeyBody `json:"body"`
}

type UpdateAcoustIDApplicationKeyBody struct {
	Key string `json:"key"`
}

type SetGitHubTokenInput struct {
	Body SetGitHubTokenBody `json:"body"`
}
type SetGitHubTokenBody struct {
	Token string `json:"token" maxLength:"512"`
}

type CheckSettingsMusicBrainzOutput struct {
	Body CheckMusicBrainzBody `json:"body"`
}

func registerSettings(api huma.API, setup *service.SetupService) {
	huma.Register(api, huma.Operation{OperationID: "set-github-token", Method: http.MethodPut, Path: "/settings/github/token", Summary: "Set GitHub token", Tags: []string{"Settings"}}, func(ctx context.Context, input *SetGitHubTokenInput) (*struct{}, error) {
		if setup == nil {
			return nil, huma.Error503ServiceUnavailable("settings service is unavailable")
		}
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		if err := setup.SetGitHubToken(ctx, input.Body.Token); err != nil {
			switch {
			case errors.Is(err, service.ErrGitHubTokenMalformed):
				return nil, huma.Error400BadRequest("GitHub token has an invalid format")
			case errors.Is(err, service.ErrGitHubTokenRejected):
				return nil, huma.Error401Unauthorized("GitHub rejected the token; check that it is valid")
			case errors.Is(err, service.ErrGitHubTokenValidationUnavailable):
				return nil, huma.Error502BadGateway("GitHub token validation is temporarily unavailable")
			case errors.Is(err, service.ErrGitHubTokenRateLimited):
				return nil, huma.Error502BadGateway(err.Error())
			default:
				return nil, huma.Error500InternalServerError("failed to save GitHub token")
			}
		}
		return nil, nil
	})
	huma.Register(api, huma.Operation{OperationID: "delete-github-token", Method: http.MethodDelete, Path: "/settings/github/token", Summary: "Delete GitHub token", Tags: []string{"Settings"}}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		if setup == nil {
			return nil, huma.Error503ServiceUnavailable("settings service is unavailable")
		}
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		if err := setup.DeleteGitHubToken(ctx); err != nil {
			return nil, huma.Error500InternalServerError("failed to delete GitHub token")
		}
		return nil, nil
	})
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
		Summary: "Update runtime settings", Tags: []string{"Settings"},
	}, func(ctx context.Context, input *UpdateSettingsInput) (*struct{}, error) {
		if err := requireSetupComplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		var outputDirectory, publicationFormat *string
		if input.Body.OutputDirectory != "" {
			outputDirectory = &input.Body.OutputDirectory
		}
		if input.Body.PublicationFormat != "" {
			publicationFormat = &input.Body.PublicationFormat
		}
		expected := input.Body.Expected
		hasChanges := input.Body.WorkDirectory != nil || input.Body.OutputDirectory != "" || publicationFormat != nil ||
			input.Body.PublicationPathTemplate != nil || input.Body.PublicationRemoveSources != nil || input.Body.SourceFileConcurrency != nil
		if hasChanges && (expected == nil ||
			(input.Body.WorkDirectory != nil && expected.WorkDirectory == nil) ||
			(input.Body.OutputDirectory != "" && expected.OutputDirectory == nil) ||
			(publicationFormat != nil && expected.PublicationFormat == nil) ||
			(input.Body.PublicationPathTemplate != nil && expected.PublicationPathTemplate == nil) ||
			(input.Body.PublicationRemoveSources != nil && expected.PublicationRemoveSources == nil) ||
			(input.Body.SourceFileConcurrency != nil && expected.SourceFileConcurrency == nil)) {
			return nil, huma.Error422UnprocessableEntity("expected saved settings are required for every changed field")
		}
		var expectedValues *settings.RuntimeSettingsExpected
		if expected != nil {
			expectedValues = &settings.RuntimeSettingsExpected{
				WorkDirectory: expected.WorkDirectory, OutputDirectory: expected.OutputDirectory,
				PublicationFormat: expected.PublicationFormat, PublicationPathTemplate: expected.PublicationPathTemplate,
				PublicationRemoveSources: expected.PublicationRemoveSources, SourceFileConcurrency: expected.SourceFileConcurrency,
			}
		}
		if err := setup.SaveRuntimeUpdate(ctx, service.RuntimeUpdateRequest{
			OutputDirectory: outputDirectory, WorkDirectory: input.Body.WorkDirectory, PublicationFormat: publicationFormat,
			PublicationPathTemplate:  input.Body.PublicationPathTemplate,
			PublicationRemoveSources: input.Body.PublicationRemoveSources,
			SourceFileConcurrency:    input.Body.SourceFileConcurrency, Expected: expectedValues,
		}); err != nil {
			if errors.Is(err, service.ErrSettingsConflict) {
				return nil, huma.Error409Conflict("settings changed since this draft was opened")
			}
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
		targetChanged := input.Body.Mode != nil || input.Body.BaseURL != nil
		if (input.Body.Mode == nil) != (input.Body.BaseURL == nil) ||
			(input.Body.Expected != nil && (input.Body.Expected.Mode == nil) != (input.Body.Expected.BaseURL == nil)) {
			return nil, huma.Error422UnprocessableEntity("MusicBrainz mode and base URL must be supplied together")
		}
		if input.Body.Expected == nil && (targetChanged || input.Body.SelfHostedThrottle != nil || input.Body.SelfHostedDelaySeconds != nil) {
			return nil, huma.Error422UnprocessableEntity("expected MusicBrainz settings are required")
		}
		if targetChanged && (input.Body.Expected.Mode == nil || input.Body.Expected.BaseURL == nil) {
			return nil, huma.Error422UnprocessableEntity("expected MusicBrainz target is required when changing the target")
		}
		if !targetChanged && input.Body.Expected != nil && (input.Body.Expected.Mode != nil || input.Body.Expected.BaseURL != nil) {
			return nil, huma.Error422UnprocessableEntity("MusicBrainz target expectation requires a target update")
		}
		if (input.Body.SelfHostedThrottle != nil && input.Body.Expected.SelfHostedThrottle == nil) ||
			(input.Body.SelfHostedDelaySeconds != nil && input.Body.Expected.SelfHostedDelaySeconds == nil) {
			return nil, huma.Error422UnprocessableEntity("expected saved provider settings are required for every changed field")
		}
		var expected settings.MusicBrainzSettingsExpected
		if input.Body.Expected != nil {
			expected.Mode, expected.BaseURL = input.Body.Expected.Mode, input.Body.Expected.BaseURL
			expected.SelfHostedThrottle = input.Body.Expected.SelfHostedThrottle
			expected.SelfHostedDelaySeconds = input.Body.Expected.SelfHostedDelaySeconds
		}
		if err := setup.SaveMusicBrainzSettings(ctx, input.Body.Mode, input.Body.BaseURL, expected, input.Body.SelfHostedThrottle, input.Body.SelfHostedDelaySeconds); err != nil {
			if errors.Is(err, service.ErrSettingsConflict) {
				return nil, huma.Error409Conflict("MusicBrainz settings changed since this draft was opened")
			}
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
		OperationID: "update-sha256-setting", Method: http.MethodPut, Path: "/settings/sha256",
		Summary: "Enable or disable SHA-256 sampling", Tags: []string{"Settings"},
	}, func(ctx context.Context, input *UpdateSHA256Input) (*struct{}, error) {
		if err := requireSetupComplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		if err := setup.SetSHA256Enabled(ctx, input.Body.Enabled); err != nil {
			return nil, huma.Error500InternalServerError("SHA-256 setting could not be saved")
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

	huma.Register(api, huma.Operation{
		OperationID: "set-acoustid-application-key", Method: http.MethodPut, Path: "/settings/acoustid/application-key",
		Summary: "Set the AcoustID application key", Tags: []string{"Settings"},
	}, func(ctx context.Context, input *UpdateAcoustIDApplicationKeyInput) (*struct{}, error) {
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		if len(input.Body.Key) == 0 || len(input.Body.Key) > 4096 {
			return nil, huma.Error422UnprocessableEntity("invalid AcoustID application key request")
		}
		if err := setup.SetAcoustIDApplicationKey(ctx, input.Body.Key); err != nil {
			return nil, huma.Error400BadRequest("AcoustID application key could not be saved")
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-acoustid-application-key", Method: http.MethodDelete, Path: "/settings/acoustid/application-key",
		Summary: "Delete the AcoustID application key", Tags: []string{"Settings"},
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		if err := requireSupportedPlatform(ctx, setup); err != nil {
			return nil, err
		}
		if err := setup.DeleteAcoustIDApplicationKey(ctx); err != nil {
			return nil, huma.Error500InternalServerError("AcoustID application key could not be deleted")
		}
		return nil, nil
	})
}
