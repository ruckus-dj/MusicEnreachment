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

func TestFixtureDatabaseCreateQueryCloneStrategyPolicy(t *testing.T) {
	tests := []struct {
		name, template string
		clone          bool
		want           string
	}{
		{name: "untemplated databases do not use clone", want: `CREATE DATABASE "fixture"`},
		{name: "ordinary template copy", template: "template", want: `CREATE DATABASE "fixture" TEMPLATE "template"`},
		{name: "clone template copy", template: "template", clone: true, want: `CREATE DATABASE "fixture" TEMPLATE "template" STRATEGY FILE_COPY`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := fixtureDatabaseCreateQuery("fixture", test.template, test.clone); got != test.want {
				t.Fatalf("fixture create query = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRunFixtureCreateSessionCloneLifecycle(t *testing.T) {
	setErr := errors.New("set failed")
	createErr := errors.New("create failed")
	resetErr := errors.New("reset failed")
	tests := []struct {
		name      string
		setErr    error
		createErr error
		resetErr  error
		wantCalls string
		wantErr   error
		created   bool
	}{
		{name: "success resets before return", wantCalls: "scer", created: true},
		{name: "set failure discards without create", setErr: setErr, wantCalls: "sd", wantErr: setErr},
		{name: "create failure still resets without retry", createErr: createErr, wantCalls: "scr", wantErr: createErr},
		{name: "reset failure discards after registration", resetErr: resetErr, wantCalls: "scerd", wantErr: resetErr, created: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls string
			created := false
			err := runFixtureCreateSession(true,
				func() error { calls += "s"; return test.setErr },
				func() error { calls += "c"; return test.createErr },
				func() error { calls += "r"; return test.resetErr },
				func() error { calls += "d"; return nil },
				func() { calls += "e"; created = true },
			)
			if calls != test.wantCalls {
				t.Fatalf("session calls = %q, want %q", calls, test.wantCalls)
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("session error = %v, want %v", err, test.wantErr)
			}
			if created != test.created {
				t.Fatalf("database registered = %v, want %v", created, test.created)
			}
		})
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
