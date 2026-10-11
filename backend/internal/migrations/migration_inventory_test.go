package migrations

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

var migrationFilename = regexp.MustCompile(`^(\d+)_([a-z0-9_]+)\.tx\.(up|down)\.sql$`)

// Bun identifies migrations by the numeric prefix, not by the descriptive
// suffix. Keep the embedded inventory to one logical migration per prefix so
// Discovery cannot silently overwrite a migration.
func TestEmbeddedMigrationInventoryHasUniquePrefixesAndUpDownPairs(t *testing.T) {
	type pair struct {
		stem       string
		directions map[string]string
	}
	pairs := make(map[string]*pair)
	err := fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
			return nil
		}
		base := path.Base(name)
		matches := migrationFilename.FindStringSubmatch(base)
		if matches == nil {
			return fmt.Errorf("unexpected embedded migration filename %q", base)
		}
		prefix, stem, direction := matches[1], matches[2], matches[3]
		migration, ok := pairs[prefix]
		if !ok {
			migration = &pair{stem: stem, directions: make(map[string]string)}
			pairs[prefix] = migration
		} else if migration.stem != stem {
			return fmt.Errorf("migration prefix %s is shared by %q and %q", prefix, migration.stem, stem)
		}
		if previous, exists := migration.directions[direction]; exists {
			return fmt.Errorf("duplicate %s migration for prefix %s: %q and %q", direction, prefix, previous, base)
		}
		migration.directions[direction] = base
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) == 0 {
		t.Fatal("no embedded migrations found")
	}
	for prefix, migration := range pairs {
		if len(migration.directions) != 2 || migration.directions["up"] == "" || migration.directions["down"] == "" {
			t.Errorf("migration prefix %s (%s) must have exactly one up and one down file; got %#v", prefix, migration.stem, migration.directions)
		}
	}
}
