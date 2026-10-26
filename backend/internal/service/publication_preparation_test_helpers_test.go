package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type simplePreparationFixture struct {
	input  persistence.PublicationInputs
	output string
	source string
}

func newSimplePreparationFixture(t *testing.T) simplePreparationFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot, output := filepath.Join(base, "source"), filepath.Join(base, "output")
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceRoot, "album", "track.flac")
	if err := os.Mkdir(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("audio source bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return simplePreparationFixture{output: output, source: source}
}

func makeRewriteFixture(t *testing.T) simplePreparationFixture {
	t.Helper()
	fixture := newSimplePreparationFixture(t)
	currentPath := filepath.Join(fixture.output, "current.mp3")
	contents := []byte("current published MP3 bytes")
	if err := os.WriteFile(currentPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	digest := sha256.Sum256(contents)
	lossless := false
	fixture.input = persistence.PublicationInputs{
		Version: persistence.PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind: "rewrite", Format: "source", Rewrite: &persistence.PublicationRewriteInput{
			CurrentGeneration: uuid.New(), RelativePath: "current.mp3",
			Ownership:  persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(digest[:])},
			AudioFacts: persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "mp3", Lossless: &lossless, Container: "mp3", SupportedSourceContainer: true},
		},
		DesiredTags: map[string][]string{"TITLE": {"Current track"}},
	}
	_, fixture.input.Tools, _ = pinnedToolFixture(t, true)
	return fixture
}

func flacFixtureWithoutPictures(contents []byte) ([]byte, error) {
	if len(contents) < 8 || !bytes.Equal(contents[:4], []byte("fLaC")) {
		return nil, errors.New("fixture is not a FLAC stream")
	}
	type metadataBlock struct {
		kind byte
		data []byte
	}
	blocks := make([]metadataBlock, 0)
	offset := 4
	lastBlock := false
	for !lastBlock {
		if len(contents)-offset < 4 {
			return nil, errors.New("fixture has truncated FLAC metadata")
		}
		header := contents[offset]
		lastBlock = header&0x80 != 0
		kind := header & 0x7f
		length := int(contents[offset+1])<<16 | int(contents[offset+2])<<8 | int(contents[offset+3])
		offset += 4
		if length > len(contents)-offset {
			return nil, errors.New("fixture has truncated FLAC metadata block")
		}
		if kind != 6 {
			blocks = append(blocks, metadataBlock{kind: kind, data: contents[offset : offset+length]})
		}
		offset += length
	}
	if len(blocks) == 0 {
		return nil, errors.New("fixture has no non-picture FLAC metadata")
	}
	result := append([]byte(nil), "fLaC"...)
	for index, block := range blocks {
		header := block.kind
		if index == len(blocks)-1 {
			header |= 0x80
		}
		length := len(block.data)
		result = append(result, header, byte(length>>16), byte(length>>8), byte(length))
		result = append(result, block.data...)
	}
	return append(result, contents[offset:]...), nil
}
