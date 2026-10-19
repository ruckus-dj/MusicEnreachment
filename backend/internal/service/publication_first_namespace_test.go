package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestPlanFirstPublicationNamespaceDoesNotCreateTargets(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	positionID := uuid.New()
	candidates := []publication.NamespaceCandidate{{
		RelativePath: "Музыка.flac",
		NodeKind:     "file",
		PositionID:   positionID,
	}}

	nodes, err := planFirstPublicationNamespace(context.Background(), root, positionID, candidates, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].RelativePath != candidates[0].RelativePath || nodes[0].NodeKind != "file" {
		t.Fatalf("unexpected namespace plan: %#v", nodes)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("namespace planning left output artifacts: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(root, candidates[0].RelativePath)); !os.IsNotExist(err) {
		t.Fatalf("candidate unexpectedly exists: %v", err)
	}
}
