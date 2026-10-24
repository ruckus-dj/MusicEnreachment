package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestAutomaticAudioUpgradeCurrentRequiresExactWitness(t *testing.T) {
	input := publicationAudioUpgradeFixture(t)
	w := input.AudioUpgrade.Current
	epoch, positionID := uuid.New(), uuid.New()
	current := TrackPublication{
		PositionID: positionID, Epoch: epoch, Generation: w.Generation, RelativePath: w.RelativePath,
		Ownership:      mustPublicationJSON(PublicationCurrentOwnership{Version: 1, Identity: w.Ownership}),
		ActualAudio:    mustPublicationJSON(PublicationCurrentAudio{Version: 1, Facts: w.AudioFacts}),
		ActualMetadata: mustPublicationJSON(PublicationCommitMetadata{Version: 1, Tags: w.Tags}),
		ActualAssets:   mustPublicationJSON(w.Assets),
	}
	authority := PositionSourceAuthority{PositionID: positionID, Mode: "automatic", Revision: input.AudioUpgrade.SourceAuthorityRevision,
		PublicationEpoch: epoch, PublicationGeneration: w.Generation}
	if !automaticAudioUpgradeCurrent(current, authority, input, epoch) {
		t.Fatal("exact CURRENT witness rejected")
	}

	tests := []struct {
		name   string
		change func(*TrackPublication, *PositionSourceAuthority, *PublicationInputs, *uuid.UUID)
	}{
		{"epoch", func(_ *TrackPublication, _ *PositionSourceAuthority, _ *PublicationInputs, e *uuid.UUID) {
			*e = uuid.New()
		}},
		{"generation", func(c *TrackPublication, _ *PositionSourceAuthority, _ *PublicationInputs, _ *uuid.UUID) {
			c.Generation = uuid.New()
		}},
		{"path", func(c *TrackPublication, _ *PositionSourceAuthority, _ *PublicationInputs, _ *uuid.UUID) {
			c.RelativePath += ".changed"
		}},
		{"ownership size", func(c *TrackPublication, _ *PositionSourceAuthority, _ *PublicationInputs, _ *uuid.UUID) {
			var o PublicationCurrentOwnership
			_ = json.Unmarshal(c.Ownership, &o)
			*o.Identity.SizeBytes++
			c.Ownership = mustPublicationJSON(o)
		}},
		{"ownership digest", func(c *TrackPublication, _ *PositionSourceAuthority, _ *PublicationInputs, _ *uuid.UUID) {
			var o PublicationCurrentOwnership
			_ = json.Unmarshal(c.Ownership, &o)
			o.Identity.SHA256 = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
			c.Ownership = mustPublicationJSON(o)
		}},
		{"audio facts", func(c *TrackPublication, _ *PositionSourceAuthority, _ *PublicationInputs, _ *uuid.UUID) {
			var a PublicationCurrentAudio
			_ = json.Unmarshal(c.ActualAudio, &a)
			*a.Facts.Channels++
			c.ActualAudio = mustPublicationJSON(a)
		}},
		{"tags", func(c *TrackPublication, _ *PositionSourceAuthority, _ *PublicationInputs, _ *uuid.UUID) {
			c.ActualMetadata = mustPublicationJSON(PublicationCommitMetadata{Version: 1, Tags: map[string][]string{"title": {"changed"}}})
		}},
		{"assets", func(c *TrackPublication, _ *PositionSourceAuthority, _ *PublicationInputs, _ *uuid.UUID) {
			c.ActualAssets = mustPublicationJSON(PublicationAssetOutcomes{Version: 1})
		}},
		{"source authority mode", func(_ *TrackPublication, a *PositionSourceAuthority, _ *PublicationInputs, _ *uuid.UUID) {
			a.Mode = "manual"
		}},
		{"source authority revision", func(_ *TrackPublication, a *PositionSourceAuthority, _ *PublicationInputs, _ *uuid.UUID) {
			a.Revision = uuid.New()
		}},
		{"source authority generation", func(_ *TrackPublication, a *PositionSourceAuthority, _ *PublicationInputs, _ *uuid.UUID) {
			a.PublicationGeneration = uuid.New()
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, a, i, e := current, authority, input.Clone(), epoch
			test.change(&c, &a, &i, &e)
			if automaticAudioUpgradeCurrent(c, a, i, e) {
				t.Fatal("changed CURRENT witness accepted")
			}
		})
	}
}

func TestAutomaticAudioUpgradeQualityBetterRequiresStrictCompatibleImprovement(t *testing.T) {
	base := PersistedAudioFacts{Codec: "flac", Lossless: boolPointer(true), Channels: intPointer(2), ChannelLayout: "stereo", BitDepth: intPointer(16), SampleRate: intPointer(44100)}
	better := base
	better.BitDepth, better.SampleRate = intPointer(24), intPointer(48000)
	if !automaticAudioUpgradeQualityBetter(better, base) {
		t.Fatal("higher bit depth and sample rate should be better")
	}
	for _, test := range []struct {
		name   string
		actual PersistedAudioFacts
	}{
		{"equal", base},
		{"worse", func() PersistedAudioFacts { f := base; f.BitDepth = intPointer(16); return f }()},
		{"incompatible layout", func() PersistedAudioFacts { f := better; f.ChannelLayout = "5.1"; f.Channels = intPointer(6); return f }()},
		{"unknown", func() PersistedAudioFacts { f := better; f.BitDepth = nil; return f }()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if automaticAudioUpgradeQualityBetter(test.actual, base) {
				t.Fatal("equal, worse, incompatible, or unknown quality accepted")
			}
		})
	}
}

func boolPointer(v bool) *bool { return &v }
