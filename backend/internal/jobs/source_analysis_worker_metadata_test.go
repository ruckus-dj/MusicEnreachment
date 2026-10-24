package jobs

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type metadataApplyWorkerRepository struct {
	analysisWorkerRepository
	apply   persistence.SourceMetadataApply
	failure *persistence.SourceStepFailure
}

func (repository *metadataApplyWorkerRepository) ApplySourceMetadata(_ context.Context, apply persistence.SourceMetadataApply) (*persistence.SourceMetadataResult, error) {
	repository.apply = apply
	return &persistence.SourceMetadataResult{}, nil
}

func (repository *metadataApplyWorkerRepository) FailSourceAnalysisStep(_ context.Context, failure persistence.SourceStepFailure) error {
	repository.failure = &failure
	return nil
}

func TestSourceAnalysisWorkerAppliesDetachedMetadataCapture(t *testing.T) {
	imageBytes := []byte("known image bytes")
	properties := &metadata.AudioProperties{Format: "FLAC", Channels: 2, SampleRateHz: 44100}
	capture := &metadata.Capture{
		Tags:       map[string][]string{"LYRICS": {"complete lyrics"}},
		Properties: properties,
		CoverInspection: &metadata.CoverInspection{
			Observed: true,
			Cover:    &metadata.Cover{Data: imageBytes, MIME: "image/png"},
		},
	}
	repository := &metadataApplyWorkerRepository{}
	worker := &SourceAnalysisWorker{repository: repository}
	operation := &persistence.Operation{ID: uuid.New(), Attempt: 2}
	work := &persistence.SourceAnalysisWork{ID: uuid.New()}

	_, err := worker.applyPreparedStep(context.Background(), operation, persistence.SourceAnalysisOperationSnapshot{}, 17,
		persistence.SourceAnalysisExecution{}, 3, work, persistence.SourceStepMetadata,
		service.SourceAnalysisPreparation{Metadata: service.SourceAnalysisMetadataOutcome{
			SourceAnalysisStepOutcome: service.SourceAnalysisStepOutcome{State: service.SourceAnalysisSucceeded},
			Capture:                   capture,
		}},
	)
	if err != nil {
		t.Fatalf("apply metadata: %v", err)
	}
	if repository.failure != nil {
		t.Fatalf("unexpected step failure: %+v", repository.failure)
	}
	if !repository.apply.CoverObserved || repository.apply.CoverMIME != "image/png" {
		t.Fatalf("cover observation not passed through: %+v", repository.apply)
	}
	if string(repository.apply.CoverData) != string(imageBytes) {
		t.Fatalf("cover data = %q, want %q", repository.apply.CoverData, imageBytes)
	}
	imageBytes[0] = 'X'
	if string(repository.apply.CoverData) == string(imageBytes) {
		t.Fatal("applied cover data aliases the capture buffer")
	}
	var tags map[string][]string
	if err := json.Unmarshal(repository.apply.ObservedTags, &tags); err != nil {
		t.Fatalf("decode observed tags: %v", err)
	}
	if got := tags["LYRICS"]; len(got) != 1 || got[0] != "complete lyrics" {
		t.Fatalf("lyrics tags = %#v", got)
	}
	var gotProperties metadata.AudioProperties
	if err := json.Unmarshal(repository.apply.Properties, &gotProperties); err != nil {
		t.Fatalf("decode audio properties: %v", err)
	}
	if gotProperties.Format != properties.Format || gotProperties.Channels != properties.Channels || gotProperties.SampleRateHz != properties.SampleRateHz {
		t.Fatalf("properties = %+v, want %+v", gotProperties, properties)
	}
}

func TestSourceAnalysisWorkerPreservesObservedCoverAbsence(t *testing.T) {
	for _, test := range []struct {
		name       string
		inspection *metadata.CoverInspection
		observed   bool
	}{
		{name: "unknown"},
		{name: "observed absent", inspection: &metadata.CoverInspection{Observed: true}, observed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &metadataApplyWorkerRepository{}
			worker := &SourceAnalysisWorker{repository: repository}
			_, err := worker.applyPreparedStep(context.Background(), &persistence.Operation{ID: uuid.New(), Attempt: 1},
				persistence.SourceAnalysisOperationSnapshot{}, 1, persistence.SourceAnalysisExecution{}, 1,
				&persistence.SourceAnalysisWork{ID: uuid.New()}, persistence.SourceStepMetadata,
				service.SourceAnalysisPreparation{Metadata: service.SourceAnalysisMetadataOutcome{
					SourceAnalysisStepOutcome: service.SourceAnalysisStepOutcome{State: service.SourceAnalysisSucceeded},
					Capture:                   &metadata.Capture{Tags: map[string][]string{}, CoverInspection: test.inspection},
				}},
			)
			if err != nil {
				t.Fatalf("apply metadata: %v", err)
			}
			if repository.apply.CoverObserved != test.observed || repository.apply.CoverData != nil || repository.apply.CoverMIME != "" {
				t.Fatalf("cover state = observed %v, data %v, MIME %q", repository.apply.CoverObserved, repository.apply.CoverData, repository.apply.CoverMIME)
			}
		})
	}
}

func TestSourceAnalysisWorkerPersistsOptionalCoverDiagnosticWithMetadata(t *testing.T) {
	const coverError = "cover exceeds the storage limit"
	repository := &metadataApplyWorkerRepository{}
	worker := &SourceAnalysisWorker{repository: repository}
	_, err := worker.applyPreparedStep(context.Background(), &persistence.Operation{ID: uuid.New(), Attempt: 1},
		persistence.SourceAnalysisOperationSnapshot{}, 1, persistence.SourceAnalysisExecution{}, 1,
		&persistence.SourceAnalysisWork{ID: uuid.New()}, persistence.SourceStepMetadata,
		service.SourceAnalysisPreparation{Metadata: service.SourceAnalysisMetadataOutcome{
			SourceAnalysisStepOutcome: service.SourceAnalysisStepOutcome{State: service.SourceAnalysisSucceeded},
			Capture: &metadata.Capture{
				Tags:            map[string][]string{"TITLE": {"preserved title"}},
				CoverInspection: &metadata.CoverInspection{Observed: true, Error: coverError},
			},
		}},
	)
	if err != nil {
		t.Fatalf("apply metadata: %v", err)
	}
	if repository.failure != nil {
		t.Fatalf("unexpected step failure: %+v", repository.failure)
	}
	if repository.apply.CoverError != coverError || !repository.apply.CoverObserved {
		t.Fatalf("cover diagnostic = %q, observed %v; want %q, true", repository.apply.CoverError, repository.apply.CoverObserved, coverError)
	}
	if len(repository.apply.CoverData) != 0 || repository.apply.CoverMIME != "" {
		t.Fatalf("oversized cover unexpectedly retained: data %d bytes, MIME %q", len(repository.apply.CoverData), repository.apply.CoverMIME)
	}
	var tags map[string][]string
	if err := json.Unmarshal(repository.apply.ObservedTags, &tags); err != nil {
		t.Fatalf("decode observed tags: %v", err)
	}
	if got := tags["TITLE"]; len(got) != 1 || got[0] != "preserved title" {
		t.Fatalf("metadata tags = %#v, want preserved title", tags)
	}
}
