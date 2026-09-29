package settings

import "testing"

func TestUnicodeNormalizationClassification(t *testing.T) {
	for _, test := range []struct {
		name   string
		alias  bool
		stored string
		want   string
	}{
		{name: "distinct names preserve both forms", stored: "\u00e9", want: "none"},
		{name: "aliased names stored composed", alias: true, stored: "\u00e9", want: "nfc"},
		{name: "aliased names stored decomposed", alias: true, stored: "e\u0301", want: "nfd"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyUnicodeNormalization(test.alias, test.stored); got != test.want {
				t.Fatalf("normalization = %q, want %q", got, test.want)
			}
		})
	}
}
