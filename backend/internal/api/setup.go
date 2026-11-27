package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type SetupStateOutput struct {
	Body SetupStateBody `json:"body"`
}

type SetupStateBody struct {
	Completed                        bool                           `json:"completed"`
	ConfigurationHealth              ConfigurationHealthResponse    `json:"configuration_health"`
	Platform                         PlatformResponse               `json:"platform"`
	Settings                         RuntimeSettingsResponse        `json:"settings"`
	MusicBrainzConfigurationIdentity string                         `json:"musicbrainz_configuration_identity"`
	GenreCatalog                     service.GenreCatalogSetupState `json:"genre_catalog"`
}

type PlatformResponse struct {
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	Supported  bool   `json:"supported"`
	Diagnostic bool   `json:"diagnostic"`
	Reason     string `json:"reason,omitempty"`
}

type RuntimeSettingsResponse struct {
	SourceFileConcurrency             int        `json:"source_file_concurrency"`
	MatchingPairThreshold             float64    `json:"matching_pair_threshold"`
	ToolsDirectory                    string     `json:"tools_directory"`
	WorkDirectory                     string     `json:"work_directory"`
	OutputDirectory                   string     `json:"output_directory"`
	PublicationFormat                 string     `json:"publication_format"`
	PublicationPathTemplate           string     `json:"publication_path_template"`
	PublicationRemoveSources          bool       `json:"publication_remove_sources"`
	PublicationAutoCompleteAlbums     bool       `json:"publication_auto_complete_albums"`
	MusicBrainzMode                   string     `json:"musicbrainz_mode"`
	MusicBrainzBaseURL                string     `json:"musicbrainz_base_url"`
	MusicBrainzVerifiedAt             *time.Time `json:"musicbrainz_verified_at,omitempty"`
	LRCLIBEnabled                     bool       `json:"lrclib_enabled"`
	SHA256Enabled                     bool       `json:"sha256_enabled"`
	LogLevel                          string     `json:"log_level"`
	ActiveFFmpegInstallationID        string     `json:"active_ffmpeg_installation_id,omitempty"`
	ActiveFPCalcInstallationID        string     `json:"active_fpcalc_installation_id,omitempty"`
	OutputCaseSensitive               *bool      `json:"output_case_sensitive,omitempty"`
	OutputUnicodeNormalization        string     `json:"output_unicode_normalization,omitempty"`
	HasAcoustIDApplicationKey         bool       `json:"has_acoustid_application_key"`
	AcoustIDApplicationKeyVerified    bool       `json:"acoustid_application_key_verified,omitempty"`
	HasGitHubToken                    bool       `json:"has_github_token"`
	MusicBrainzSelfHostedThrottle     bool       `json:"musicbrainz_self_hosted_throttle"`
	MusicBrainzSelfHostedDelaySeconds float64    `json:"musicbrainz_self_hosted_delay_seconds"`
}

type ConfigurationHealthResponse struct {
	Healthy  bool     `json:"healthy"`
	Problems []string `json:"problems"`
}

type SaveRuntimeInput struct {
	Body SaveRuntimeBody `json:"body"`
}

type SaveRuntimeBody struct {
	ToolsDirectory                *string `json:"tools_directory,omitempty" maxLength:"4096"`
	WorkDirectory                 *string `json:"work_directory,omitempty" maxLength:"4096"`
	OutputDirectory               *string `json:"output_directory,omitempty" maxLength:"4096"`
	PublicationFormat             *string `json:"publication_format,omitempty" enum:"source,mka,m4a"`
	PublicationPathTemplate       *string `json:"publication_path_template,omitempty" maxLength:"4096"`
	PublicationRemoveSources      *bool   `json:"publication_remove_sources,omitempty"`
	PublicationAutoCompleteAlbums *bool   `json:"publication_auto_complete_albums,omitempty"`
	SourceFileConcurrency         *int    `json:"source_file_concurrency,omitempty" minimum:"1"`
}

type SaveSetupMusicBrainzInput struct {
	Body SaveSetupMusicBrainzBody `json:"body"`
}

type SaveSetupMusicBrainzBody struct {
	Mode    string `json:"mode" enum:"public,self-hosted"`
	BaseURL string `json:"base_url" maxLength:"2048"`
}

type SetupMusicBrainzIdentityBody struct {
	ConfigurationIdentity string `json:"configuration_identity" minLength:"1" maxLength:"64"`
}

type SetupMusicBrainzIdentityInput struct {
	Body SetupMusicBrainzIdentityBody `json:"body"`
}

