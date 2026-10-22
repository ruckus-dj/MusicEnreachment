package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type LibraryPublicationInput struct {
	ReleaseID  uuid.UUID `path:"release_id"`
	PositionID uuid.UUID `path:"position_id"`
}

type LibraryPublicationOutput struct {
	Body LibraryPublicationResponse `json:"body"`
}

type LibraryPublicationResponse struct {
	ReleaseID                   uuid.UUID                                        `json:"release_id"`
	PositionID                  uuid.UUID                                        `json:"position_id"`
	ActiveEpoch                 uuid.UUID                                        `json:"active_epoch"`
	MetadataRevision            int64                                            `json:"metadata_revision"`
	CurrentGeneration           *uuid.UUID                                       `json:"current_generation,omitempty"`
	CurrentEpoch                *uuid.UUID                                       `json:"current_epoch,omitempty"`
	CurrentPath                 string                                           `json:"current_path,omitempty"`
	CurrentOwnership            *publicationOwnershipResponse                    `json:"current_ownership,omitempty"`
	CurrentAudio                *publicationAudioResponse                        `json:"current_audio,omitempty"`
	CurrentMetadata             *publicationMetadataResponse                     `json:"current_metadata,omitempty"`
	CurrentAssets               *publicationAssetsResponse                       `json:"current_assets,omitempty"`
	InitialLyrics               *initialLyricsResponse                           `json:"initial_lyrics,omitempty"`
	CurrentQualityPolicyVersion *int                                             `json:"current_quality_policy_version,omitempty"`
	CurrentValidatedAt          *time.Time                                       `json:"current_validated_at,omitempty"`
	ActiveAttempt               *publicationAttemptResponse                      `json:"active_attempt,omitempty"`
	ActiveAttemptCandidate      *uuid.UUID                                       `json:"active_attempt_candidate,omitempty"`
	ActiveAttemptPhase          *string                                          `json:"active_attempt_phase,omitempty"`
	ManualLyrics                *service.LibraryPublicationManualLyrics          `json:"manual_lyrics,omitempty"`
	ManualLyricsReceipt         *service.LibraryPublicationManualLyricsReceipt   `json:"manual_lyrics_receipt,omitempty"`
	CanApplyManualLyrics        bool                                             `json:"can_apply_manual_lyrics"`
	SourceCleanupWarnings       []service.LibraryPublicationSourceCleanupWarning `json:"source_cleanup_warnings,omitempty"`
}

type initialLyricsResponse struct {
	Source                string `json:"source"`
	Outcome               string `json:"outcome"`
	ResolutionWarningCode string `json:"resolution_warning_code,omitempty"`
	OutcomeWarningCode    string `json:"outcome_warning_code,omitempty"`
}

type publicationMetadataResponse struct {
	Version int                 `json:"version"`
	Tags    map[string][]string `json:"tags"`
}

type publicationAssetsResponse struct {
	Version int                       `json:"version"`
	Cover   publicationCoverResponse  `json:"cover"`
	Lyrics  publicationLyricsResponse `json:"lyrics"`
}

type publicationCoverResponse struct {
	State     string `json:"state"`
	SHA256    string `json:"sha256,omitempty"`
	MIME      string `json:"mime,omitempty"`
	SizeBytes *int64 `json:"size_bytes,omitempty"`
}

type publicationLyricsResponse struct {
	State  string `json:"state"`
	SHA256 string `json:"sha256,omitempty"`
	Format string `json:"format,omitempty"`
}

