package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

var ErrMatchingAuthorityInvalid = errors.New("matching authority request is invalid")
var ErrMatchingAuthorityConflict = errors.New("matching authority changed")

type matchingAuthorityRepository interface {
	ReturnIncomingGroupMatchingToAutomatic(context.Context, uuid.UUID, string, int64, uuid.UUID) (persistence.IncomingGroupReturnAutomaticResult, error)
}

type MatchingReturnAutomaticResult = persistence.IncomingGroupReturnAutomaticResult

type MatchingAuthorityService struct{ repository matchingAuthorityRepository }

func NewMatchingAuthorityService(repository matchingAuthorityRepository) *MatchingAuthorityService {
	return &MatchingAuthorityService{repository: repository}
}

func (s *MatchingAuthorityService) ReturnAutomatic(ctx context.Context, groupID uuid.UUID, groupRevision string, authorityRevision int64, requestID uuid.UUID) (MatchingReturnAutomaticResult, error) {
	if s == nil || s.repository == nil || groupID == uuid.Nil || groupRevision == "" || authorityRevision < 0 || requestID == uuid.Nil {
		return MatchingReturnAutomaticResult{}, ErrMatchingAuthorityInvalid
	}
	result, err := s.repository.ReturnIncomingGroupMatchingToAutomatic(ctx, groupID, groupRevision, authorityRevision, requestID)
	return result, matchingAuthorityError(err)
}

func matchingAuthorityError(err error) error {
	if errors.Is(err, persistence.ErrIncomingMatchConflict) {
		return fmt.Errorf("%w: %v", ErrMatchingAuthorityConflict, err)
	}
	if err == nil {
		return nil
	}
	switch err.Error() {
	case "unlink matching intent is incomplete", "unlink source identities are invalid", "return automatic matching intent is incomplete":
		return fmt.Errorf("%w: %v", ErrMatchingAuthorityInvalid, err)
	default:
		return err
	}
}
