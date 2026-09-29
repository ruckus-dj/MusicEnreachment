package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
)

type CatalogInput struct {
	PackageKind tools.PackageKind `query:"package_kind" enum:"ffmpeg,fpcalc" required:"true"`
}

type CatalogOutput struct {
	Body CatalogBody `json:"body"`
}

type CatalogBody struct {
	PackageKind tools.PackageKind `json:"package_kind"`
	Platform    PlatformResponse  `json:"platform"`
	Releases    []ReleaseResponse `json:"releases"`
	Notice      string            `json:"notice,omitempty"`
}

type ReleaseResponse struct {
	Identity  string             `json:"identity"`
	Source    string             `json:"source"`
	Artifacts []ArtifactResponse `json:"artifacts"`
}

type ArtifactResponse struct {
	Name             string `json:"name"`
	ChecksumProvided bool   `json:"checksum_provided"`
}

type InstallPreflightInput struct {
	Body struct {
		PackageKind     tools.PackageKind `json:"package_kind" enum:"ffmpeg,fpcalc"`
		ReleaseIdentity string            `json:"release_identity" minLength:"1" maxLength:"128"`
	}
}

type InstallPreflightOutput struct {
	Body InstallPreflightBody `json:"body"`
}

type InstallPreflightBody struct {
	Token     string   `json:"preflight_token"`
	Targets   []string `json:"targets"`
	Conflicts []string `json:"conflicts"`
}

type StartInstallInput struct {
	Body StartInstallBody `json:"body"`
}

type StartInstallBody struct {
	PreflightToken     string   `json:"preflight_token" minLength:"1"`
	ConfirmedConflicts []string `json:"confirmed_conflicts,omitempty"`
}

type InstallationsInput struct {
	PackageKind tools.PackageKind `query:"package_kind" enum:"ffmpeg,fpcalc"`
}

type InstallationsOutput struct {
	Body InstallationsBody `json:"body"`
}

type InstallationsBody struct {
	Installations []InstallationResponse `json:"installations"`
}

type InstallationResponse struct {
	ID                 uuid.UUID         `json:"id"`
	PackageKind        string            `json:"package_kind"`
	SourceName         string            `json:"source_name"`
	ReleaseIdentity    string            `json:"release_identity"`
	State              string            `json:"state"`
	Active             bool              `json:"active"`
	ExecutableVersions map[string]string `json:"executable_versions"`
	CreatedAt          time.Time         `json:"created_at"`
	VerifiedAt         *time.Time        `json:"verified_at,omitempty"`
}

type InstallationActionInput struct {
	ID   uuid.UUID `path:"installation_id"`
	Body struct {
		PackageKind string `json:"package_kind" enum:"ffmpeg,fpcalc"`
	}
}

type MovePreflightInput struct {
	Body struct {
		NewRoot        string `json:"new_tools_directory" minLength:"1" maxLength:"4096"`
		RemoveOldFiles bool   `json:"remove_old_files"`
	}
}

type MovePreflightOutput struct {
	Body MovePreflightBody `json:"body"`
}

type MovePreflightBody struct {
	Token     string   `json:"preflight_token"`
	Conflicts []string `json:"conflicts"`
	FileCount int      `json:"managed_file_count"`
}

type StartMoveInput struct {
	Body StartMoveBody `json:"body"`
}

type StartMoveBody struct {
	PreflightToken     string   `json:"preflight_token" minLength:"1"`
	ConfirmedConflicts []string `json:"confirmed_conflicts,omitempty"`
}

