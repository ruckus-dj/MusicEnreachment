package persistence

import (
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

const MatchingApplyOperationKind = "matching_apply_v1"

// MatchingApplyOperationArgs deliberately contains only the durable operation
// identifier. The immutable snapshot is the worker's source of truth.
type MatchingApplyOperationArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (MatchingApplyOperationArgs) Kind() string { return MatchingApplyOperationKind }

var _ river.JobArgs = MatchingApplyOperationArgs{}
