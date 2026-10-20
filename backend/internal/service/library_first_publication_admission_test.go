package service

import (
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestValidFirstPublicationPreviewDigest(t *testing.T) {
	tests := []struct {
		name   string
		digest string
		valid  bool
	}{
		{name: "lowercase sha256", digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", valid: true},
		{name: "uppercase sha256", digest: "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF", valid: true},
		{name: "short", digest: "abcd"},
		{name: "invalid character", digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdeg"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validFirstPublicationPreviewDigest(test.digest); got != test.valid {
				t.Fatalf("validFirstPublicationPreviewDigest() = %t, want %t", got, test.valid)
			}
		})
	}
}

func TestInitialLyricsAuthoritySnapshotDetectsPositionRevisionAndFieldChanges(t *testing.T) {
	base := &persistence.LibraryFirstPublicationContext{Metadata: persistence.FirstPublicationMetadata{Position: persistence.LibraryMetadata{
		Revision: 7,
		Envelope: librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{}},
	}}}
	frozen := initialLyricsAuthorityInput(base)
	if !sameInitialLyricsAuthority(base, frozen) {
		t.Fatal("unchanged initial lyrics authority did not compare equal")
	}
	changed := *base
	changed.Metadata.Position.Revision++
	if sameInitialLyricsAuthority(&changed, frozen) {
		t.Fatal("changed metadata revision was accepted")
	}
	changed = *base
	changed.Metadata.Position.Envelope = librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{
		"synced_lyrics": {State: librarymetadata.StateExplicitEmpty, Authority: librarymetadata.AuthorityManual, Provenance: librarymetadata.Provenance{Kind: librarymetadata.ProvenanceManual}},
	}}
	if sameInitialLyricsAuthority(&changed, frozen) {
		t.Fatal("newly-added manual lyrics field was accepted")
	}
}
