package testpostgres

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestReleaseFixtureSkipsSQLForOwnedCluster(t *testing.T) {
	dropped := false
	if err := releaseFixture("fixture", true, func() error {
		dropped = true
		return nil
	}); err != nil {
		t.Fatalf("release embedded fixture: %v", err)
	}
	if dropped {
		t.Fatal("embedded fixture issued a per-database SQL drop")
	}
}

func TestReleaseFixtureDropsExternalDatabase(t *testing.T) {
	wantErr := errors.New("drop failed")
	called := false
	if err := releaseFixture("fixture", false, func() error {
		called = true
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("release external fixture error = %v, want %v", err, wantErr)
	}
	if !called {
		t.Fatal("external fixture did not issue its SQL drop")
	}
}

func TestCleanupResourcesStopsEmbeddedClusterAfterCallbacks(t *testing.T) {
	var calls []string
	firstErr := errors.New("cleanup failure")
	clear := false
	err := cleanupResources(cleanupPlan{
		resources: []string{"one", "two"},
		cleanups: []func() error{
			func() error { calls = append(calls, "first"); return firstErr },
			func() error { calls = append(calls, "second"); return nil },
		},
		ownedDisposable: true,
		drop:            func(name string) error { calls = append(calls, "drop:"+name); return nil },
		closeAdmin:      func() error { calls = append(calls, "admin"); return nil },
		stop:            func() error { calls = append(calls, "stop"); return nil },
		clear:           func() { clear = true },
	})
	if err == nil || !strings.Contains(err.Error(), firstErr.Error()) {
		t.Fatalf("cleanup error = %v, want callback failure", err)
	}
	if want := []string{"second", "first", "admin", "stop"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("cleanup order = %v, want %v", calls, want)
	}
	if !clear {
		t.Fatal("successful embedded stop did not clear registry")
	}
}

func TestCleanupResourcesRetainsRegistryWhenEmbeddedStopFails(t *testing.T) {
	stopErr := errors.New("stop failed")
	clear := false
	err := cleanupResources(cleanupPlan{
		ownedDisposable: true,
		drop:            func(string) error { t.Fatal("embedded cleanup issued SQL drop"); return nil },
		closeAdmin:      func() error { return nil },
		stop:            func() error { return stopErr },
		clear:           func() { clear = true },
	})
	if err == nil || !strings.Contains(err.Error(), stopErr.Error()) {
		t.Fatalf("cleanup error = %v, want stop failure", err)
	}
	if clear {
		t.Fatal("failed embedded stop cleared registry")
	}
}
