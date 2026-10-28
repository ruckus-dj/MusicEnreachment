package tools

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type testGitHubToken string

func (t testGitHubToken) ReadGitHubToken(context.Context) (string, bool, error) {
	return string(t), true, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGitHubTokenTransportOnlyAuthorizesAPIRequests(t *testing.T) {
	const secret = "test-secret-value"
	var requests []*http.Request
	client := &http.Client{
		Transport: githubTokenTransport{tokens: testGitHubToken(secret), base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests = append(requests, r)
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
		})},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) == 10 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	for _, raw := range []string{
		"https://api.github.com/user", "https://api.github.com/releases", "https://downloads.github.com/file", "https://api.github.com.evil.test/user", "http://api.github.com/user", "https://api.github.com:444/user", "https://user@api.github.com/user",
	} {
		u, _ := url.Parse(raw)
		requests = nil
		_, _ = client.Do(&http.Request{Method: http.MethodGet, URL: u, Header: make(http.Header)})
		if len(requests) != 1 {
			t.Fatalf("%s made %d requests", raw, len(requests))
		}
		want := raw == "https://api.github.com/user" || raw == "https://api.github.com/releases"
		if got := requests[0].Header.Get("Authorization"); (got == "Bearer "+secret) != want {
			t.Errorf("%s authorization = %q, want authorized=%t", raw, got, want)
		}
	}
}

func TestGitHubTokenTransportRedirectsNeverForwardCredentials(t *testing.T) {
	for _, chain := range [][]string{
		{"https://api.github.com/start", "https://api.github.com/next"},
		{"https://api.github.com/start", "https://downloads.github.com/file"},
		{"https://downloads.github.com/file", "https://api.github.com/user"},
		{"https://api.github.com/start", "https://downloads.github.com/file", "https://api.github.com/user"},
	} {
		var seen []*http.Request
		client := &http.Client{Transport: githubTokenTransport{tokens: testGitHubToken("secret"), base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			seen = append(seen, r)
			var location string
			for i, u := range chain {
				if u == r.URL.String() && i+1 < len(chain) {
					location = chain[i+1]
					break
				}
			}
			status := http.StatusOK
			h := make(http.Header)
			if location != "" {
				status = http.StatusFound
				h.Set("Location", location)
			}
			return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		})}}
		start, _ := url.Parse(chain[0])
		_, _ = client.Do(&http.Request{Method: http.MethodGet, URL: start, Header: make(http.Header)})
		if len(seen) != len(chain) {
			t.Errorf("chain %v sent %d requests", chain, len(seen))
			continue
		}
		for i, r := range seen {
			want := i == 0 && strings.EqualFold(r.URL.Hostname(), "api.github.com")
			if got := r.Header.Get("Authorization"); (got == "Bearer secret") != want {
				t.Errorf("chain %v hop %d (%s) auth=%q", chain, i, r.URL, got)
			}
		}
	}
}

func TestGitHubResponseRateLimitClassification(t *testing.T) {
	for _, tc := range []struct {
		status    int
		remaining string
		retry     string
		want      bool
	}{
		{403, "0", "3", true}, {429, "10", "3", true},
		{403, "1", "3", true}, {403, "1", "", false},
		{403, "1", "invalid", false}, {401, "0", "3", false},
	} {
		r := &http.Response{StatusCode: tc.status, Header: make(http.Header)}
		r.Header.Set("X-RateLimit-Remaining", tc.remaining)
		r.Header.Set("Retry-After", tc.retry)
		err := githubResponseError(r)
		_, isRate := err.(*GitHubRateLimitError)
		if isRate != tc.want {
			t.Errorf("status %d remaining %s classified rate=%t; want %t", tc.status, tc.remaining, isRate, tc.want)
		}
		if tc.want && strings.Contains(err.Error(), "secret") {
			t.Errorf("error leaked secret: %v", err)
		}
	}
}

func TestGitHubTokenCheckerUsesAuthenticatedUserEndpoint(t *testing.T) {
	const token = "candidate-secret"
	var seen *http.Request
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		seen = request
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}")), Request: request}, nil
	})}
	if err := NewGitHubTokenChecker(client).Check(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if seen == nil || seen.URL.String() != "https://api.github.com/user" || seen.Method != http.MethodGet || seen.Header.Get("Authorization") != "Bearer "+token {
		t.Fatalf("validation request = %#v", seen)
	}
}

func TestGitHubTokenCheckerDoesNotFollowRedirects(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		header := make(http.Header)
		header.Set("Location", "https://attacker.example/collect")
		return &http.Response{StatusCode: http.StatusFound, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
	})}
	err := NewGitHubTokenChecker(client).Check(context.Background(), "secret")
	if err == nil || calls != 1 {
		t.Fatalf("redirect validation err=%v calls=%d, want one request and rejection", err, calls)
	}
}
