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
