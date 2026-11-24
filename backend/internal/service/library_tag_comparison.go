package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type libraryTagComparisonReader interface {
	ReadPositionTagComparison(context.Context, uuid.UUID, uuid.UUID) (persistence.LibraryTagComparison, error)
}

type LibraryTagComparisons struct {
	reader libraryTagComparisonReader
}

func NewLibraryTagComparisons(reader libraryTagComparisonReader) *LibraryTagComparisons {
	return &LibraryTagComparisons{reader: reader}
}

type LibraryTagComparison struct {
	ProviderName   string
	ProviderStatus string
	ProviderTags   map[string][]string
	Overrides      map[string][]string
}

func (s *LibraryTagComparisons) Read(ctx context.Context, releaseID, positionID uuid.UUID) (LibraryTagComparison, error) {
	if s == nil || s.reader == nil {
		return LibraryTagComparison{}, errors.New("position tag comparison is unavailable")
	}
	evidence, err := s.reader.ReadPositionTagComparison(ctx, releaseID, positionID)
	if errors.Is(err, persistence.ErrLibraryPositionNotFound) {
		return LibraryTagComparison{}, fmt.Errorf("read position tag comparison: %w", ErrLibraryPositionNotFound)
	}
	if err != nil {
		return LibraryTagComparison{}, fmt.Errorf("read position tag comparison: %w", err)
	}
	return LibraryTagComparison{
		ProviderName:   evidence.Provider.Name,
		ProviderStatus: evidence.Provider.Status,
		ProviderTags:   evidence.Provider.Tags,
		Overrides:      evidence.Overrides,
	}, nil
}