type SetupMusicBrainzIdentityOutput struct {
	Body SetupMusicBrainzIdentityBody `json:"body"`
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

type CheckMusicBrainzOutput struct {
	Body CheckMusicBrainzBody `json:"body"`
}

type CheckMusicBrainzBody struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type BrowseDirectoriesInput struct {
	Path   string `query:"path"`
	Offset int    `query:"offset"`
}

type BrowseDirectoriesBody struct {
	CurrentPath string   `json:"current_path"`
	ParentPath  string   `json:"parent_path"`
	Roots       []string `json:"roots"`
	Directories []string `json:"directories"`
	Offset      int      `json:"offset"`
	NextOffset  *int     `json:"next_offset,omitempty"`
}

type BrowseDirectoriesOutput struct {
	Body BrowseDirectoriesBody `json:"body"`
}

func RegisterSetup(api huma.API, setup *service.SetupService) {
	huma.Register(api, huma.Operation{
		OperationID: "browse-server-directories",
		Method:      http.MethodGet,
		Path:        "/setup/directories",
		Summary:     "Browse existing server directories",
		Tags:        []string{"Setup"},
	}, func(ctx context.Context, input *BrowseDirectoriesInput) (*BrowseDirectoriesOutput, error) {
		if err := requireSupportedPlatform(setup); err != nil {
			return nil, err
		}
		listing, err := setup.BrowseDirectories(ctx, input.Path, input.Offset, 200)
		if err != nil {
			return nil, huma.Error400BadRequest("directory is unavailable or invalid")
		}
		return &BrowseDirectoriesOutput{Body: BrowseDirectoriesBody{
			CurrentPath: listing.CurrentPath, ParentPath: listing.ParentPath,
			Roots: listing.Roots, Directories: listing.Directories,
			Offset: listing.Offset, NextOffset: listing.NextOffset,
		}}, nil
	})

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
		if err := requireSupportedPlatform(setup); err != nil {
			return nil, err
		}
		if err := setup.SaveRuntimeUpdate(ctx, service.RuntimeUpdateRequest{
			ToolsDirectory: input.Body.ToolsDirectory, WorkDirectory: input.Body.WorkDirectory, OutputDirectory: input.Body.OutputDirectory,
			PublicationFormat: input.Body.PublicationFormat, SourceFileConcurrency: input.Body.SourceFileConcurrency,
			PublicationPathTemplate:       input.Body.PublicationPathTemplate,
			PublicationRemoveSources:      input.Body.PublicationRemoveSources,
			PublicationAutoCompleteAlbums: input.Body.PublicationAutoCompleteAlbums,
		}); err != nil {
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
		if err := requireSupportedPlatform(setup); err != nil {
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
		OperationID: "check-setup-musicbrainz",
		Method:      http.MethodPost,
		Path:        "/setup/musicbrainz/check",
		Summary:     "Check MusicBrainz connectivity",
		Tags:        []string{"Setup"},
	}, func(ctx context.Context, input *SetupMusicBrainzIdentityInput) (*CheckMusicBrainzOutput, error) {
		if err := requireSetupIncomplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(setup); err != nil {
			return nil, err
		}
		if err := setup.CheckSetupMusicBrainz(ctx, input.Body.ConfigurationIdentity); err != nil {
			if errors.Is(err, service.ErrSettingsConflict) {
				return nil, huma.Error409Conflict("MusicBrainz configuration changed")
			}
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
	}, func(ctx context.Context, input *SetupMusicBrainzIdentityInput) (*struct{}, error) {
		if err := requireSetupIncomplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(setup); err != nil {
			return nil, err
		}
		if err := setup.Complete(ctx, input.Body.ConfigurationIdentity); err != nil {
			if errors.Is(err, service.ErrSetupStateUnavailable) {
				return nil, huma.Error500InternalServerError("failed to read setup state")
			}
			return nil, huma.Error409Conflict("setup requirements are not met")
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "save-setup-musicbrainz", Method: http.MethodPut, Path: "/setup/musicbrainz",
		Summary: "Save setup MusicBrainz configuration", Tags: []string{"Setup"},
	}, func(ctx context.Context, input *SaveSetupMusicBrainzInput) (*SetupMusicBrainzIdentityOutput, error) {
		if err := requireSetupIncomplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(setup); err != nil {
			return nil, err
		}
		identity, err := setup.SaveMusicBrainz(ctx, input.Body.Mode, input.Body.BaseURL)
		if err != nil {
			if errors.Is(err, service.ErrSettingsConflict) {
				return nil, huma.Error409Conflict("setup configuration changed")
			}
			return nil, huma.Error400BadRequest("MusicBrainz settings are invalid")
		}
		return &SetupMusicBrainzIdentityOutput{
			Body: SetupMusicBrainzIdentityBody{ConfigurationIdentity: identity},
		}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "start-setup-genre-catalog-refresh",
		Method:        http.MethodPost,
		Path:          "/setup/genre-catalog/refresh",
		Summary:       "Queue a genre catalog refresh for the verified Setup configuration",
		Tags:          []string{"Setup"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, input *SetupMusicBrainzIdentityInput) (*genreCatalogRefreshOutput, error) {
		if err := requireSetupIncomplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(setup); err != nil {
			return nil, err
		}
		snapshot, err := setup.StartGenreCatalogRefresh(ctx, input.Body.ConfigurationIdentity)
		if err != nil {
			if errors.Is(err, persistence.ErrGenreCatalogSetupCompleted) {
				return nil, huma.Error404NotFound("setup route is closed after completion")
			}
			if errors.Is(err, persistence.ErrGenreCatalogSetupIdentityStale) ||
				errors.Is(err, persistence.ErrGenreCatalogRefreshConflict) {
				return nil, huma.Error409Conflict("MusicBrainz configuration changed or another catalog refresh is still active")
			}
			return nil, huma.Error503ServiceUnavailable("failed to queue genre catalog refresh")
		}
		return &genreCatalogRefreshOutput{Body: operationResponse(snapshot)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "save-setup-lrclib", Method: http.MethodPut, Path: "/setup/lrclib",
		Summary: "Save setup LRCLIB setting", Tags: []string{"Setup"},
	}, func(ctx context.Context, input *UpdateLRCLIBInput) (*struct{}, error) {
		if err := requireSetupIncomplete(ctx, setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(setup); err != nil {
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
			Completed:                        state.Completed,
			MusicBrainzConfigurationIdentity: state.Runtime.MusicBrainzConfigIdentity,
			GenreCatalog:                     state.GenreCatalog,
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
				SourceFileConcurrency:             state.Runtime.SourceFileConcurrency,
				MatchingPairThreshold:             state.Runtime.MatchingPairThreshold,
				ToolsDirectory:                    state.Runtime.ToolsDirectory,
				WorkDirectory:                     state.Runtime.WorkDirectory,
				OutputDirectory:                   state.Runtime.OutputDirectory,
				PublicationFormat:                 state.Runtime.PublicationFormat,
				PublicationPathTemplate:           state.Runtime.PublicationPathTemplate,
				PublicationRemoveSources:          state.Runtime.PublicationRemoveSources,
				PublicationAutoCompleteAlbums:     state.Runtime.PublicationAutoCompleteAlbums,
				MusicBrainzMode:                   state.Runtime.MusicBrainzMode,
				MusicBrainzBaseURL:                state.Runtime.MusicBrainzBaseURL,
				MusicBrainzVerifiedAt:             state.Runtime.MusicBrainzVerifiedAt,
				LRCLIBEnabled:                     state.Runtime.LRCLIBEnabled,
				SHA256Enabled:                     state.SHA256Enabled,
				LogLevel:                          state.Runtime.LogLevel,
				ActiveFFmpegInstallationID:        state.Runtime.ActiveFFmpegInstallation,
				ActiveFPCalcInstallationID:        state.Runtime.ActiveFPCalcInstallation,
				OutputCaseSensitive:               state.Runtime.OutputCaseSensitive,
				OutputUnicodeNormalization:        state.Runtime.OutputUnicodeNormalization,
				HasAcoustIDApplicationKey:         state.Runtime.HasAcoustIDApplicationKey,
				AcoustIDApplicationKeyVerified:    state.Runtime.AcoustIDApplicationKeyVerified,
				HasGitHubToken:                    state.Runtime.HasGitHubToken,
				MusicBrainzSelfHostedThrottle:     state.Runtime.MusicBrainzSelfHostedThrottle,
				MusicBrainzSelfHostedDelaySeconds: state.Runtime.MusicBrainzSelfHostedDelaySeconds,
			},
		},
	}
}

func requireSetupIncomplete(ctx context.Context, setup *service.SetupService) error {
	if setup == nil {
		return huma.Error503ServiceUnavailable("setup service is unavailable")
	}
	completed, err := setup.SetupCompleted(ctx)
	if err != nil {
		return huma.Error500InternalServerError("failed to read setup state")
	}
	if completed {
		return huma.Error404NotFound("setup route is closed after completion")
	}
	return nil
}

func requireSetupComplete(ctx context.Context, setup *service.SetupService) error {
	if setup == nil {
		return huma.Error503ServiceUnavailable("setup service is unavailable")
	}
	completed, err := setup.SetupCompleted(ctx)
	if err != nil {
		return huma.Error500InternalServerError("failed to read setup state")
	}
	if !completed {
		return huma.Error409Conflict("setup is not complete")
	}
	return nil
}

func requireSupportedPlatform(setup *service.SetupService) error {
	if setup == nil {
		return huma.Error503ServiceUnavailable("setup service is unavailable")
	}
	platform := setup.PlatformState()
	if platform.Diagnostic || !platform.Platform.Supported() {
		reason := platform.Reason
		if reason == "" {
			reason = fmt.Sprintf("unsupported instance platform %s/%s", platform.Platform.GOOS, platform.Platform.GOARCH)
		}
		return huma.Error503ServiceUnavailable("product operations are unavailable for this instance platform: " + reason)
	}
	return nil
}
