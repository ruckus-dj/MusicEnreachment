package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
)

// initialOutputCopyTamperingDirectory preserves OutputDirectory's native
// capabilities while interposing on the fixed output's read-back handle.
type initialOutputCopyTamperingDirectory struct {
	sourcefs.OutputDirectory
	mutation  sourcefs.OutputMutation
	directory string
	name      string
	contents  []byte
	tampered  bool
}

var _ sourcefs.OutputMutation = (*initialOutputCopyTamperingDirectory)(nil)

func (directory *initialOutputCopyTamperingDirectory) CreateExclusive(ctx context.Context, name string) (sourcefs.OutputFile, error) {
	directory.name = name
	return directory.OutputDirectory.CreateExclusive(ctx, name)
}

func (directory *initialOutputCopyTamperingDirectory) RenameNoReplace(ctx context.Context, sourceParent sourcefs.OutputDirectory, sourceName string, targetParent sourcefs.OutputDirectory, targetName string) error {
	return directory.mutation.RenameNoReplace(ctx, sourceParent, sourceName, targetParent, targetName)
}

func (directory *initialOutputCopyTamperingDirectory) OpenExistingRegular(ctx context.Context, name string) (sourcefs.RegularFile, error) {
	file, err := directory.mutation.OpenExistingRegular(ctx, name)
	if err != nil || name != directory.name || directory.tampered {
		return file, err
	}
	return &initialOutputCopyTamperingFile{
		RegularFile: file,
		tamper: func() error {
			directory.tampered = true
			// Retain the opened inode and place a same-bytes replacement at the
			// actual name passed to CreateExclusive, not the planned output path.
			retained := filepath.Join(directory.directory, name+".retained")
			if err := os.Rename(filepath.Join(directory.directory, name), retained); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(directory.directory, name), directory.contents, 0o600)
		},
	}, nil
}

type initialOutputCopyTamperingFile struct {
	sourcefs.RegularFile
	tamper func() error
}

func (file *initialOutputCopyTamperingFile) Borrow(ctx context.Context, callback func(*os.File) error) error {
	return file.RegularFile.Borrow(ctx, func(native *os.File) error {
		if err := file.tamper(); err != nil {
			return err
		}
		return callback(native)
	})
}

func TestInitialOutputCopyRejectsOutputReplacementDuringReadback(t *testing.T) {
	fixture, stage, expected := preparedManualLyricsFLACStage(t)
	mutation, ok := stage.directory.(sourcefs.OutputMutation)
	if !ok {
		t.Fatal("prepared stage directory does not support output mutation")
	}
	// The fixture output root plus the private directory component identify the
	// pinned directory. The final filename comes only from CreateExclusive.
	nativeDirectory := filepath.Join(fixture.output, stage.directoryName)
	wrapped := &initialOutputCopyTamperingDirectory{
		OutputDirectory: stage.directory,
		mutation:        mutation,
		directory:       nativeDirectory,
		contents:        expected,
	}
	stage.directory = wrapped

	scope, reservation := initialOutputCopyScope(t, fixture.guard)
	proof, err := stage.initialOutputCopy(context.Background(), scope, reservation)
	if err == nil {
		t.Fatal("expected output namespace replacement to reject the copy")
	}
	if proof != nil {
		t.Fatal("tampered output unexpectedly produced a proof")
	}
	if !wrapped.tampered || wrapped.name == "" {
		t.Fatal("read-back replacement did not use the captured created output name")
	}
	if !scope.unknown {
		t.Fatal("failed output copy did not quarantine the execution scope")
	}
	actual := filepath.Join(nativeDirectory, wrapped.name)
	retained := actual + ".retained"
	for _, path := range []string{actual, retained} {
		if _, statErr := os.Lstat(path); statErr != nil {
			t.Fatalf("expected tampering artifact %q to remain: %v", path, statErr)
		}
	}
	replacement, err := os.ReadFile(actual)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(replacement, expected) {
		t.Fatal("same-bytes replacement was modified")
	}
	retainedInfo, err := os.Stat(retained)
	if err != nil {
		t.Fatal(err)
	}
	replacementInfo, err := os.Stat(actual)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(retainedInfo, replacementInfo) {
		t.Fatal("replacement did not use a distinct file identity")
	}
	current, err := os.ReadFile(filepath.Join(fixture.output, "current.flac"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(current, expected) {
		t.Fatal("current input was modified")
	}
}
