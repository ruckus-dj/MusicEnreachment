//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
)

func setActiveAnalysisFFmpeg(t *testing.T, ctx context.Context, database *bun.DB, installationID uuid.UUID) {
	t.Helper()
	if err := persistence.NewSettingsRepository(database).Set(ctx, settings.ActiveFFmpegInstallationKey, installationID.String()); err != nil {
		t.Fatalf("set active analysis ffmpeg installation: %v", err)
	}
}
