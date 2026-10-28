package tools

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type githubTokenReader interface {
	ReadGitHubToken(context.Context) (string, bool, error)
}

// GitHubTokenChecker validates a candidate token against GitHub's authenticated-user endpoint.
type GitHubTokenChecker struct{ client *http.Client }

func NewGitHubTokenChecker(client *http.Client) *GitHubTokenChecker {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	private := *client
	private.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &GitHubTokenChecker{client: &private}
}

var ErrGitHubTokenRejected = fmt.Errorf("GitHub rejected the token")

func (c *GitHubTokenChecker) Check(ctx context.Context, token string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return fmt.Errorf("create GitHub token validation request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("GitHub token validation request failed: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusUnauthorized {
		return ErrGitHubTokenRejected
	}
	if response.StatusCode != http.StatusOK {
		return githubResponseError(response)
	}
	return nil
}

// NewGitHubTokenHTTPClient returns a private client whose transport adds the
// current DB-backed token only to initial GitHub API requests.
func NewGitHubTokenHTTPClient(tokens githubTokenReader) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{Transport: githubTokenTransport{base: base, tokens: tokens}, Timeout: 15 * time.Second}
}

type GitHubRateLimitError struct{ RetryAfter string }

func (e *GitHubRateLimitError) Error() string {
	if e.RetryAfter != "" {
		return "GitHub API rate limit exceeded; " + e.RetryAfter
	}
	return "GitHub API rate limit exceeded"
}

func githubResponseError(response *http.Response) error {
	retryHeader := make(http.Header)
	retryHeader.Set("Retry-After", response.Header.Get("Retry-After"))
	secondaryLimit := githubRateLimitDiagnostic(retryHeader, time.Now()) != ""
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusForbidden && (response.Header.Get("X-RateLimit-Remaining") == "0" || secondaryLimit) {
		return &GitHubRateLimitError{RetryAfter: githubRateLimitDiagnostic(response.Header, time.Now())}
	}
	return fmt.Errorf("GitHub API request failed with status %d", response.StatusCode)
}

func githubRateLimitDiagnostic(header http.Header, now time.Time) string {
	if raw := strings.TrimSpace(header.Get("Retry-After")); raw != "" {
		if seconds, err := strconv.ParseUint(raw, 10, 32); err == nil && seconds <= 1<<31-1 {
			return "retry after " + (time.Duration(seconds) * time.Second).String()
		}
		if when, err := http.ParseTime(raw); err == nil && when.After(now) && when.Before(now.Add(366*24*time.Hour)) {
			return "retry after " + when.UTC().Format(time.RFC3339)
		}
	}
	if raw := strings.TrimSpace(header.Get("X-RateLimit-Reset")); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds >= 0 && seconds <= 253402300799 {
			when := time.Unix(seconds, 0).UTC()
			if when.After(now) {
				return "rate limit resets at " + when.Format(time.RFC3339)
			}
		}
	}
	return ""
}

type githubTokenTransport struct {
	base   http.RoundTripper
	tokens githubTokenReader
}

func (t githubTokenTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.Header = request.Header.Clone()
	copy.Header.Del("Authorization")
	if t.tokens != nil && request.Response == nil && request.Method == http.MethodGet && request.URL.Scheme == "https" && strings.EqualFold(request.URL.Hostname(), "api.github.com") && (request.URL.Port() == "" || request.URL.Port() == "443") && request.URL.User == nil {
		token, present, err := t.tokens.ReadGitHubToken(request.Context())
		if err != nil {
			return nil, fmt.Errorf("read GitHub token for API request")
		}
		if present && token != "" {
			copy.Header.Set("Authorization", "Bearer "+token)
		}
	}
	return t.base.RoundTrip(copy)
}