type publicationOwnershipResponse struct {
	SizeBytes *int64 `json:"size_bytes,omitempty"`
	MTime     *int64 `json:"mtime,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type publicationAudioResponse struct {
	StreamIndex              int    `json:"stream_index"`
	Codec                    string `json:"codec"`
	Lossless                 *bool  `json:"lossless"`
	Channels                 *int   `json:"channels,omitempty"`
	Bitrate                  *int   `json:"bitrate,omitempty"`
	BitDepth                 *int   `json:"bit_depth,omitempty"`
	SampleRate               *int   `json:"sample_rate,omitempty"`
	Container                string `json:"container"`
	SupportedSourceContainer bool   `json:"supported_source_container"`
	ALACCapabilitiesVerified bool   `json:"alac_capabilities_verified"`
}

type publicationAttemptResponse struct {
	ID    uuid.UUID `json:"id"`
	State string    `json:"state"`
	Stage string    `json:"stage"`
}

type ApplyManualLyricsInput struct {
	LibraryPublicationInput
	Body struct {
		ExpectedMetadataRevision int64     `json:"expected_metadata_revision" minimum:"0"`
		ExpectedGeneration       uuid.UUID `json:"expected_generation"`
		ExpectedEpoch            uuid.UUID `json:"expected_epoch"`
	} `json:"body"`
}

func registerLibraryPublication(api huma.API, dependencies Dependencies) {
	huma.Register(api, huma.Operation{
		OperationID: "get-library-publication", Method: http.MethodGet,
		Path:    "/library/releases/{release_id}/positions/{position_id}/publication",
		Summary: "Read the current publication for a library position", Tags: []string{"Library"},
	}, func(ctx context.Context, input *LibraryPublicationInput) (*LibraryPublicationOutput, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.LibraryPublications == nil {
			return nil, huma.Error503ServiceUnavailable("library publication service is unavailable")
		}
		view, err := dependencies.LibraryPublications.Read(ctx, input.ReleaseID, input.PositionID)
		if err != nil {
			return nil, libraryPublicationError(err)
		}
		return &LibraryPublicationOutput{Body: libraryPublicationResponse(view)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "apply-library-manual-lyrics", Method: http.MethodPost,
		Path:    "/library/releases/{release_id}/positions/{position_id}/publication/manual-lyrics",
		Summary: "Apply saved manual lyrics to the current publication", Tags: []string{"Library"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, input *ApplyManualLyricsInput) (*OperationOutput, error) {
		if err := requireSetupComplete(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if err := requireSupportedPlatform(ctx, dependencies.Setup); err != nil {
			return nil, err
		}
		if dependencies.LibraryPublications == nil || dependencies.Operations == nil {
			return nil, huma.Error503ServiceUnavailable("library publication service is unavailable")
		}
		id, err := dependencies.LibraryPublications.ApplyManualLyrics(ctx, input.ReleaseID, input.PositionID, service.LibraryPublicationApplyManualLyricsRequest{
			ExpectedMetadataRevision: input.Body.ExpectedMetadataRevision,
			ExpectedGeneration:       input.Body.ExpectedGeneration,
			ExpectedEpoch:            input.Body.ExpectedEpoch,
		})
		if err != nil {
			return nil, libraryPublicationError(err)
		}
		snapshot, err := dependencies.Operations.Snapshot(ctx, id)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to read queued manual lyrics publication")
		}
		return operationOutput(snapshot), nil
	})
}

func libraryPublicationResponse(view service.LibraryPublicationView) LibraryPublicationResponse {
	response := LibraryPublicationResponse{
		ReleaseID: view.ReleaseID, PositionID: view.PositionID, ActiveEpoch: view.ActiveEpoch,
		MetadataRevision: view.MetadataRevision, CurrentGeneration: view.CurrentGeneration,
		CurrentEpoch: view.CurrentEpoch, CurrentPath: view.CurrentPath,
		CurrentQualityPolicyVersion: view.CurrentQualityPolicyVersion, CurrentValidatedAt: view.CurrentValidatedAt,
		ActiveAttemptCandidate: view.ActiveAttemptCandidate, ActiveAttemptPhase: view.ActiveAttemptPhase,
		ManualLyrics: view.ManualLyrics, CanApplyManualLyrics: view.CanApplyManualLyrics,
		ManualLyricsReceipt:   view.ManualLyricsReceipt,
		SourceCleanupWarnings: view.SourceCleanupWarnings,
	}
	if initialLyrics := view.CurrentInitialLyrics; initialLyrics != nil {
		response.InitialLyrics = &initialLyricsResponse{
			Source: initialLyrics.Source, Outcome: initialLyrics.Outcome,
			ResolutionWarningCode: initialLyrics.ResolutionWarningCode,
			OutcomeWarningCode:    initialLyrics.OutcomeWarningCode,
		}
	}
	if metadata := view.CurrentMetadata; metadata != nil {
		tags := make(map[string][]string, len(metadata.Tags))
		for key, values := range metadata.Tags {
			tags[key] = append([]string(nil), values...)
		}
		response.CurrentMetadata = &publicationMetadataResponse{Version: metadata.Version, Tags: tags}
	}
	if assets := view.CurrentAssets; assets != nil {
		var coverSize *int64
		if assets.Cover.SizeBytes != nil {
			size := *assets.Cover.SizeBytes
			coverSize = &size
		}
		response.CurrentAssets = &publicationAssetsResponse{
			Version: assets.Version,
			Cover:   publicationCoverResponse{State: string(assets.Cover.State), SHA256: assets.Cover.SHA256, MIME: assets.Cover.MIME, SizeBytes: coverSize},
			Lyrics:  publicationLyricsResponse{State: string(assets.Lyrics.State), SHA256: assets.Lyrics.SHA256, Format: assets.Lyrics.Format},
		}
	}
	if identity := view.CurrentOwnership; identity != nil {
		response.CurrentOwnership = &publicationOwnershipResponse{SizeBytes: identity.SizeBytes, MTime: identity.MTime, SHA256: identity.SHA256}
	}
	if facts := view.CurrentAudio; facts != nil {
		response.CurrentAudio = &publicationAudioResponse{
			StreamIndex: facts.StreamIndex, Codec: facts.Codec, Lossless: facts.Lossless,
			Channels: facts.Channels, Bitrate: facts.Bitrate, BitDepth: facts.BitDepth,
			SampleRate: facts.SampleRate, Container: string(facts.Container),
			SupportedSourceContainer: facts.SupportedSourceContainer,
			ALACCapabilitiesVerified: facts.ALACCapabilitiesVerified,
		}
	}
	if attempt := view.ActiveAttempt; attempt != nil {
		response.ActiveAttempt = &publicationAttemptResponse{ID: attempt.ID, State: attempt.State, Stage: attempt.Stage}
	}
	return response
}

func libraryPublicationError(err error) error {
	switch {
	case errors.Is(err, persistence.ErrPublicationPositionNotFound):
		return huma.Error404NotFound("library publication position not found")
	case errors.Is(err, service.ErrLibraryPublicationConflict), errors.Is(err, persistence.ErrPublicationAdmissionConflict):
		return huma.Error409Conflict("library publication changed; refresh and retry")
	case errors.Is(err, service.ErrLibraryPublicationUnavailable):
		return huma.Error409Conflict("manual lyrics cannot be applied to this publication")
	default:
		return huma.Error500InternalServerError("library publication request failed")
	}
}
