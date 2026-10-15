//go:build integration

package migrations_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestLocalAutomaticPolicyRejectsUnicodeWhitespace(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	for _, table := range []struct {
		name, columns string
	}{
		{"provider_local_artist", "provider_id,provider_artist_id,local_artist_id,method,policy_version"},
		{"provider_local_release", "provider_id,provider_release_id,local_release_id,method,policy_version"},
		{"provider_local_recording", "provider_id,provider_recording_id,local_recording_id,method,policy_version"},
		{"provider_local_track", "provider_id,provider_track_id,local_track_id,method,policy_version"},
		{"track_source", "id,track_id,media_variant_id,decision_method,policy_version"},
	} {
		t.Run(table.name, func(t *testing.T) {
			for _, policy := range []string{"\r", "\t\n\v\f\r ", "\u0085\u00a0\u2003\u2028\u3000"} {
				query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (?,?,?,'automatic',?)", table.name, table.columns)
				_, err := database.ExecContext(context.Background(), query, uuid.New(), uuid.New(), uuid.New(), policy)
				var databaseError *pgconn.PgError
				if !errors.As(err, &databaseError) || databaseError.Code != "23514" || databaseError.ConstraintName != table.name+"_check" {
					t.Fatalf("whitespace policy %q returned %v, want the policy CHECK violation (not an unrelated FK failure)", policy, err)
				}
			}
		})
	}
}
