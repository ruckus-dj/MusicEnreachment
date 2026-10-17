package publication

import "testing"

func qualityCandidate(lossless *bool, codec string, channels *int, layout string, depth, rate, bitrate *int) QualityCandidate {
	return QualityCandidate{
		Lossless: lossless, Codec: codec, Channels: channels, Layout: layout,
		BitDepth: depth, SampleRate: rate, Bitrate: bitrate,
	}
}

func boolPointer(value bool) *bool { return &value }
func intPointer(value int) *int    { return &value }

func losslessCandidate(depth, rate int) QualityCandidate {
	return qualityCandidate(boolPointer(true), "flac", intPointer(2), "stereo", intPointer(depth), intPointer(rate), nil)
}

func lossyCandidate(codec string, bitrate int) QualityCandidate {
	return qualityCandidate(boolPointer(false), codec, intPointer(2), "stereo", nil, nil, intPointer(bitrate))
}

func TestCompareQuality(t *testing.T) {
	tests := []struct {
		name string
		a, b QualityCandidate
		want QualityComparison
	}{
		{"lossless over confirmed lossy", losslessCandidate(16, 44100), lossyCandidate("mp3", 320), QualityBetter},
		{"confirmed lossy below lossless", lossyCandidate("mp3", 320), losslessCandidate(16, 44100), QualityWorse},
		{"lossless bit depth ranks first", losslessCandidate(24, 44100), losslessCandidate(16, 96000), QualityBetter},
		{"lossless sample rate breaks depth tie", losslessCandidate(16, 96000), losslessCandidate(16, 44100), QualityBetter},
		{"same lossless facts equal", losslessCandidate(24, 96000), losslessCandidate(24, 96000), QualityEqual},
		{"same lossy codec bitrate ranks", lossyCandidate("mp3", 320), lossyCandidate("mp3", 192), QualityBetter},
		{"different lossy codecs incomparable", lossyCandidate("mp3", 320), lossyCandidate("aac", 256), QualityIncomparable},
		{"different channel counts incomparable", withChannels(losslessCandidate(24, 96000), 1, "mono"), losslessCandidate(16, 44100), QualityIncomparable},
		{"different layouts incomparable", withChannels(losslessCandidate(24, 96000), 2, "2.0"), losslessCandidate(16, 44100), QualityIncomparable},
		{"unknown channel data", qualityCandidate(boolPointer(true), "flac", nil, "", intPointer(24), intPointer(96000), nil), losslessCandidate(16, 44100), QualityUnknown},
		{"unknown lossless flag", qualityCandidate(nil, "flac", intPointer(2), "stereo", intPointer(24), intPointer(96000), nil), lossyCandidate("mp3", 320), QualityUnknown},
		{"unknown lossless bit depth", qualityCandidate(boolPointer(true), "flac", intPointer(2), "stereo", nil, intPointer(96000), nil), losslessCandidate(16, 44100), QualityUnknown},
		{"unknown lossless sample rate", qualityCandidate(boolPointer(true), "flac", intPointer(2), "stereo", intPointer(16), nil, nil), losslessCandidate(16, 44100), QualityUnknown},
		{"unknown lossy codec", qualityCandidate(boolPointer(false), "", intPointer(2), "stereo", nil, nil, intPointer(320)), lossyCandidate("mp3", 192), QualityUnknown},
		{"unknown lossy bitrate", qualityCandidate(boolPointer(false), "mp3", intPointer(2), "stereo", nil, nil, nil), lossyCandidate("mp3", 192), QualityUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CompareQuality(test.a, test.b); got != test.want {
				t.Fatalf("CompareQuality() = %v, want %v", got, test.want)
			}
		})
	}
}

func withChannels(candidate QualityCandidate, channels int, layout string) QualityCandidate {
	candidate.Channels = intPointer(channels)
	candidate.Layout = layout
	return candidate
}

func TestCompareQualityIsAntisymmetric(t *testing.T) {
	candidates := []QualityCandidate{
		losslessCandidate(16, 44100),
		losslessCandidate(24, 44100),
		lossyCandidate("mp3", 320),
		lossyCandidate("aac", 256),
		qualityCandidate(nil, "flac", intPointer(2), "stereo", nil, nil, nil),
		withChannels(losslessCandidate(24, 96000), 1, "mono"),
	}
	for i, a := range candidates {
		for j, b := range candidates {
			forward := CompareQuality(a, b)
			reverse := CompareQuality(b, a)
			if reverse != reverseComparison(forward) {
				t.Errorf("comparison [%d,%d] = %v, reverse = %v", i, j, forward, reverse)
			}
		}
	}
}

func TestCompareQualityDoesNotInferThroughUnknown(t *testing.T) {
	lossless := losslessCandidate(24, 96000)
	unknown := qualityCandidate(nil, "", intPointer(2), "stereo", nil, nil, nil)
	lossy := lossyCandidate("mp3", 320)
	if got := CompareQuality(lossless, unknown); got != QualityUnknown {
		t.Fatalf("lossless vs unknown = %v, want unknown", got)
	}
	if got := CompareQuality(unknown, lossy); got != QualityUnknown {
		t.Fatalf("unknown vs lossy = %v, want unknown", got)
	}
	if got := CompareQuality(lossless, lossy); got != QualityBetter {
		t.Fatalf("lossless vs lossy = %v, want better", got)
	}
	selection := SelectQuality([]QualityCandidate{lossless, unknown, lossy}, nil)
	if selection.Candidate != nil || !selection.RequiresManual {
		t.Fatalf("selection = %#v, want manual selection", selection)
	}
}

