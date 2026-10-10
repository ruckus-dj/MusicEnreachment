package textsimilarity

import (
	"math"
	"testing"
)

func TestTokenSetSimilarityReferenceCases(t *testing.T) {
	tests := []struct {
		name        string
		left, right []rune
		want        float64
	}{
		{name: "both empty", want: 0},
		{name: "reordered tokens", left: []rune("fuzzy was a bear"), right: []rune("bear a was fuzzy"), want: 1},
		{name: "subset tokens", left: []rune("fuzzy was a bear"), right: []rune("fuzzy fuzzy was a bear"), want: 1},
		{name: "duplicate words", left: []rune("alpha alpha beta"), right: []rune("alpha beta"), want: 1},
		{name: "Unicode codepoints", left: []rune("кис кис"), right: []rune("кис"), want: 1},
		{name: "distinct CJK codepoints", left: []rune("猫"), right: []rune("犬"), want: 0},
		{name: "distinct emoji codepoints", left: []rune("😀"), right: []rune("😁"), want: 0},
		{name: "distinct one-character tokens", left: []rune("a"), right: []rune("b"), want: 0},
		{name: "one character change", left: []rune("abc"), right: []rune("axc"), want: 2.0 / 3},
		{name: "disjoint tokens", left: []rune("alpha"), right: []rune("omega"), want: 0.2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := TokenSetSimilarity(test.left, test.right)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(got-test.want) > 1e-12 {
				t.Fatalf("TokenSetSimilarity(%q, %q) = %.15f, want %.15f", string(test.left), string(test.right), got, test.want)
			}
		})
	}
}
