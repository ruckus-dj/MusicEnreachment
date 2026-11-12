package publication

import (
	"reflect"
	"testing"
)

func TestProjectSourceEncoderTags(t *testing.T) {
	current := map[string][]string{
		"TITLE":           {"Track"},
		"ENCODEDBY":       {"old encoder"},
		"ENCODING":        {"old settings"},
		"ENCODERSETTINGS": {"old parameters"},
		"UNRELATED":       {"keep"},
	}
	source := map[string][]string{
		"encodedby":       {"Source encoder"},
		"encoding":        {"Source encoder version"},
		"EncoderSettings": {"Source parameters"},
		"COMMENT":         {"not an encoder description"},
	}
	tests := []struct {
		name   string
		action AudioCodecAction
		want   map[string][]string
	}{
		{
			name:   "copy source descriptions",
			action: AudioCodecCopy,
			want: map[string][]string{
				"TITLE":           {"Track"},
				"ENCODEDBY":       {"Source encoder"},
				"ENCODING":        {"Source encoder version"},
				"ENCODERSETTINGS": {"Source parameters"},
				"UNRELATED":       {"keep"},
			},
		},
		{
			name:   "omit source descriptions when transcoding",
			action: AudioCodecALAC,
			want:   map[string][]string{"TITLE": {"Track"}, "UNRELATED": {"keep"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ProjectSourceEncoderTags(current, source, test.action)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ProjectSourceEncoderTags() = %#v, want %#v", got, test.want)
			}
		})
	}
	if current["ENCODEDBY"][0] != "old encoder" || source["encodedby"][0] != "Source encoder" {
		t.Fatal("projection mutated source or current tags")
	}
}
