package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type SourceLocationDetailInput struct {
	RootID     uuid.UUID `path:"source_id"`
	LocationID uuid.UUID `path:"location_id"`
}

type SourceLocationDetailOutput struct {
	Body SourceLocationDetailResponse `json:"body"`
}

// SourceLocationDetailResponse is the inspector read of one location: the
// identity and availability of the file, the optional stored technical result
// and the analysis operation that is currently active. The raw ffprobe JSON is
// served here alone; the location list never carries it.
type SourceLocationDetailResponse struct {
	RootID       uuid.UUID `json:"root_id"`
	LocationID   uuid.UUID `json:"location_id"`
	RelativePath string    `json:"relative_path"`
	SizeBytes    int64     `json:"size_bytes"`
	Mtime        time.Time `json:"mtime"`
	ProbeStatus  string    `json:"probe_status" enum:"audio,no_audio,probe_error"`
	SafeError    *string   `json:"safe_error,omitempty"`

	Root SourceLocationRootStateResponse `json:"root"`

	MediaVariantID *uuid.UUID                     `json:"media_variant_id,omitempty"`
	AnalysisState  string                         `json:"analysis_state" enum:"not_analyzed,analyzed"`
	Result         *SourceTechnicalResultResponse `json:"result,omitempty"`

	ActiveAnalysisOperationID *uuid.UUID                   `json:"active_analysis_operation_id,omitempty"`
	SelectedProbeVariantID    *uuid.UUID                   `json:"selected_probe_variant_id,omitempty"`
	ActiveFPCalcVersion       *string                      `json:"active_fpcalc_version,omitempty"`
	Steps                     []SourceAnalysisStepResponse `json:"steps"`
	MatchingEligible          bool                         `json:"matching_eligible"`
	StagedArtifact            SourceStagedArtifactResponse `json:"staged_artifact"`
}

// SourceStagedArtifactResponse exposes the bounded registry state, never a
// filesystem stat or an absolute path. The unknown state explicitly represents
// a snapshot with no evidence of a staged artifact or staged preparation.
type SourceStagedArtifactResponse struct {
	ArtifactID          *uuid.UUID `json:"artifact_id,omitempty"`
	State               string     `json:"state" enum:"unknown,preparation,acquiring,ready,retained,cleanup_eligible,cleanup_failed"`
	RequestedSteps      []string   `json:"requested_steps"`
	RequestedStepsKnown bool       `json:"requested_steps_known"`
	CreatorOperationID  *uuid.UUID `json:"creator_operation_id,omitempty"`
	BorrowerOperationID *uuid.UUID `json:"borrower_operation_id,omitempty"`
	SafeError           *string    `json:"safe_error,omitempty"`
	RelativeOutputPath  *string    `json:"relative_output_path,omitempty"`
}

type SourceAnalysisStepResponse struct {
	Name        string                      `json:"name" enum:"sha256,probe,fingerprint,metadata"`
	State       string                      `json:"state" enum:"not_requested,pending,queued,running,succeeded,failed,skipped"`
	SafeError   *string                     `json:"safe_error,omitempty"`
	SkipReason  *string                     `json:"skip_reason,omitempty"`
	Attempt     int                         `json:"attempt"`
	ReuseOrigin *string                     `json:"reuse_origin,omitempty"`
	SHA256      *SourceSHA256ResultResponse `json:"sha256,omitempty"`
	Fingerprint *SourceFingerprintResponse  `json:"fingerprint,omitempty"`
	Metadata    *SourceMetadataResponse     `json:"metadata,omitempty"`
}

type SourceMetadataResponse struct {
	Tags       map[string][]string              `json:"tags"`
	Provenance SourceMetadataProvenanceResponse `json:"provenance"`
	Matroska   json.RawMessage                  `json:"native_matroska,omitempty"`
}

type SourceMetadataProvenanceResponse struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Contract string `json:"contract"`
}

type SourceSHA256ResultResponse struct {
	Value              string     `json:"value"`
	Algorithm          *string    `json:"algorithm,omitempty"`
	CalculatedAt       *time.Time `json:"calculated_at,omitempty"`
	AppliedOperationID *uuid.UUID `json:"applied_operation_id,omitempty"`
}

type SourceFingerprintResponse struct {
	Value                 string    `json:"value"`
	Version               string    `json:"version"`
	VersionBanner         string    `json:"version_banner"`
	AlgorithmNamespace    string    `json:"algorithm_namespace"`
	AlgorithmID           int16     `json:"algorithm_id"`
	Duration              float64   `json:"duration"`
	CalculatedAt          time.Time `json:"calculated_at"`
	AppliedOperationID    uuid.UUID `json:"applied_operation_id"`
	ParserContractVersion int       `json:"parser_contract_version"`
}