func reverseComparison(comparison QualityComparison) QualityComparison {
	switch comparison {
	case QualityBetter:
		return QualityWorse
	case QualityWorse:
		return QualityBetter
	default:
		return comparison
	}
}

func TestSelectQuality(t *testing.T) {
	unknown := qualityCandidate(nil, "", intPointer(2), "stereo", nil, nil, nil)
	base := losslessCandidate(16, 44100)
	higher := losslessCandidate(24, 96000)
	mp3 := lossyCandidate("mp3", 320)
	aac := lossyCandidate("aac", 256)

	tests := []struct {
		name            string
		candidates      []QualityCandidate
		current         *QualityCandidate
		wantVariant     string
		wantManual      bool
		wantReplacement bool
	}{
		{name: "single unknown initial candidate is allowed", candidates: []QualityCandidate{identified(unknown, "v1", "l1", 0)}, wantVariant: "v1"},
		{name: "multiple incomparable candidates require manual choice", candidates: []QualityCandidate{identified(mp3, "v1", "l1", 0), identified(aac, "v2", "l2", 0)}, wantManual: true},
		{name: "equal candidates use stable technical identity order", candidates: []QualityCandidate{identified(base, "v2", "l2", 0), identified(losslessCandidate(16, 44100), "v1", "l9", 1)}, wantVariant: "v1"},
		{name: "unique proven maximum wins", candidates: []QualityCandidate{identified(base, "v1", "l1", 0), identified(higher, "v2", "l2", 0), identified(losslessCandidate(16, 32000), "v3", "l3", 0)}, wantVariant: "v2"},
		{name: "unknown pair requires manual choice", candidates: []QualityCandidate{identified(base, "v1", "l1", 0), identified(unknown, "v2", "l2", 0)}, wantManual: true},
		{name: "proven candidate cannot hide incomparable candidate", candidates: []QualityCandidate{identified(mp3, "v1", "l1", 0), identified(lossyCandidate("mp3", 192), "v2", "l2", 0), identified(aac, "v3", "l3", 0)}, wantManual: true},
		{name: "better candidate replaces current", candidates: []QualityCandidate{identified(higher, "v2", "l2", 0)}, current: ptrCandidate(identified(base, "v1", "l1", 0)), wantVariant: "v2", wantReplacement: true},
		{name: "equal candidate keeps current", candidates: []QualityCandidate{identified(losslessCandidate(16, 44100), "v2", "l2", 0)}, current: ptrCandidate(identified(base, "v1", "l1", 0)), wantVariant: "v1"},
		{name: "worse candidate keeps current", candidates: []QualityCandidate{identified(aac, "v2", "l2", 0)}, current: ptrCandidate(identified(base, "v1", "l1", 0)), wantVariant: "v1"},
		{name: "incomparable candidate keeps current", candidates: []QualityCandidate{identified(aac, "v2", "l2", 0)}, current: ptrCandidate(identified(mp3, "v1", "l1", 0)), wantVariant: "v1"},
		{name: "unknown candidate keeps current", candidates: []QualityCandidate{identified(unknown, "v2", "l2", 0)}, current: ptrCandidate(identified(base, "v1", "l1", 0)), wantVariant: "v1"},
		{name: "no candidates retains current", current: ptrCandidate(identified(base, "v1", "l1", 0)), wantVariant: "v1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := SelectQuality(test.candidates, test.current)
			if got.RequiresManual != test.wantManual {
				t.Fatalf("RequiresManual = %t, want %t", got.RequiresManual, test.wantManual)
			}
			if got.ReplacesCurrent != test.wantReplacement {
				t.Fatalf("ReplacesCurrent = %t, want %t", got.ReplacesCurrent, test.wantReplacement)
			}
			if test.wantVariant == "" {
				if got.Candidate != nil {
					t.Fatalf("Candidate = %#v, want nil", got.Candidate)
				}
				return
			}
			if got.Candidate == nil || got.Candidate.VariantUUID != test.wantVariant {
				t.Fatalf("Candidate = %#v, want variant %q", got.Candidate, test.wantVariant)
			}
		})
	}
}

func identified(candidate QualityCandidate, variant, location string, stream int) QualityCandidate {
	candidate.VariantUUID = variant
	candidate.LocationUUID = location
	candidate.StreamIndex = stream
	return candidate
}

func ptrCandidate(candidate QualityCandidate) *QualityCandidate { return &candidate }

func TestSelectQualityUsesLocationAndStreamTieBreakers(t *testing.T) {
	candidate := identified(losslessCandidate(16, 44100), "same", "loc-b", 8)
	selected := identified(losslessCandidate(16, 44100), "same", "loc-a", 9)
	got := SelectQuality([]QualityCandidate{candidate, selected}, nil)
	if got.Candidate == nil || got.Candidate.LocationUUID != "loc-a" {
		t.Fatalf("selected candidate = %#v, want location loc-a", got.Candidate)
	}

	candidate.LocationUUID = "same-location"
	selected.LocationUUID = "same-location"
	candidate.StreamIndex = 2
	selected.StreamIndex = 1
	got = SelectQuality([]QualityCandidate{candidate, selected}, nil)
	if got.Candidate == nil || got.Candidate.StreamIndex != 1 {
		t.Fatalf("selected candidate = %#v, want stream index 1", got.Candidate)
	}
}
