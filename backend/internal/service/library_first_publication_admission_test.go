package service

import "testing"

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
