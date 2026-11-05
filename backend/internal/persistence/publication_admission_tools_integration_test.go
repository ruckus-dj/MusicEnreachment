//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/uptrace/bun"
)

func publicationFFmpegVersionFixture() (json.RawMessage, string, string) {
	executableNames := tools.ExpectedExecutables(tools.PackageFFmpeg, runtime.GOOS)
	metadata := map[string]string{executableNames[0]: "ffmpeg version 7.0", executableNames[1]: "ffprobe version 7.0"}
	versions, _ := json.Marshal(metadata)
	ffmpegBanner, _ := tools.VerifiedExecutableVersion(metadata, tools.PackageFFmpeg, "ffmpeg", runtime.GOOS)
	ffprobeBanner, _ := tools.VerifiedExecutableVersion(metadata, tools.PackageFFmpeg, "ffprobe", runtime.GOOS)
	return versions, ffmpegBanner, ffprobeBanner
}

func assertNoAdmissionArtifacts(t *testing.T, ctx context.Context, db *bun.DB, request PublicationAdmission, operationID *uuid.UUID) {
	t.Helper()
	checks := []struct {
		label string
		query string
		args  []any
	}{
		{"operation", `SELECT count(*) FROM operation WHERE kind='publish_track_v1' AND input_snapshot->>'position_id'=?`, []any{request.PositionID.String()}},
		{"attempt", `SELECT count(*) FROM track_publication_attempt WHERE position_id=?`, []any{request.PositionID}},
		{"cover receipt", `SELECT count(*) FROM publication_attempt_cover_input WHERE position_id=?`, []any{request.PositionID}},
		{"reservation", `SELECT count(*) FROM publication_namespace_reservation WHERE position_id=? AND generation=?`, []any{request.PositionID, request.CandidateGeneration}},
	}
	if operationID != nil {
		checks = append(checks,
			struct {
				label, query string
				args         []any
			}{"tool hold", `SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?`, []any{*operationID}},
			struct {
				label, query string
				args         []any
			}{"River job", `SELECT count(*) FROM river_job WHERE args->>'operation_id'=?`, []any{operationID.String()}},
		)
	}
	for _, check := range checks {
		var count int
		if err := db.NewRaw(check.query, check.args...).Scan(ctx, &count); err != nil || count != 0 {
			t.Errorf("failed admission left %s rows: %d (%v)", check.label, count, err)
		}
	}
}
