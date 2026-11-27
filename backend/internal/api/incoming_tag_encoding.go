package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/tagencoding"
)

// IncomingTagEncodingRepository is the API's narrow boundary to saved tag
// encoding review. It exposes only the current visible group and one
// transactional compare-and-swap confirmation.
type IncomingTagEncodingRepository interface {
	Preview(context.Context, uuid.UUID) (persistence.IncomingTagEncodingGroup, error)
	Confirm(context.Context, uuid.UUID, persistence.IncomingTagEncodingConfirmation) (persistence.IncomingTagEncodingGroup, error)
}

type incomingTagEncodingOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         incomingTagEncodingBody
}

type incomingTagEncodingBody struct {
	GroupID            uuid.UUID                       `json:"group_id"`
	Generation         int64                           `json:"generation"`
	GroupRevision      string                          `json:"group_revision"`
	Digest             string                          `json:"digest"`
	CorrectionRevision string                          `json:"correction_revision"`
	Members            []incomingTagEncodingMemberBody `json:"members"`
}

type incomingTagEncodingMemberBody struct {
	VariantID        uuid.UUID                      `json:"variant_id"`
	MetadataResultID uuid.UUID                      `json:"metadata_result_id"`
	Stored           bool                           `json:"stored"`
	Revision         string                         `json:"revision"`
	UserRevision     string                         `json:"user_revision,omitempty"`
	Tags             map[string][]string            `json:"tags"`
	Automatic        []incomingTagEncodingEntryBody `json:"automatic"`
	Candidates       []incomingTagEncodingEntryBody `json:"candidates"`
}

type incomingTagEncodingEntryBody struct {
	Key         string `json:"key"`
	Index       int    `json:"index"`
	Original    string `json:"original"`
	Replacement string `json:"replacement"`
	// Origin is "saved" for entries from a persisted report and "proposed" for
	// the pure Analyze preview of a capture whose tag_encoding is still NULL.
	Origin     string                            `json:"origin"`
	Provenance incomingTagEncodingProvenanceBody `json:"provenance"`
}

type incomingTagEncodingProvenanceBody struct {
	Transform       string                          `json:"transform"`
	DetectorVersion int                             `json:"detector_version"`
	Decision        string                          `json:"decision"`
	Evidence        incomingTagEncodingEvidenceBody `json:"evidence"`
}

type incomingTagEncodingEvidenceBody struct {
	HighBytes     int    `json:"high_bytes"`
	CyrillicRunes int    `json:"cyrillic_runes"`
	LongestRun    int    `json:"longest_run"`
	Signature     string `json:"signature,omitempty"`
}

type incomingTagEncodingGetInput struct {
	GroupID uuid.UUID `path:"group_id"`
}

type incomingTagEncodingConfirmInput struct {
	GroupID uuid.UUID `path:"group_id"`
	Body    incomingTagEncodingConfirmBody
}

type incomingTagEncodingConfirmBody struct {
	Generation         int64                               `json:"generation"`
	GroupRevision      string                              `json:"group_revision" minLength:"1"`
	Digest             string                              `json:"digest" minLength:"1"`
	CorrectionRevision string                              `json:"correction_revision" minLength:"1"`
	Members            []incomingTagEncodingCorrectionBody `json:"members"`
}

type incomingTagEncodingCorrectionBody struct {
	VariantID        uuid.UUID `json:"variant_id"`
	MetadataResultID uuid.UUID `json:"metadata_result_id"`
	CandidateIndexes []int     `json:"candidate_indexes,omitempty"`
	Undo             bool      `json:"undo,omitempty"`
}

