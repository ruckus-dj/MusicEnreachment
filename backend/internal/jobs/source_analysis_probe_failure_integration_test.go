//go:build integration

package jobs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func (fixture *analysisDispatchFixture) changeSourceBytes(t *testing.T, ctx context.Context, contents string) {
	t.Helper()
	path := filepath.Join(fixture.source, filepath.FromSlash(fixture.work.RelativePath))
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("change source bytes for cache miss: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat changed source: %v", err)
	}
	mtime := info.ModTime().UTC().Truncate(time.Microsecond)
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_analysis_work SET size_bytes=?,mtime=? WHERE id=?`, info.Size(), mtime, fixture.work.ID); err != nil {
		t.Fatalf("update normalized work for changed source: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_location SET size_bytes=?,mtime=? WHERE id=?`, info.Size(), mtime, fixture.track.ID); err != nil {
		t.Fatalf("update inventory location for changed source: %v", err)
	}
	fixture.work.SizeBytes, fixture.work.Mtime = info.Size(), mtime
	fixture.track.SizeBytes, fixture.track.Mtime = info.Size(), mtime
}
