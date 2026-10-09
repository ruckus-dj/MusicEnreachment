package persistence

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

const outputAdmissionGateKey int32 = 3

// AcquireOutputAdmissionGate admits output-producing work while excluding an
// output reset. Call before taking root, installation, or operation locks.
func AcquireOutputAdmissionGate(ctx context.Context, database bun.IDB) error {
	if err := advisoryXactLock(ctx, database, true, toolsCoordinationNamespace, outputAdmissionGateKey); err != nil {
		return err
	}
	var unresolved bool
	if err := database.NewRaw("SELECT EXISTS (SELECT 1 FROM output_reset_journal WHERE state <> 'finished')").Scan(ctx, &unresolved); err != nil {
		return fmt.Errorf("check unresolved output reset: %w", err)
	}
	if unresolved {
		return fmt.Errorf("output reset recovery is required")
	}
	return nil
}

// lockOutputAdmissionGateExclusive is reserved for reset coordination and tests.
func lockOutputAdmissionGateExclusive(ctx context.Context, database bun.IDB) error {
	if err := advisoryXactLock(ctx, database, false, toolsCoordinationNamespace, outputAdmissionGateKey); err != nil {
		return err
	}
	var unresolved bool
	if err := database.NewRaw("SELECT EXISTS (SELECT 1 FROM output_reset_journal WHERE state <> 'finished')").Scan(ctx, &unresolved); err != nil {
		return fmt.Errorf("check unresolved output reset: %w", err)
	}
	if unresolved {
		return fmt.Errorf("output reset recovery is required")
	}
	return nil
}

// WithExclusiveOutputAdmissionSession holds the output gate on one pinned
// connection across journal updates and filesystem callbacks.
func WithExclusiveOutputAdmissionSession(ctx context.Context, db *bun.DB, callback func(*OutputResetSession) error) (resultErr error) {
	if callback == nil {
		return fmt.Errorf("output reset callback is required")
	}
	conn, err := db.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire output reset connection: %w", err)
	}
	locked := false
	defer func() {
		if locked {
			var unlocked bool
			unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := conn.QueryRowContext(unlockCtx, "SELECT pg_advisory_unlock($1, $2)", toolsCoordinationNamespace, outputAdmissionGateKey).Scan(&unlocked)
			cancel()
			if err != nil || !unlocked {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
				if resultErr == nil {
					resultErr = fmt.Errorf("release output reset gate: %w", errors.Join(err, errors.New("session gate was not held")))
				}
			}
		}
		if err := conn.Close(); err != nil && resultErr == nil {
			resultErr = fmt.Errorf("close output reset connection: %w", err)
		}
	}()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1, $2)", toolsCoordinationNamespace, outputAdmissionGateKey); err != nil {
		return fmt.Errorf("acquire exclusive output admission gate: %w", err)
	}
	locked = true
	return callback(&OutputResetSession{conn: conn})
}
