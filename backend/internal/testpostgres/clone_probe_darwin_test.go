//go:build darwin

package testpostgres

import (
	"os"
	"testing"
)

func TestProbeCloneFileCopyCleansPrivateFiles(t *testing.T) {
	workDir := t.TempDir()
	_, _, err := probeCloneFileCopy(workDir)
	if err != nil {
		t.Fatalf("probe ordinary-file clone support: %v", err)
	}
	entries, err := os.ReadDir(workDir)
	if err != nil {
		t.Fatalf("inspect clone-probe work directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("clone probe left files behind: %v", entries)
	}
}
