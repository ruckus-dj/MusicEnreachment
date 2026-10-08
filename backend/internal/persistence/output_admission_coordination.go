package persistence

import (
	"context"

	"github.com/uptrace/bun"
)

const outputAdmissionGateKey int32 = 3

// AcquireOutputAdmissionGate admits output-producing work while excluding an
// output reset. Call before taking root, installation, or operation locks.
func AcquireOutputAdmissionGate(ctx context.Context, database bun.IDB) error {
	return advisoryXactLock(ctx, database, true, toolsCoordinationNamespace, outputAdmissionGateKey)
}

// lockOutputAdmissionGateExclusive is reserved for reset coordination and tests.
func lockOutputAdmissionGateExclusive(ctx context.Context, database bun.IDB) error {
	return advisoryXactLock(ctx, database, false, toolsCoordinationNamespace, outputAdmissionGateKey)
}
