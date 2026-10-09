package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type sourceLocationDetailRepositoryFixture struct {
	err      error
	snapshot *persistence.SourceLocationDetailSnapshot
}

func (fixture sourceLocationDetailRepositoryFixture) ReadSourceLocationDetail(context.Context, uuid.UUID, uuid.UUID) (*persistence.SourceLocationDetailSnapshot, error) {
	return fixture.snapshot, fixture.err
}

func TestSourceLocationDetailDoesNotClassifyMissingVariantAsMissingRoot(t *testing.T) {
	variantErr := fmt.Errorf("read source location detail variant: %w", persistence.ErrMediaVariantNotFound)
	details := service.NewSourceLocationDetails(sourceLocationDetailRepositoryFixture{err: variantErr})

	_, err := details.Read(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, persistence.ErrMediaVariantNotFound) {
		t.Fatalf("missing variant error = %v, want ErrMediaVariantNotFound", err)
	}
	if service.IsSourceRootNotFound(err) {
		t.Fatalf("missing variant was classified as a missing root: %v", err)
	}
}

func TestSourceLocationDetailKeepsCanonicalAndSelectedProbeIdentityAndActiveVersionSeparate(t *testing.T) {
	canonicalID, selectedID := uuid.New(), uuid.New()
	rootID, locationID := uuid.New(), uuid.New()
	detail, err := service.NewSourceLocationDetails(sourceLocationDetailRepositoryFixture{
		snapshot: &persistence.SourceLocationDetailSnapshot{
			Root: &persistence.SourceRoot{ID: rootID, ConfiguredPath: "/music", Enabled: true, Status: "available"},
			Location: &persistence.SourceLocation{
				ID: locationID, SourceRootID: rootID, RelativePath: "track.flac", MediaVariantID: &canonicalID,
			},
			Variant: &persistence.MediaVariant{
				ID: selectedID, FFProbeJSON: []byte(`{"format":{},"streams":[]}`),
			},
			ActiveFPCalcInstallation: &persistence.ToolInstallation{
				ExecutableVersions: []byte(`{"fpcalc":"fpcalc version 1.6.0 (FFmpeg Lavc62.11.100)"}`),
			},
		},
	}).Read(context.Background(), rootID, locationID)
	if err != nil {
		t.Fatalf("read source location detail: %v", err)
	}
	if detail.MediaVariantID == nil || *detail.MediaVariantID != canonicalID {
		t.Fatalf("canonical media variant = %v, want %s", detail.MediaVariantID, canonicalID)
	}
	if detail.SelectedProbeVariantID == nil || *detail.SelectedProbeVariantID != selectedID {
		t.Fatalf("selected probe variant = %v, want %s", detail.SelectedProbeVariantID, selectedID)
	}
	if detail.ActiveFPCalcVersion == nil || *detail.ActiveFPCalcVersion != "1.6.0" {
		t.Fatalf("active fpcalc version = %v, want parsed version 1.6.0", detail.ActiveFPCalcVersion)
	}
}

func TestSourceLocationDetailDoesNotInventActiveFPCalcVersion(t *testing.T) {
	for _, installation := range []*persistence.ToolInstallation{
		nil,
		{ExecutableVersions: []byte(`{"fpcalc":"fpcalc version ???"}`)},
		{ExecutableVersions: []byte(`{invalid json`)},
	} {
		rootID, locationID := uuid.New(), uuid.New()
		detail, err := service.NewSourceLocationDetails(sourceLocationDetailRepositoryFixture{
			snapshot: &persistence.SourceLocationDetailSnapshot{
				Root:                     &persistence.SourceRoot{ID: rootID},
				Location:                 &persistence.SourceLocation{ID: locationID, SourceRootID: rootID},
				ActiveFPCalcInstallation: installation,
			},
		}).Read(context.Background(), rootID, locationID)
		if err != nil {
			t.Fatalf("read source location detail: %v", err)
		}
		if detail.ActiveFPCalcVersion != nil {
			t.Fatalf("active fpcalc version = %q, want unavailable", *detail.ActiveFPCalcVersion)
		}
	}
}

