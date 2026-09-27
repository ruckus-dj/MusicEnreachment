package migrations

import "testing"

func TestCollectionDiscoversEmbeddedMigrations(t *testing.T) {
	collection, err := Collection()
	if err != nil {
		t.Fatalf("Collection() error = %v", err)
	}
	if len(collection.Sorted()) == 0 {
		t.Fatal("Collection() discovered no migrations")
	}
}
