package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type incomingGroupsOutput struct {
	Body incomingGroupsBody
}

type incomingGroupsBody struct {
	Epoch         int64                   `json:"epoch"`
	Groups        []incomingGroupResponse `json:"groups"`
	Files         []incomingFileResponse  `json:"files"`
	Revision      string                  `json:"revision"`
	DraftRevision string                  `json:"draft_revision,omitempty"`
}

type incomingGroupResponse struct {
	ID             uuid.UUID                            `json:"id"`
	Revision       string                               `json:"revision"`
	Manual         bool                                 `json:"manual"`
	Members        []uuid.UUID                          `json:"members"`
	UnreadyMembers []uuid.UUID                          `json:"unready_members"`
	Diagnostics    []incomingGroupingDiagnosticResponse `json:"diagnostics"`
}

type incomingGroupingDiagnosticResponse struct {
	VariantID uuid.UUID `json:"variant_id"`
	Code      string    `json:"code"`
	Values    []string  `json:"values"`
}

type incomingFileResponse struct {
	VariantID         uuid.UUID                  `json:"variant_id"`
	CaptureAnalysisID uuid.UUID                  `json:"capture_analysis_id"`
	CaptureIdentity   string                     `json:"capture_identity"`
	Locations         []incomingLocationResponse `json:"locations"`
	Tags              map[string][]string        `json:"tags"`
}

type incomingLocationResponse struct {
	RootID            uuid.UUID `json:"root_id"`
	WorkID            uuid.UUID `json:"work_id"`
	LocationID        uuid.UUID `json:"location_id"`
	ConfiguredPath    string    `json:"configured_path"`
	InventoryPath     string    `json:"inventory_path"`
	TechnicalIdentity string    `json:"technical_identity"`
	RelativePath      string    `json:"relative_path"`
	SizeBytes         int64     `json:"size_bytes"`
	Mtime             string    `json:"mtime"`
}

type incomingGroupingActionInput struct {
	Kind          string      `json:"kind"`
	GroupIDs      []uuid.UUID `json:"group_ids,omitempty"`
	GroupID       uuid.UUID   `json:"group_id,omitempty"`
	MemberIDs     []uuid.UUID `json:"member_ids,omitempty"`
	TargetGroupID uuid.UUID   `json:"target_group_id,omitempty"`
}

type incomingGroupingEditBody struct {
	Epoch         int64                         `json:"epoch"`
	BaseRevision  string                        `json:"base_revision"`
	DraftRevision string                        `json:"draft_revision,omitempty"`
	Actions       []incomingGroupingActionInput `json:"actions"`
}

type incomingGroupingEditInput struct {
	Body incomingGroupingEditBody
}

func registerIncomingGroups(api huma.API, groups *service.IncomingGroups) {
	huma.Register(api, huma.Operation{
		OperationID: "get-incoming-groups", Method: http.MethodGet, Path: "/incoming-groups",
		Summary: "Read persisted incoming file groups", Tags: []string{"Sources"},
	}, func(ctx context.Context, _ *struct{}) (*incomingGroupsOutput, error) {
		if groups == nil {
			return nil, huma.Error503ServiceUnavailable("incoming groups are unavailable")
		}
		snapshot, err := groups.Snapshot(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read incoming groups")
		}
		return incomingGroupsResult(snapshot)
	})

	huma.Register(api, huma.Operation{
		OperationID: "preview-incoming-groups", Method: http.MethodPost, Path: "/incoming-groups/preview",
		Summary: "Preview incoming group corrections", Tags: []string{"Sources"},
	}, func(ctx context.Context, input *incomingGroupingEditInput) (*incomingGroupsOutput, error) {
		if groups == nil {
			return nil, huma.Error503ServiceUnavailable("incoming groups are unavailable")
		}
		snapshot, err := groups.Snapshot(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read incoming groups")
		}
		if input.Body.Epoch != snapshot.Epoch {
			return nil, huma.Error409Conflict("incoming groups changed")
		}
		base, err := service.IncomingGroupsRevision(snapshot.Groups)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to calculate incoming group revision")
		}
		if input.Body.BaseRevision != base {
			return nil, huma.Error409Conflict("incoming groups changed")
		}
		draft, err := service.ReplayIncomingGroupingActions(snapshot.Groups, base, incomingGroupingActions(input.Body.Actions))
		if err != nil {
			return nil, incomingGroupingError(err)
		}
		preview := snapshot
		preview.Groups = draft.Groups
		result, err := incomingGroupsResult(preview)
		if err != nil {
			return nil, err
		}
		result.Body.DraftRevision = result.Body.Revision
		return result, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "confirm-incoming-groups", Method: http.MethodPost, Path: "/incoming-groups/confirm",
		Summary: "Confirm incoming group corrections", Tags: []string{"Sources"},
	}, func(ctx context.Context, input *incomingGroupingEditInput) (*incomingGroupsOutput, error) {
		if groups == nil {
			return nil, huma.Error503ServiceUnavailable("incoming groups are unavailable")
		}
		actions := incomingGroupingActions(input.Body.Actions)
		if err := groups.ConfirmActions(ctx, input.Body.Epoch, input.Body.BaseRevision, input.Body.DraftRevision, actions); err != nil {
			return nil, incomingGroupingError(err)
		}
		snapshot, err := groups.Snapshot(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read confirmed incoming groups")
		}
		return incomingGroupsResult(snapshot)
	})
}

