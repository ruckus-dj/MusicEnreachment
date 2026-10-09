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

type Checker interface {
	CheckConnectivity(context.Context, string, string) CheckResult
}

func NewClient() *Client {
	return NewClientWithGate(NewPublicRateGate())
}

func NewClientWithGate(gate *PublicRateGate) *Client {
	return NewClientWithHTTPClient(NewGovernedHTTPClient(gate))
}

// NewClientWithHTTPClient lets the application share the same governed
// transport with the MusicBrainz catalogue client.
func NewClientWithHTTPClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	client := *httpClient
	client.Timeout = RequestTimeout
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || len(via) == 0 || req.URL.Scheme != "https" || req.URL.Host != via[0].URL.Host {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}
	return &Client{
		httpClient: &client,
	}
}

func (c *Client) CheckConnectivity(ctx context.Context, mode, baseURL string) CheckResult {
	endpoint := OfficialEndpoint
	if mode == "self-hosted" {
		if baseURL == "" {
			return CheckResult{Success: false, Error: "self-hosted mode requires base URL"}
		}
		parsed, err := url.Parse(baseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
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
		return CheckResult{Success: false, Error: "connection failed"}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return CheckResult{Success: false, Error: fmt.Sprintf("unexpected status: %d", resp.StatusCode)}
	}

	body := io.LimitReader(resp.Body, MaxResponseSize+1)
	data, err := io.ReadAll(body)
	if err != nil {
		return CheckResult{Success: false, Error: "failed to read response"}
	}
	if len(data) > MaxResponseSize {
		return CheckResult{Success: false, Error: "response exceeds size limit"}
	}

	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return CheckResult{Success: false, Error: "invalid response format"}
	}

	if response.ID != "5b11f4ce-a62d-471e-81fc-a69a8278c7da" {
		return CheckResult{Success: false, Error: "response missing expected fields"}
	}

	return CheckResult{Success: true}
}
