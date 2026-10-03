package settings

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestUnicodeNormalizationClassification(t *testing.T) {
	for _, test := range []struct {
		name   string
		alias  bool
		stored string
		want   string
	}{
		{name: "distinct names preserve both forms", stored: "\u00e9", want: "none"},
		{name: "aliased names stored composed", alias: true, stored: "\u00e9", want: "nfc"},
		{name: "aliased names stored decomposed", alias: true, stored: "e\u0301", want: "nfd"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyUnicodeNormalization(test.alias, test.stored); got != test.want {
				t.Fatalf("normalization = %q, want %q", got, test.want)
			}
		})
	}
}

func TestProbeOutputDirectorySerializesAliasDiscoveredAfterCreation(t *testing.T) {
	root := t.TempDir()
	semantics, err := ProbeFilesystemSemantics(root)
	if err != nil {
		t.Fatal(err)
	}
	if semantics.CaseSensitive {
		t.Skip("requires a case-insensitive filesystem")
	}
	target := filepath.Join(root, "created-by-probe")
	alias := filepath.Join(root, "CREATED-BY-PROBE")
	normalizedTarget, err := NormalizePath(target)
	if err != nil {
		t.Fatal(err)
	}
	normalizedAlias, err := NormalizePath(alias)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseProbe := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseProbe()
	var pause sync.Once
	restore := SetProbeFilesystemSemanticsHook(func() {
		pause.Do(func() { close(entered) })
		<-release
	})
	defer restore()
	first := make(chan error, 1)
	go func() { _, err := ProbeOutputDirectory(target, false); first <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first probe did not reach probe hook")
	}
	probeDirectoryLocks.Lock()
	firstLock := probeDirectoryLocks.locks[normalizedTarget]
	probeDirectoryLocks.Unlock()

	registered := make(chan string, 1)
	restoreRegistration := SetProbeDirectoryRegistrationHook(func(path string) {
		registered <- path
	})
	defer restoreRegistration()
	second := make(chan error, 1)
	go func() {
		second <- withProbeDirectory(alias, true, func(string) error { return nil })
	}()
	select {
	case path := <-registered:
		probeDirectoryLocks.Lock()
		aliasLock := probeDirectoryLocks.locks[path]
		probeDirectoryLocks.Unlock()
		if aliasLock != firstLock {
			t.Fatalf("registered alias path %q did not reuse the active probe lock for %q (normalized alias %q)", path, normalizedTarget, normalizedAlias)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("alias did not register its active probe lock")
	}
	releaseProbe()
	for label, result := range map[string]<-chan error{"target": first, "alias": second} {
		select {
		case err := <-result:
			if err != nil {
				t.Errorf("%s probe: %v", label, err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s probe remained blocked", label)
		}
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Errorf("probe artifacts remain: %v, %v", entries, err)
	}
}
