package persistence

import "testing"

func TestValidFirstPublicationPreviewDigest(t *testing.T) {
	for _, test := range []struct {
		name   string
		digest string
		valid  bool
	}{
		{name: "lowercase hex", digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", valid: true},
		{name: "uppercase hex", digest: "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF", valid: true},
		{name: "wrong length", digest: "abcd"},
		{name: "non-hex", digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdeg"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validFirstPublicationPreviewDigest(test.digest); got != test.valid {
				t.Fatalf("validFirstPublicationPreviewDigest() = %v, want %v", got, test.valid)
			}
		})
	}
}