func TestSourceLocationDetailProjectsStagedArtifactWithoutInventingPreparationIdentity(t *testing.T) {
	rootID, locationID := uuid.New(), uuid.New()
	preparing, err := service.NewSourceLocationDetails(sourceLocationDetailRepositoryFixture{
		snapshot: &persistence.SourceLocationDetailSnapshot{
			Root:           &persistence.SourceRoot{ID: rootID},
			Location:       &persistence.SourceLocation{ID: locationID, SourceRootID: rootID},
			StagedArtifact: &persistence.SourceLocationStagedArtifact{State: "preparation", RequestedSteps: []string{"sha256"}, RequestedStepsKnown: true},
		},
	}).Read(context.Background(), rootID, locationID)
	if err != nil {
		t.Fatalf("read preparing location: %v", err)
	}
	if preparing.StagedArtifact.State != "preparation" || preparing.StagedArtifact.ID != nil || preparing.StagedArtifact.CreatorOperationID != nil || !preparing.StagedArtifact.RequestedStepsKnown || len(preparing.StagedArtifact.RequestedSteps) != 1 || preparing.StagedArtifact.RequestedSteps[0] != "sha256" {
		t.Fatalf("preparation projection = %#v", preparing.StagedArtifact)
	}

	artifactID, creatorID, borrowerID := uuid.New(), uuid.New(), uuid.New()
	safeError := "cleanup could not be completed"
	projected, err := service.NewSourceLocationDetails(sourceLocationDetailRepositoryFixture{
		snapshot: &persistence.SourceLocationDetailSnapshot{
			Root:     &persistence.SourceRoot{ID: rootID},
			Location: &persistence.SourceLocation{ID: locationID, SourceRootID: rootID},
			StagedArtifact: &persistence.SourceLocationStagedArtifact{
				ID: artifactID, State: "cleanup_failed", RequestedSteps: []string{"sha256", "probe"}, RequestedStepsKnown: true,
				CreatorOperationID: &creatorID, BorrowerOperationID: &borrowerID, SafeError: &safeError,
				RelativeOutputPath: "analysis/staging/root/work/artifact",
			},
		},
	}).Read(context.Background(), rootID, locationID)
	if err != nil {
		t.Fatalf("read artifact location: %v", err)
	}
	artifact := projected.StagedArtifact
	if artifact.ID == nil || *artifact.ID != artifactID || artifact.State != "cleanup_failed" || len(artifact.RequestedSteps) != 2 || !artifact.RequestedStepsKnown || artifact.CreatorOperationID == nil || *artifact.CreatorOperationID != creatorID || artifact.BorrowerOperationID == nil || *artifact.BorrowerOperationID != borrowerID || artifact.SafeError == nil || *artifact.SafeError != safeError {
		t.Fatalf("artifact projection = %#v", artifact)
	}
}

func TestSourceLocationDetailKeepsUnknownArtifactShapeAndProjectsRegisteredStates(t *testing.T) {
	rootID, locationID := uuid.New(), uuid.New()
	read := func(artifact *persistence.SourceLocationStagedArtifact) service.SourceLocationDetail {
		t.Helper()
		detail, err := service.NewSourceLocationDetails(sourceLocationDetailRepositoryFixture{
			snapshot: &persistence.SourceLocationDetailSnapshot{
				Root:           &persistence.SourceRoot{ID: rootID},
				Location:       &persistence.SourceLocation{ID: locationID, SourceRootID: rootID},
				StagedArtifact: artifact,
			},
		}).Read(context.Background(), rootID, locationID)
		if err != nil {
			t.Fatalf("read source location detail: %v", err)
		}
		return detail
	}

	unknown := read(nil).StagedArtifact
	if unknown.State != "unknown" || len(unknown.RequestedSteps) != 0 || unknown.RequestedStepsKnown || unknown.ID != nil || unknown.CreatorOperationID != nil || unknown.BorrowerOperationID != nil || unknown.RelativeOutputPath != nil {
		t.Fatalf("unknown staged artifact projection = %#v", unknown)
	}

	creatorID := uuid.New()
	for _, state := range []string{"acquiring", "ready", "cleanup_eligible", "cleanup_failed"} {
		artifactID := uuid.New()
		projected := read(&persistence.SourceLocationStagedArtifact{
			ID: artifactID, State: state, RequestedSteps: []string{"sha256"}, RequestedStepsKnown: true, CreatorOperationID: &creatorID,
		}).StagedArtifact
		if projected.ID == nil || *projected.ID != artifactID || projected.State != state || len(projected.RequestedSteps) != 1 || projected.RequestedSteps[0] != "sha256" || !projected.RequestedStepsKnown || projected.CreatorOperationID == nil || *projected.CreatorOperationID != creatorID {
			t.Fatalf("%s staged artifact projection = %#v", state, projected)
		}
	}
}
