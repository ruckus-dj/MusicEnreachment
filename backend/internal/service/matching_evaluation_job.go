package service

import (
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

type MatchingEvaluationOperationArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (MatchingEvaluationOperationArgs) Kind() string { return "matching_evaluation_v1" }

var _ river.JobArgs = MatchingEvaluationOperationArgs{}
