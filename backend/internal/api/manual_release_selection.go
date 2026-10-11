package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type manualReleaseSelectionBody struct {
	ExpectedGroupRevision      string    `json:"expected_group_revision" minLength:"1"`
	ExpectedEvaluationRevision *string   `json:"expected_evaluation_revision,omitempty"`
	SelectedRelease            string    `json:"selected_release" minLength:"1"`
	RequestID                  uuid.UUID `json:"request_id"`
}

type manualReleaseSelectionInput struct {
	GroupID uuid.UUID                  `path:"group_id"`
	Body    manualReleaseSelectionBody `json:"body"`
}

type manualReleaseSelectionOutput struct {
	Body struct {
		OperationID uuid.UUID `json:"operation_id"`
	}
}

func registerManualReleaseSelection(api huma.API, selection *service.ManualReleaseSelectionService) {
	huma.Register(api, huma.Operation{
		OperationID: "start-incoming-group-manual-release-selection", Method: http.MethodPost,
		Path:    "/incoming-groups/{group_id}/matching/manual-release",
		Summary: "Prepare an explicitly selected MusicBrainz release for incoming group matching", Tags: []string{"Sources"}, DefaultStatus: http.StatusAccepted,
		Responses: map[string]*huma.Response{"202": {Description: "Manual release lookup admitted"}},
	}, func(ctx context.Context, input *manualReleaseSelectionInput) (*manualReleaseSelectionOutput, error) {
		if selection == nil {
			return nil, huma.Error503ServiceUnavailable("manual release selection is unavailable")
		}
		releaseID, err := parseSelectedRelease(input.Body.SelectedRelease)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("selected release must be a MusicBrainz release MBID or official release URL")
		}
		operation, err := selection.Start(ctx, input.GroupID, input.Body.RequestID, input.Body.ExpectedGroupRevision, input.Body.ExpectedEvaluationRevision, releaseID)
		if errors.Is(err, service.ErrManualReleaseSelectionInvalid) {
			return nil, huma.Error422UnprocessableEntity("manual release selection is invalid")
		}
		if errors.Is(err, persistence.ErrIncomingMatchConflict) {
			return nil, huma.Error409Conflict("incoming group or matching evaluation changed")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to prepare selected release")
		}
		output := &manualReleaseSelectionOutput{}
		output.Body.OperationID = operation.ID
		return output, nil
	})
}

func parseSelectedRelease(value string) (uuid.UUID, error) {
	if strings.TrimSpace(value) != value || value == "" {
		return uuid.Nil, errors.New("invalid selected release")
	}
	if id, err := uuid.Parse(value); err == nil && strings.EqualFold(value, id.String()) {
		return id, nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "musicbrainz.org" || parsed.Host != "musicbrainz.org" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(value, "#") || strings.Contains(value, "?") || parsed.Fragment != "" || parsed.RawPath != "" {
		return uuid.Nil, errors.New("invalid MusicBrainz release URL")
	}
	parts := strings.Split(parsed.EscapedPath(), "/")
	if len(parts) != 3 || parts[1] != "release" || parts[2] == "" {
		return uuid.Nil, errors.New("URL is not a MusicBrainz release")
	}
	id, err := uuid.Parse(parts[2])
	if err != nil || !strings.EqualFold(parts[2], id.String()) {
		return uuid.Nil, errors.New("invalid MusicBrainz release MBID")
	}
	return id, nil
}
