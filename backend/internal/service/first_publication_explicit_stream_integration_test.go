//go:build integration

package service

import (
	"context"
	"testing"

	"github.com/uptrace/bun"
)

func integrationPublicationArtifacts(t *testing.T, db *bun.DB) (int, int, int) {
	t.Helper()
	var jobs, operations, claims int
	if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='publish_track_v1'`).Scan(context.Background(), &jobs); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM operation WHERE kind='publish_track_v1'`).Scan(context.Background(), &operations); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM publication_namespace_reservation`).Scan(context.Background(), &claims); err != nil {
		t.Fatal(err)
	}
	return jobs, operations, claims
}
