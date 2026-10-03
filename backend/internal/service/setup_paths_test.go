package service

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func TestValidatePathsNormalizesAndProbesWithoutSaving(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	setup := NewSetup(store, settings.New(store, nil), settings.PlatformState{}, nil, nil)
	root := t.TempDir()
	tools := filepath.Join(root, "tools")
	output := filepath.Join(root, "output")
	normalizedTools, err := settings.NormalizePath(tools)
	if err != nil {
		t.Fatal(err)
	}
	normalizedOutput, err := settings.NormalizePath(output)
	if err != nil {
		t.Fatal(err)
	}

	result, err := setup.ValidatePaths(ctx, tools, output)
	if err != nil {
		t.Fatalf("valid prospective paths rejected: %#v, %v", result, err)
	}
	if result.ToolsDirectory != normalizedTools || result.OutputDirectory != normalizedOutput ||
		result.OutputUnicodeNormalization == "" {
		t.Fatalf("normalized paths or semantics missing: %#v", result)
	}
	if len(store.data) != 0 {
		t.Fatalf("preflight saved settings: %#v", store.data)
	}
	for _, path := range []string{tools, output} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("preflight created requested directory %q: %v", path, err)
		}
	}
}

func TestValidatePathsUsesSavedPathsForOmittedFields(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	tools, output := t.TempDir(), t.TempDir()
	store.data[settings.ToolsDirectoryKey] = tools
	store.data[settings.OutputDirectoryKey] = output
	before := maps.Clone(store.data)
	setup := NewSetup(store, settings.New(store, nil), settings.PlatformState{}, nil, nil)
	normalizedTools, err := settings.NormalizePath(tools)
	if err != nil {
		t.Fatal(err)
	}
	normalizedOutput, err := settings.NormalizePath(output)
	if err != nil {
		t.Fatal(err)
	}

	result, err := setup.ValidatePaths(ctx, "", "")
	if err != nil || result.ToolsDirectory != normalizedTools || result.OutputDirectory != normalizedOutput {
		t.Fatalf("saved paths were not validated: %#v, %v", result, err)
	}
	if !maps.Equal(before, store.data) {
		t.Fatalf("preflight changed saved settings: before=%#v after=%#v", before, store.data)
	}
	for _, request := range [][2]string{{"", output}, {tools, ""}} {
		result, err := setup.ValidatePaths(ctx, request[0], request[1])
		if err != nil || result.ToolsDirectory != normalizedTools || result.OutputDirectory != normalizedOutput {
			t.Fatalf("one omitted path was not resolved: %#v, %v", result, err)
		}
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		t.Fatalf("path probes left files in output: %v, %v", entries, err)
	}
}

func TestValidatePathsRequiresMissingSavedPaths(t *testing.T) {
	store := newMemoryStore()
	setup := NewSetup(store, settings.New(store, nil), settings.PlatformState{}, nil, nil)
	if _, err := setup.ValidatePaths(context.Background(), "", ""); err == nil {
		t.Fatal("missing paths were accepted")
	}
	if len(store.data) != 0 {
		t.Fatalf("preflight saved incomplete paths: %#v", store.data)
	}
}

func TestValidatePathsRejectsOverlappingAndRelativePaths(t *testing.T) {
	store := newMemoryStore()
	setup := NewSetup(store, settings.New(store, nil), settings.PlatformState{}, nil, nil)
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "tools-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}

	_, err := setup.ValidatePaths(context.Background(), link, filepath.Join(root, "output"))
	if err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("symlink overlap was accepted: %v", err)
	}
	_, err = setup.ValidatePaths(context.Background(), "relative/tools", filepath.Join(root, "output"))
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative tools path was accepted: %v", err)
	}
	if len(store.data) != 0 {
		t.Fatalf("invalid preflight saved settings: %#v", store.data)
	}
}