func registerTools(api huma.API, dependencies Dependencies, tokens *preflightTokens) {
	huma.Register(api, huma.Operation{
		OperationID: "list-tool-catalog", Method: http.MethodGet, Path: "/tools/catalog",
		Summary: "List compatible tool releases", Tags: []string{"Tools"},
	}, func(ctx context.Context, input *CatalogInput) (*CatalogOutput, error) {
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.Catalog == nil || dependencies.Setup == nil {
			return nil, huma.Error503ServiceUnavailable("tool catalog is unavailable")
		}
		state, err := dependencies.Setup.State(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read instance state")
		}
		result, err := dependencies.Catalog.ListWithNotice(ctx, input.PackageKind)
		if err != nil {
			return nil, huma.Error502BadGateway("upstream tool catalog is unavailable")
		}
		response := make([]ReleaseResponse, 0, len(result.Releases))
		for _, release := range result.Releases {
			artifacts := make([]ArtifactResponse, 0, len(release.Artifacts))
			for _, artifact := range release.Artifacts {
				artifacts = append(artifacts, ArtifactResponse{Name: artifact.Name, ChecksumProvided: artifact.ChecksumProvided})
			}
			response = append(response, ReleaseResponse{Identity: release.Identity, Source: release.Source, Artifacts: artifacts})
		}
		return &CatalogOutput{Body: CatalogBody{
			PackageKind: input.PackageKind,
			Platform: PlatformResponse{
				GOOS: state.Platform.Platform.GOOS, GOARCH: state.Platform.Platform.GOARCH,
				Supported: state.Platform.Platform.Supported(), Diagnostic: state.Platform.Diagnostic,
				Reason: state.Platform.Reason,
			},
			Releases: response, Notice: result.Notice,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "preflight-tool-install", Method: http.MethodPost, Path: "/tools/installations/preflight",
		Summary: "Preflight a tool installation", Tags: []string{"Tools"},
	}, func(ctx context.Context, input *InstallPreflightInput) (*InstallPreflightOutput, error) {
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.InstallOperations == nil {
			return nil, huma.Error503ServiceUnavailable("installation service is unavailable")
		}
		plan, err := dependencies.InstallOperations.Preflight(ctx, input.Body.PackageKind, input.Body.ReleaseIdentity)
		if err != nil {
			return nil, huma.Error409Conflict("release cannot be installed")
		}
		token := tokens.issueInstall(plan)
		return &InstallPreflightOutput{Body: InstallPreflightBody{Token: token, Targets: plan.Targets, Conflicts: plan.Conflicts}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "start-tool-install", Method: http.MethodPost, Path: "/tools/installations",
		Summary: "Start a tool installation", Tags: []string{"Tools"},
	}, func(ctx context.Context, input *StartInstallInput) (*OperationOutput, error) {
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.InstallOperations == nil {
			return nil, huma.Error503ServiceUnavailable("installation service is unavailable")
		}
		entry, exists := tokens.consume(input.Body.PreflightToken)
		if !exists || entry.install == nil {
			return nil, huma.Error409Conflict("installation preflight expired or is invalid")
		}
		operation, err := dependencies.InstallOperations.StartFromPreflight(ctx, *entry.install, input.Body.ConfirmedConflicts)
		if err != nil {
			return nil, huma.Error409Conflict("installation could not be started")
		}
		if dependencies.Operations == nil {
			return nil, huma.Error503ServiceUnavailable("operation service is unavailable")
		}
		snapshot, err := dependencies.Operations.Snapshot(ctx, operation.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read started operation")
		}
		return operationOutput(snapshot), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-installations", Method: http.MethodGet, Path: "/tools/installations",
		Summary: "List installed tool packages", Tags: []string{"Tools"},
	}, func(ctx context.Context, input *InstallationsInput) (*InstallationsOutput, error) {
		if dependencies.Installations == nil {
			return nil, huma.Error503ServiceUnavailable("installation service is unavailable")
		}
		installations, err := dependencies.Installations.List(ctx, string(input.PackageKind))
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to list installations")
		}
		response := make([]InstallationResponse, 0, len(installations))
		for _, installation := range installations {
			response = append(response, InstallationResponse{
				ID: installation.ID, PackageKind: installation.PackageKind, SourceName: installation.SourceName,
				ReleaseIdentity: installation.ReleaseIdentity, State: installation.State, Active: installation.Active,
				ExecutableVersions: installation.ExecutableVersions, CreatedAt: installation.CreatedAt,
				VerifiedAt: installation.VerifiedAt,
			})
		}
		return &InstallationsOutput{Body: InstallationsBody{Installations: response}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "activate-tool-installation", Method: http.MethodPost, Path: "/tools/installations/{installation_id}/activate",
		Summary: "Activate a verified tool package", Tags: []string{"Tools"},
	}, func(ctx context.Context, input *InstallationActionInput) (*struct{}, error) {
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.Installations == nil {
			return nil, huma.Error503ServiceUnavailable("installation service is unavailable")
		}
		if err := dependencies.Installations.Activate(ctx, input.Body.PackageKind, input.ID); err != nil {
			return nil, huma.Error409Conflict("installation could not be activated")
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-tool-installation", Method: http.MethodDelete, Path: "/tools/installations/{installation_id}",
		Summary: "Delete an inactive tool package", Tags: []string{"Tools"},
	}, func(ctx context.Context, input *InstallationActionInput) (*struct{}, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.Installations == nil {
			return nil, huma.Error503ServiceUnavailable("installation service is unavailable")
		}
		if err := dependencies.Installations.Delete(ctx, input.Body.PackageKind, input.ID); err != nil {
			return nil, huma.Error409Conflict("installation could not be deleted")
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "preflight-tools-root-move", Method: http.MethodPost, Path: "/tools/move/preflight",
		Summary: "Preflight a tools directory move", Tags: []string{"Tools"},
	}, func(ctx context.Context, input *MovePreflightInput) (*MovePreflightOutput, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.MoveTools == nil {
			return nil, huma.Error503ServiceUnavailable("move service is unavailable")
		}
		plan, err := dependencies.MoveTools.Preflight(ctx, input.Body.NewRoot, input.Body.RemoveOldFiles)
		if err != nil {
			return nil, huma.Error400BadRequest("tools directory preflight failed")
		}
		token := tokens.issueMove(plan)
		return &MovePreflightOutput{Body: MovePreflightBody{Token: token, Conflicts: plan.Conflicts, FileCount: len(plan.Snapshot.Files)}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "start-tools-root-move", Method: http.MethodPost, Path: "/tools/move",
		Summary: "Start a tools directory move", Tags: []string{"Tools"},
	}, func(ctx context.Context, input *StartMoveInput) (*OperationOutput, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.MoveTools == nil {
			return nil, huma.Error503ServiceUnavailable("move service is unavailable")
		}
		entry, exists := tokens.consume(input.Body.PreflightToken)
		if !exists || entry.move == nil {
			return nil, huma.Error409Conflict("move preflight expired or is invalid")
		}
		operation, err := dependencies.MoveTools.Start(ctx, *entry.move, input.Body.ConfirmedConflicts)
		if err != nil {
			return nil, huma.Error409Conflict("tools directory move could not be started")
		}
		if dependencies.Operations == nil {
			return nil, huma.Error503ServiceUnavailable("operation service is unavailable")
		}
		snapshot, err := dependencies.Operations.Snapshot(ctx, operation.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read started move")
		}
		return operationOutput(snapshot), nil
	})
}
