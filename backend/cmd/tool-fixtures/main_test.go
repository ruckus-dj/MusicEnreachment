package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRunRequiresExplicitOutputAndManifestWithoutNetwork(t *testing.T) {
	err := run(t.Context(), []string{"-release", "8.0"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("run without output/manifest unexpectedly succeeded")
	}
}

func TestVerifyTailMoov(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{name: "media followed by trailing index", data: mp4Atoms("ftyp", "mdat", "moov")},
		{name: "index before media", data: mp4Atoms("ftyp", "moov", "mdat"), wantErr: true},
		{name: "missing index", data: mp4Atoms("ftyp", "mdat"), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.mp4")
			if err := os.WriteFile(path, test.data, 0o600); err != nil {
				t.Fatal(err)
			}
			err := verifyTailMoov(path)
			if (err != nil) != test.wantErr {
				t.Fatalf("verifyTailMoov() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func mp4Atoms(kinds ...string) []byte {
	var result []byte
	for _, kind := range kinds {
		result = append(result, 0, 0, 0, 8)
		result = append(result, kind...)
	}
	return result
}
