package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	OfficialEndpoint = "https://musicbrainz.org/ws/2"
	RequestTimeout   = 10 * time.Second
	MaxResponseSize  = 1 << 20
)

type CheckResult struct {
	Success bool
	Error   string
}

type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: RequestTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return fmt.Errorf("too many redirects")
				}
				if req.URL.Scheme != "https" {
					return fmt.Errorf("redirect to non-HTTPS URL")
				}
				return nil
			},
		},
	}
}

func (c *Client) CheckConnectivity(ctx context.Context, mode, baseURL string) CheckResult {
	endpoint := OfficialEndpoint
	if mode == "self-hosted" {
		if baseURL == "" {
			return CheckResult{Success: false, Error: "self-hosted mode requires base URL"}
		}
		parsed, err := url.Parse(baseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return CheckResult{Success: false, Error: "invalid base URL: must be HTTP or HTTPS"}
		}
		endpoint = strings.TrimSuffix(baseURL, "/")
	}

	checkURL := fmt.Sprintf("%s/artist/5b11f4ce-a62d-471e-81fc-a69a8278c7da?fmt=json", endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	if err != nil {
		return CheckResult{Success: false, Error: "failed to create request"}
	}

	req.Header.Set("User-Agent", "MeloTrove/0.1.0 (https://github.com/ruckus/MusicEnreachment)")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return CheckResult{Success: false, Error: "request timeout"}
		}
		return CheckResult{Success: false, Error: fmt.Sprintf("connection failed: %v", sanitizeError(err))}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return CheckResult{Success: false, Error: fmt.Sprintf("unexpected status: %d", resp.StatusCode)}
	}

	body := io.LimitReader(resp.Body, MaxResponseSize)
	data, err := io.ReadAll(body)
	if err != nil {
		return CheckResult{Success: false, Error: "failed to read response"}
	}

	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return CheckResult{Success: false, Error: "invalid response format"}
	}

	if response.ID == "" {
		return CheckResult{Success: false, Error: "response missing expected fields"}
	}

	return CheckResult{Success: true}
}

func sanitizeError(err error) error {
	msg := err.Error()
	if idx := strings.Index(msg, "://"); idx > 0 {
		if end := strings.Index(msg[idx:], "@"); end > 0 {
			return fmt.Errorf("connection error")
		}
	}
	return err
}
