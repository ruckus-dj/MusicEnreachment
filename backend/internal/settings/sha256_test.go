package settings_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func TestSHA256EnabledDefaultsToTrueWithoutRow(t *testing.T) {
	store := newMemoryStore()
	registry := settings.New(store, nil)
	ctx := context.Background()

	enabled, err := registry.GetSHA256Enabled(ctx)
	if err != nil || !enabled {
		t.Fatalf("default SHA-256 enabled: %v, %v; want true, nil", enabled, err)
	}
	if _, exists := store.data[settings.SHA256EnabledKey]; exists {
		t.Fatal("reading the default SHA-256 flag wrote a row")
	}
}

func TestSHA256EnabledReadbackPersistsAcrossRegistryInstances(t *testing.T) {
	store := newMemoryStore()
	ctx := context.Background()

	for _, want := range []bool{false, true, false} {
		if err := settings.New(store, nil).SetSHA256Enabled(ctx, want); err != nil {
			t.Fatalf("set SHA-256 enabled %v: %v", want, err)
		}

		got, err := settings.New(store, nil).GetSHA256Enabled(ctx)
		if err != nil || got != want {
			t.Fatalf("SHA-256 enabled = %v, %v; want %v, nil", got, err, want)
		}
		if raw := store.data[settings.SHA256EnabledKey]; raw != "false" && raw != "true" {
			t.Fatalf("stored SHA-256 flag = %q, want a boolean literal", raw)
		}
	}
}

func TestSHA256EnabledRejectsInvalidStoredValue(t *testing.T) {
	store := newMemoryStore()
	store.data[settings.SHA256EnabledKey] = "not-a-bool"
	registry := settings.New(store, nil)

	enabled, err := registry.GetSHA256Enabled(context.Background())
	if err == nil {
		t.Fatal("invalid stored SHA-256 flag was accepted")
	}
	if enabled {
		t.Fatal("invalid stored SHA-256 flag returned a non-zero value")
	}
	if !strings.Contains(err.Error(), settings.SHA256EnabledKey) {
		t.Fatalf("error %q does not identify the setting", err)
	}
}

type sha256FailingStore struct {
	*memoryStore
	getErr error
	setErr error
}

func (store *sha256FailingStore) Get(ctx context.Context, key string) (string, bool, error) {
	if store.getErr != nil {
		return "", false, store.getErr
	}
	return store.memoryStore.Get(ctx, key)
}

func (store *sha256FailingStore) Set(ctx context.Context, key, value string) error {
	if store.setErr != nil {
		return store.setErr
	}
	return store.memoryStore.Set(ctx, key, value)
}

func TestSHA256EnabledPropagatesStoreErrors(t *testing.T) {
	ctx := context.Background()
	getErr := errors.New("get failed")
	setErr := errors.New("set failed")

	getStore := &sha256FailingStore{memoryStore: newMemoryStore(), getErr: getErr}
	if _, err := settings.New(getStore, nil).GetSHA256Enabled(ctx); !errors.Is(err, getErr) {
		t.Fatalf("get error = %v, want %v", err, getErr)
	}

	setStore := &sha256FailingStore{memoryStore: newMemoryStore(), setErr: setErr}
	if err := settings.New(setStore, nil).SetSHA256Enabled(ctx, true); !errors.Is(err, setErr) {
		t.Fatalf("set error = %v, want %v", err, setErr)
	}
}