func TestValidatePathsRejectsNonemptyOutputAndNonDirectoryTools(t *testing.T) {
	store := newMemoryStore()
	setup := NewSetup(store, settings.New(store, nil), settings.PlatformState{}, nil, nil)
	root, output := t.TempDir(), t.TempDir()
	toolsFile := filepath.Join(root, "tools-file")
	if err := os.WriteFile(toolsFile, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "existing.mp3"), []byte("music"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := setup.ValidatePaths(context.Background(), toolsFile, output)
	if err == nil || !strings.Contains(err.Error(), "tools directory") {
		t.Fatalf("non-directory tools path was accepted: %v", err)
	}
	_, err = setup.ValidatePaths(context.Background(), root, output)
	if err == nil || !strings.Contains(err.Error(), "not writable or empty") {
		t.Fatalf("nonempty output was accepted: %v", err)
	}
}

func TestValidatePathsAllowsExistingCompletedOutputWithoutRequiringEmpty(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	tools, output := t.TempDir(), t.TempDir()
	store.data[settings.ToolsDirectoryKey] = tools
	store.data[settings.OutputDirectoryKey] = output
	store.data[settings.SetupCompletedAtKey] = "2026-09-28T00:00:00Z"
	if err := os.WriteFile(filepath.Join(output, "published.mka"), []byte("music"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := maps.Clone(store.data)
	setup := NewSetup(store, settings.New(store, nil), settings.PlatformState{}, nil, nil)

	result, err := setup.ValidatePaths(ctx, "", "")
	if err != nil || result.OutputUnicodeNormalization == "" {
		t.Fatalf("existing completed output was rejected: %#v, %v", result, err)
	}
	if !maps.Equal(before, store.data) {
		t.Fatalf("preflight changed completed settings: %#v", store.data)
	}
}

func TestValidatePathsMissingOutputSharesExistingAncestorProbeGuard(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	setup := NewSetup(store, settings.New(store, nil), settings.PlatformState{}, nil, nil)
	tools := t.TempDir()
	parent := t.TempDir()
	missingOutput := filepath.Join(parent, "not-created", "output")

	entered, release := make(chan struct{}), make(chan struct{})
	guardDone := make(chan error, 1)
	go func() {
		guardDone <- settings.WithProbeDirectory(parent, func(string) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	if err := os.WriteFile(filepath.Join(parent, ".melotrove-semantics-user-file"), []byte("user"), 0o600); err != nil {
		t.Fatal(err)
	}

	missingDone := make(chan error, 1)
	go func() {
		_, err := setup.ValidatePaths(ctx, tools, missingOutput)
		missingDone <- err
	}()
	parentDone := make(chan error, 1)
	go func() {
		_, err := setup.ValidatePaths(ctx, tools, parent)
		parentDone <- err
	}()
	close(release)
	if err := <-guardDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-missingDone:
		if err != nil {
			t.Errorf("missing output probe should use its writable existing ancestor: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing output validation remained blocked")
	}
	select {
	case err := <-parentDone:
		if err == nil || !strings.Contains(err.Error(), "empty") {
			t.Errorf("existing non-empty output was not rejected: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("existing output validation remained blocked")
	}
	if _, err := os.Stat(missingOutput); !os.IsNotExist(err) {
		t.Errorf("path validation created missing output %q: %v", missingOutput, err)
	}
}

func TestValidatePathsMissingOutputUnderCompletedOutputUsesAncestor(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	tools, output := t.TempDir(), t.TempDir()
	store.data[settings.ToolsDirectoryKey] = tools
	store.data[settings.OutputDirectoryKey] = output
	store.data[settings.SetupCompletedAtKey] = "2026-09-28T00:00:00Z"
	if err := os.WriteFile(filepath.Join(output, "published.mka"), []byte("music"), 0o644); err != nil {
		t.Fatal(err)
	}
	setup := NewSetup(store, settings.New(store, nil), settings.PlatformState{}, nil, nil)

	entered, release := make(chan struct{}), make(chan struct{})
	var pause sync.Once
	restore := settings.SetProbeFilesystemSemanticsHook(func() {
		pause.Do(func() { close(entered); <-release })
	})
	defer restore()
	first := make(chan error, 1)
	go func() { _, err := setup.ValidatePaths(ctx, "", ""); first <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("initial validation did not reach probe hook")
	}

	// The prospective output is below the completed output, so both probes
	// resolve to the same actual directory and must share its guard.
	missing := filepath.Join(output, "absent", "output")
	second := make(chan error, 1)
	go func() { _, err := setup.ValidatePaths(ctx, tools, missing); second <- err }()
	close(release)
	for name, ch := range map[string]<-chan error{"initial": first, "missing ancestor": second} {
		select {
		case err := <-ch:
			if err != nil {
				t.Errorf("%s validation: %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s validation remained blocked", name)
		}
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("validation created missing ancestor: %v", err)
	}
}
