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
	err error
}

func (fixture sourceLocationDetailRepositoryFixture) ReadSourceLocationDetail(context.Context, uuid.UUID, uuid.UUID) (*persistence.SourceLocationDetailSnapshot, error) {
	return nil, fixture.err
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