// SourceLocationRootStateResponse is the availability of the owning root,
// reported separately from the technical result so a stale inventory or an
// unavailable root is never mistaken for a property of the analysis.
type SourceLocationRootStateResponse struct {
	Status        string  `json:"status" enum:"unknown,available,unavailable"`
	SafeError     *string `json:"safe_error,omitempty"`
	Enabled       bool    `json:"enabled"`
	Stale         bool    `json:"stale"`
	InventoryPath *string `json:"inventory_path,omitempty"`
}

// SourceTechnicalResultResponse is the typed projection of one stored ffprobe
// result. Unknown technical values are absent, never zero or empty, and every
// audio stream is listed; non-audio streams stay in raw_json.
type SourceTechnicalResultResponse struct {
	FFProbeVersion        string                               `json:"ffprobe_version"`
	AnalysisPolicyVersion int                                  `json:"analysis_policy_version"`
	InspectedAt           time.Time                            `json:"inspected_at"`
	AppliedOperationID    uuid.UUID                            `json:"applied_operation_id"`
	Container             SourceTechnicalContainerResponse     `json:"container"`
	Streams               []SourceTechnicalAudioStreamResponse `json:"streams"`
	Tags                  map[string][]string                  `json:"tags"`
	RawJSON               map[string]any                       `json:"raw_json"`
}

type SourceTechnicalContainerResponse struct {
	Name       *string `json:"name,omitempty"`
	LongName   *string `json:"long_name,omitempty"`
	DurationMS *int64  `json:"duration_ms,omitempty"`
	BitRate    *int64  `json:"bit_rate,omitempty"`
}

type SourceTechnicalAudioStreamResponse struct {
	Index         *int64  `json:"index,omitempty"`
	CodecName     *string `json:"codec_name,omitempty"`
	Profile       *string `json:"profile,omitempty"`
	DurationMS    *int64  `json:"duration_ms,omitempty"`
	BitRate       *int64  `json:"bit_rate,omitempty"`
	SampleRateHz  *int64  `json:"sample_rate_hz,omitempty"`
	SampleFormat  *string `json:"sample_format,omitempty"`
	BitsPerSample *int64  `json:"bits_per_sample,omitempty"`
	Channels      *int64  `json:"channels,omitempty"`
	ChannelLayout *string `json:"channel_layout,omitempty"`
}

func registerSourceLocationDetail(api huma.API, dependencies Dependencies) {
	huma.Register(api, huma.Operation{
		OperationID: "get-source-location", Method: http.MethodGet,
		Path:    "/sources/{source_id}/locations/{location_id}",
		Summary: "Inspect one source location", Tags: []string{"Sources"},
	}, func(ctx context.Context, input *SourceLocationDetailInput) (*SourceLocationDetailOutput, error) {
		// The inspector is a pure read of the published inventory and the stored
		// result: it stays available while the platform is diagnostic or the
		// first-run Setup is unfinished, because it never probes the source.
		if dependencies.SourceLocationDetails == nil {
			return nil, huma.Error503ServiceUnavailable("source location inspector is unavailable")
		}
		detail, err := dependencies.SourceLocationDetails.Read(ctx, input.RootID, input.LocationID)
		if err != nil {
			switch {
			case service.IsSourceRootNotFound(err):
				return nil, huma.Error404NotFound("source root not found")
			case errors.Is(err, service.ErrSourceLocationNotFound):
				return nil, huma.Error404NotFound("source location not found")
			default:
				return nil, huma.Error500InternalServerError("failed to read the source location")
			}
		}
		response, err := sourceLocationDetailResponse(detail)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to present the stored technical result")
		}
		return &SourceLocationDetailOutput{Body: response}, nil
	})
}

