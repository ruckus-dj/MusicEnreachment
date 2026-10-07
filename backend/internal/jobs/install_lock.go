package jobs

import (
	"sync"

	"github.com/google/uuid"
)

// installExecutionLocks serialize filesystem ownership for an operation within
// this process. Durable delivery fencing remains the source of truth; this is
// not a cross-process lock.
var installExecutionLocks sync.Map // map[uuid.UUID]*sync.Mutex

func lockInstallationExecution(id uuid.UUID) func() {
	lock, _ := installExecutionLocks.LoadOrStore(id, &sync.Mutex{})
	mutex := lock.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}