func incomingGroupingActions(actions []incomingGroupingActionInput) []service.IncomingGroupingAction {
	result := make([]service.IncomingGroupingAction, len(actions))
	for i, action := range actions {
		result[i] = service.IncomingGroupingAction{Kind: action.Kind, GroupIDs: action.GroupIDs, GroupID: action.GroupID, MemberIDs: action.MemberIDs, TargetGroupID: action.TargetGroupID}
	}
	return result
}

func incomingGroupingError(err error) error {
	if errors.Is(err, persistence.ErrIncomingGroupingConflict) {
		return huma.Error409Conflict("incoming groups changed")
	}
	if errors.Is(err, service.ErrIncomingGroupingInvalid) {
		return huma.NewError(http.StatusUnprocessableEntity, "incoming group correction is invalid")
	}
	return huma.Error500InternalServerError("failed to confirm incoming group correction")
}

func incomingGroupsResult(snapshot service.IncomingGroupingSnapshot) (*incomingGroupsOutput, error) {
	revision, err := service.IncomingGroupsRevision(snapshot.Groups)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to calculate incoming group revision")
	}
	body := incomingGroupsBody{Epoch: snapshot.Epoch, Revision: revision, Groups: make([]incomingGroupResponse, 0, len(snapshot.Groups)), Files: make([]incomingFileResponse, 0, len(snapshot.Files))}
	for _, group := range snapshot.Groups {
		diagnostics := make([]incomingGroupingDiagnosticResponse, 0, len(group.Diagnostics))
		for _, diagnostic := range group.Diagnostics {
			diagnostics = append(diagnostics, incomingGroupingDiagnosticResponse{VariantID: diagnostic.VariantID, Code: diagnostic.Code, Values: diagnostic.Values})
		}
		body.Groups = append(body.Groups, incomingGroupResponse{ID: group.ID, Revision: group.Revision, Manual: group.Manual, Members: group.Members, UnreadyMembers: group.UnreadyMembers, Diagnostics: diagnostics})
	}
	for _, file := range snapshot.Files {
		locations := make([]incomingLocationResponse, 0, len(file.Locations))
		for _, location := range file.Locations {
			locations = append(locations, incomingLocationResponse{RootID: location.RootID, WorkID: location.WorkID, LocationID: location.LocationID, ConfiguredPath: location.ConfiguredPath, InventoryPath: location.InventoryPath, TechnicalIdentity: location.TechnicalIdentity, RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: location.Mtime})
		}
		body.Files = append(body.Files, incomingFileResponse{VariantID: file.VariantID, CaptureAnalysisID: file.CaptureAnalysisID, CaptureIdentity: file.CaptureIdentity, Locations: locations, Tags: file.Tags})
	}
	return &incomingGroupsOutput{Body: body}, nil
}
