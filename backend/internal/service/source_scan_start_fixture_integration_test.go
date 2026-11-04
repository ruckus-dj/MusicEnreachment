//go:build integration

package service_test

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
)

func holdSourceRootDeletion(ctx context.Context, database *bun.DB, rootID uuid.UUID, locked chan struct{}, release chan struct{}) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, "LOCK TABLE operation IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		_ = transaction.Rollback()
		return err
	}
	result, err := transaction.NewDelete().Model((*persistence.SourceRoot)(nil)).Where("id = ?", rootID).Exec(ctx)
	if err != nil {
		_ = transaction.Rollback()
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		_ = transaction.Rollback()
		return errors.New("the root to delete does not exist")
	}
	close(locked)
	<-release
	return transaction.Commit()
}
