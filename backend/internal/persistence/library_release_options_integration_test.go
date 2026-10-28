package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestLibraryRecordingOptionsSearchTreatsLikeCharactersLiterallyAndKeepsOutOfRangeTotal(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	for _, title := range []string{"A 100% Real Song", "A _ Hidden Song", `A \ Hidden Song`, "A plain Song"} {
		if _, err := db.NewRaw(`INSERT INTO local_recording(id,title) VALUES (?,?)`, uuid.New(), title).Exec(ctx); err != nil {
			t.Fatalf("insert recording %q: %v", title, err)
		}
	}
	repository := NewLibraryRepository(db)
	for _, query := range []string{"100%", "_", `\`} {
		page, err := repository.ReadLibraryRecordingOptions(ctx, LibraryOptionPageRequest{Query: query, Limit: 10})
		if err != nil {
			t.Fatalf("search for %q: %v", query, err)
		}
		if len(page.Items) != 1 || page.Total != 1 {
			t.Errorf("search for %q returned %d rows (total %d), want one literal match", query, len(page.Items), page.Total)
		}
	}
	page, err := repository.ReadLibraryRecordingOptions(ctx, LibraryOptionPageRequest{Query: "song", Limit: 1, Offset: 99})
	if err != nil {
		t.Fatalf("read out-of-range page: %v", err)
	}
	if len(page.Items) != 0 || page.Total != 4 {
		t.Fatalf("out-of-range page has %d items and total %d, want 0 and 4", len(page.Items), page.Total)
	}
}