func sourceLocationDetailResponse(detail service.SourceLocationDetail) (SourceLocationDetailResponse, error) {
	response := SourceLocationDetailResponse{
		RootID: detail.RootID, LocationID: detail.LocationID, RelativePath: detail.RelativePath,
		SizeBytes: detail.SizeBytes, Mtime: detail.Mtime, ProbeStatus: detail.ProbeStatus,
		SafeError:      detail.SafeError,
		MediaVariantID: detail.MediaVariantID, AnalysisState: "not_analyzed",
		ActiveAnalysisOperationID: detail.ActiveAnalysisOperationID,
		SelectedProbeVariantID:    detail.SelectedProbeVariantID,
		ActiveFPCalcVersion:       detail.ActiveFPCalcVersion,
		Steps:                     make([]SourceAnalysisStepResponse, 0, len(detail.Steps)),
		MatchingEligible:          detail.MatchingEligible,
		Root: SourceLocationRootStateResponse{
			Status: detail.Root.Status, SafeError: detail.Root.SafeError, Enabled: detail.Root.Enabled,
			Stale: detail.Root.Stale, InventoryPath: detail.Root.InventoryPath,
		},
	}
	artifact := detail.StagedArtifact
	response.StagedArtifact = SourceStagedArtifactResponse{
		ArtifactID: artifact.ID, State: artifact.State,
		RequestedSteps: append([]string{}, artifact.RequestedSteps...), RequestedStepsKnown: artifact.RequestedStepsKnown,
		CreatorOperationID: artifact.CreatorOperationID, BorrowerOperationID: artifact.BorrowerOperationID,
		SafeError: artifact.SafeError, RelativeOutputPath: artifact.RelativeOutputPath,
	}
	for _, step := range detail.Steps {
		stepResponse := SourceAnalysisStepResponse{
			Name: step.Name, State: step.State, SafeError: step.SafeError,
			SkipReason: step.SkipReason, Attempt: step.Attempt, ReuseOrigin: step.ReuseOrigin,
		}
		if step.SHA256 != nil {
			stepResponse.SHA256 = &SourceSHA256ResultResponse{
				Value: step.SHA256.Value, Algorithm: step.SHA256.Algorithm,
				CalculatedAt: step.SHA256.CalculatedAt, AppliedOperationID: step.SHA256.AppliedOperationID,
			}
		}
		if step.Fingerprint != nil {
			fingerprint := step.Fingerprint
			stepResponse.Fingerprint = &SourceFingerprintResponse{
				Value: fingerprint.Value, Version: fingerprint.Version, VersionBanner: fingerprint.VersionBanner,
				AlgorithmNamespace: fingerprint.AlgorithmNamespace, AlgorithmID: fingerprint.AlgorithmID,
				Duration: fingerprint.Duration, CalculatedAt: fingerprint.CalculatedAt,
				AppliedOperationID:    fingerprint.AppliedOperationID,
				ParserContractVersion: fingerprint.ParserContractVersion,
			}
		}
		if step.Metadata != nil {
			stepResponse.Metadata = &SourceMetadataResponse{
				Tags: step.Metadata.Tags,
				Provenance: SourceMetadataProvenanceResponse{
					Name: step.Metadata.Provenance.Name, Version: step.Metadata.Provenance.Version,
					Contract: step.Metadata.Provenance.Contract,
				},
				Matroska: step.Metadata.Matroska,
			}
		}
		response.Steps = append(response.Steps, stepResponse)
	}
	if detail.Result == nil {
		return response, nil
	}
	raw := map[string]any{}
	if err := json.Unmarshal(detail.Result.Analysis.RawJSON, &raw); err != nil {
		return SourceLocationDetailResponse{}, err
	}
	streams := make([]SourceTechnicalAudioStreamResponse, 0, len(detail.Result.Analysis.Streams))
	for _, stream := range detail.Result.Analysis.Streams {
		streams = append(streams, SourceTechnicalAudioStreamResponse{
			Index: stream.Index, CodecName: stream.CodecName, Profile: stream.Profile,
			DurationMS: stream.DurationMS, BitRate: stream.BitRate, SampleRateHz: stream.SampleRateHz,
			SampleFormat: stream.SampleFormat, BitsPerSample: stream.BitsPerSample,
			Channels: stream.Channels, ChannelLayout: stream.ChannelLayout,
		})
	}
	response.AnalysisState = "analyzed"
	response.Result = &SourceTechnicalResultResponse{
		FFProbeVersion:        detail.Result.FFProbeVersion,
		AnalysisPolicyVersion: detail.Result.AnalysisPolicyVersion,
		InspectedAt:           detail.Result.InspectedAt,
		AppliedOperationID:    detail.Result.AppliedOperationID,
		Container: SourceTechnicalContainerResponse{
			Name: detail.Result.Analysis.Container.Name, LongName: detail.Result.Analysis.Container.LongName,
			DurationMS: detail.Result.Analysis.Container.DurationMS, BitRate: detail.Result.Analysis.Container.BitRate,
		},
		Streams: streams,
		Tags:    detail.Result.Analysis.Tags,
		RawJSON: raw,
	}
	return response, nil
}
