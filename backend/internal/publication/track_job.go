package publication

import (
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// TrackArgs is the source-free, durable identity for one publication delivery.
// Execution details are always reloaded from the persisted publication attempt.
type TrackArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (TrackArgs) Kind() string { return "publish_track_v1" }

var _ river.JobArgs = TrackArgs{}