func registerIncomingTagEncoding(api huma.API, repository IncomingTagEncodingRepository) {
	huma.Register(api, huma.Operation{
		OperationID: "get-incoming-group-tag-encoding", Method: http.MethodGet, Path: "/incoming-groups/{group_id}/tag-encoding",
		Summary: "Review saved tag encoding repairs for an incoming group", Tags: []string{"Sources"},
	}, func(ctx context.Context, input *incomingTagEncodingGetInput) (*incomingTagEncodingOutput, error) {
		if repository == nil {
			return nil, huma.Error503ServiceUnavailable("incoming tag encoding is unavailable")
		}
		group, err := repository.Preview(ctx, input.GroupID)
		if err != nil {
			return nil, incomingTagEncodingError(err, false)
		}
		return incomingTagEncodingResult(group)
	})

	huma.Register(api, huma.Operation{
		OperationID: "confirm-incoming-group-tag-encoding", Method: http.MethodPost, Path: "/incoming-groups/{group_id}/tag-encoding/confirm",
		Summary: "Confirm saved tag encoding repairs for an incoming group", Tags: []string{"Sources"},
	}, func(ctx context.Context, input *incomingTagEncodingConfirmInput) (*incomingTagEncodingOutput, error) {
		if repository == nil {
			return nil, huma.Error503ServiceUnavailable("incoming tag encoding is unavailable")
		}
		corrections := make([]persistence.IncomingTagEncodingCorrection, 0, len(input.Body.Members))
		for _, member := range input.Body.Members {
			corrections = append(corrections, persistence.IncomingTagEncodingCorrection{
				VariantID:        member.VariantID,
				MetadataResultID: member.MetadataResultID,
				CandidateIndexes: member.CandidateIndexes,
				Undo:             member.Undo,
			})
		}
		group, err := repository.Confirm(ctx, input.GroupID, persistence.IncomingTagEncodingConfirmation{
			Generation:         input.Body.Generation,
			GroupRevision:      input.Body.GroupRevision,
			Digest:             input.Body.Digest,
			CorrectionRevision: input.Body.CorrectionRevision,
			Members:            corrections,
		})
		if err != nil {
			return nil, incomingTagEncodingError(err, true)
		}
		return incomingTagEncodingResult(group)
	})
}

func incomingTagEncodingError(err error, confirm bool) error {
	switch {
	case errors.Is(err, persistence.ErrIncomingTagEncodingUnavailable):
		return huma.Error404NotFound("incoming group is unavailable")
	case errors.Is(err, persistence.ErrIncomingTagEncodingConflict):
		return huma.Error409Conflict("incoming group tag encoding changed")
	case errors.Is(err, persistence.ErrIncomingTagEncodingInvalid):
		return huma.NewError(http.StatusUnprocessableEntity, "tag encoding correction is invalid")
	}
	if confirm {
		return huma.Error500InternalServerError("failed to confirm incoming tag encoding")
	}
	return huma.Error500InternalServerError("failed to read incoming tag encoding")
}

func incomingTagEncodingResult(group persistence.IncomingTagEncodingGroup) (*incomingTagEncodingOutput, error) {
	body := incomingTagEncodingBody{
		GroupID:            group.GroupID,
		Generation:         group.Generation,
		GroupRevision:      group.GroupRevision,
		Digest:             group.Digest,
		CorrectionRevision: group.CorrectionRevision,
		Members:            make([]incomingTagEncodingMemberBody, 0, len(group.Members)),
	}
	for _, member := range group.Members {
		origin := "proposed"
		if member.Stored {
			origin = "saved"
		}
		memberBody := incomingTagEncodingMemberBody{
			VariantID:        member.VariantID,
			MetadataResultID: member.MetadataResultID,
			Stored:           member.Stored,
			Revision:         member.Revision,
			UserRevision:     member.Report.UserRevision,
			Tags:             member.Tags,
			Automatic:        make([]incomingTagEncodingEntryBody, 0, len(member.Report.Automatic.Entries)),
			Candidates:       make([]incomingTagEncodingEntryBody, 0, len(member.Report.Candidates)),
		}
		for _, entry := range member.Report.Automatic.Entries {
			memberBody.Automatic = append(memberBody.Automatic, incomingTagEncodingEntryBody{
				Key: entry.Key, Index: entry.Index, Original: entry.Original, Replacement: entry.Replacement,
				Origin: origin, Provenance: incomingTagEncodingProvenance(entry.Provenance),
			})
		}
		for _, candidate := range member.Report.Candidates {
			memberBody.Candidates = append(memberBody.Candidates, incomingTagEncodingEntryBody{
				Key: candidate.Key, Index: candidate.Index, Original: candidate.Original, Replacement: candidate.Replacement,
				Origin: origin, Provenance: incomingTagEncodingProvenance(candidate.Provenance),
			})
		}
		body.Members = append(body.Members, memberBody)
	}
	return &incomingTagEncodingOutput{CacheControl: "no-store", Body: body}, nil
}

func incomingTagEncodingProvenance(provenance tagencoding.Provenance) incomingTagEncodingProvenanceBody {
	return incomingTagEncodingProvenanceBody{
		Transform:       provenance.Transform,
		DetectorVersion: provenance.DetectorVersion,
		Decision:        string(provenance.Decision),
		Evidence: incomingTagEncodingEvidenceBody{
			HighBytes:     provenance.Evidence.HighBytes,
			CyrillicRunes: provenance.Evidence.CyrillicRunes,
			LongestRun:    provenance.Evidence.LongestRun,
			Signature:     provenance.Evidence.Signature,
		},
	}
}
