// Package acoustid provides the narrow HTTP adapter for AcoustID lookups.
package acoustid

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	OfficialEndpoint = "https://api.acoustid.org/v2/lookup"
	RequestTimeout   = 10 * time.Second
	MaxResponseSize  = 4 << 20
	// requestMeta is intentionally space-separated. url.Values.Encode encodes a
	// space as "+", which the AcoustID server decodes back to a space and splits
	// on whitespace (acoustid-server: meta.split()). A literal "+" here would be
	// percent-encoded as "%2B" and decoded as a plus sign, so the server would
	// receive a single unsplit token.
	requestMeta = "recordings releases releasegroups tracks"
)

var (
	ErrInvalidInput = errors.New("invalid AcoustID lookup input")
	ErrRequest      = errors.New("AcoustID lookup request failed")
	ErrResponse     = errors.New("invalid AcoustID lookup response")
)

// Waiter is implemented by the shared application-level request limiter.
type Waiter interface {
	Wait(context.Context) error
}

// Client performs one AcoustID lookup per call. It does not retry requests.
type Client struct {
	httpClient *http.Client
	endpoint   string
	waiter     Waiter
}

// LookupResult is unavailable when no application key was configured.
type LookupResult struct {
	Available bool
	Matches   []Match
}

// Match preserves the complete result payload alongside normalized ID lists.
// Score is nil only when the API omitted score; a supplied zero remains non-nil.
type Match struct {
	Score           *float64
	RecordingIDs    []string
	ReleaseIDs      []string
	ReleaseGroupIDs []string
	TrackIDs        []string
	Raw             json.RawMessage
}

// NewClient uses the official API and a bounded-timeout default HTTP client.
func NewClient(client *http.Client, waiter Waiter) *Client {
	return NewClientWithEndpoint(client, waiter, OfficialEndpoint)
}

// NewClientWithEndpoint allows an HTTP test server to exercise this adapter.
// Production callers should use NewClient and the official endpoint.
func NewClientWithEndpoint(client *http.Client, waiter Waiter, endpoint string) *Client {
	if client == nil {
		client = &http.Client{Timeout: RequestTimeout}
	}
	return &Client{httpClient: client, waiter: waiter, endpoint: endpoint}
}

// Lookup sends the application key in the form body, never in the URL.
func (c *Client) Lookup(ctx context.Context, applicationKey, fingerprint string, durationSeconds int) (LookupResult, error) {
	if durationSeconds < 0 {
		return LookupResult{}, ErrInvalidInput
	}
	if strings.TrimSpace(applicationKey) == "" {
		return LookupResult{Available: false}, nil
	}
	if !validEndpoint(c.endpoint) {
		return LookupResult{}, ErrInvalidInput
	}

	// Bound both the limiter wait and the HTTP request with one deadline so a
	// blocking limiter cannot hang the lookup. context.WithTimeout keeps the
	// caller's earlier deadline when it is sooner, so an existing caller timeout
	// is preserved rather than extended.
	timeout := c.httpClient.Timeout
	if timeout <= 0 {
		timeout = RequestTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if c.waiter != nil {
		if err := c.waiter.Wait(ctx); err != nil {
			return LookupResult{}, ErrRequest
		}
	}

	// AcoustID accepts (and prefers) gzip-compressed POST bodies, but request
	// compression is optional, so the form body is sent uncompressed and no
	// Content-Encoding is set for the request. Only the response is negotiated
	// as gzip via Accept-Encoding and decoded in readResponse.
	form := url.Values{
		"client":      {applicationKey},
		"fingerprint": {fingerprint},
		"duration":    {strconv.Itoa(durationSeconds)},
		"meta":        {requestMeta},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return LookupResult{}, ErrInvalidInput
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")

	// Do not forward a credential-bearing request through a redirect.
	transportClient := *c.httpClient
	if transportClient.Timeout == 0 {
		transportClient.Timeout = RequestTimeout
	}
	transportClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := transportClient.Do(req)
	if err != nil {
		return LookupResult{}, ErrRequest
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return LookupResult{}, ErrResponse
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(response.Header.Get("Content-Type"), ";", 2)[0]))
	if mediaType != "application/json" {
		return LookupResult{}, ErrResponse
	}
	data, err := readResponse(response)
	if err != nil {
		return LookupResult{}, ErrResponse
	}
	result, err := parseResponse(data)
	if err != nil {
		return LookupResult{}, ErrResponse
	}
	return result, nil
}

func validEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func readResponse(response *http.Response) ([]byte, error) {
	var source io.Reader = response.Body
	if strings.EqualFold(strings.TrimSpace(response.Header.Get("Content-Encoding")), "gzip") {
		reader, err := gzip.NewReader(response.Body)
		if err != nil {
			return nil, err
		}
		defer func() { _ = reader.Close() }()
		source = reader
	} else if encoding := response.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return nil, errors.New("unsupported content encoding")
	}
	data, err := io.ReadAll(io.LimitReader(source, MaxResponseSize+1))
	if err != nil || len(data) > MaxResponseSize {
		return nil, errors.New("response unavailable")
	}
	return data, nil
}

type rawResponse struct {
	Status  string          `json:"status"`
	Results json.RawMessage `json:"results"`
}

func parseResponse(data []byte) (LookupResult, error) {
	var raw rawResponse
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&raw); err != nil {
		return LookupResult{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return LookupResult{}, errors.New("trailing response data")
	}
	if raw.Status != "ok" || len(raw.Results) == 0 || string(raw.Results) == "null" {
		return LookupResult{}, errors.New("API status is not ok")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw.Results, &items); err != nil || items == nil {
		return LookupResult{}, errors.New("invalid results")
	}
	result := LookupResult{Available: true, Matches: make([]Match, 0, len(items))}
	for _, item := range items {
		match, err := parseMatch(item)
		if err != nil {
			return LookupResult{}, err
		}
		result.Matches = append(result.Matches, match)
	}
	return result, nil
}

func parseMatch(data json.RawMessage) (Match, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return Match{}, errors.New("invalid result")
	}
	match := Match{Raw: append(json.RawMessage(nil), data...)}
	if scoreRaw, exists := fields["score"]; exists {
		if bytes.Equal(bytes.TrimSpace(scoreRaw), []byte("null")) {
			return Match{}, errors.New("invalid score")
		}
		var score float64
		if err := json.Unmarshal(scoreRaw, &score); err != nil || math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return Match{}, errors.New("invalid score")
		}
		match.Score = &score
	}
	var recordings []json.RawMessage
	if value, exists := fields["recordings"]; exists && string(value) != "null" {
		if err := json.Unmarshal(value, &recordings); err != nil {
			return Match{}, errors.New("invalid recordings")
		}
	}
	for _, recording := range recordings {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(recording, &item); err != nil || item == nil {
			return Match{}, errors.New("invalid recording")
		}
		match.RecordingIDs = appendValidID(match.RecordingIDs, item["id"])
		for _, field := range []struct {
			name        string
			destination *[]string
		}{{"releases", &match.ReleaseIDs}, {"releasegroups", &match.ReleaseGroupIDs}, {"tracks", &match.TrackIDs}} {
			if err := collectNestedIDs(recording, field.name, field.destination); err != nil {
				return Match{}, errors.New("invalid recording identifiers")
			}
		}
	}
	return match, nil
}

func collectNestedIDs(data json.RawMessage, field string, destination *[]string) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	return walkIDs(value, field, destination)
}

func walkIDs(value any, field string, destination *[]string) error {
	switch value := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := value[key]
			if key == field {
				items, ok := child.([]any)
				if !ok && child != nil {
					return errors.New("identifier collection is not an array")
				}
				for _, item := range items {
					entity, ok := item.(map[string]any)
					if !ok {
						return errors.New("identifier entry is not an object")
					}
					if id, ok := entity["id"].(string); ok {
						*destination = appendValidID(*destination, json.RawMessage(strconv.Quote(id)))
					}
					if err := walkIDs(entity, field, destination); err != nil {
						return err
					}
				}
				continue
			}
			if err := walkIDs(child, field, destination); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := walkIDs(child, field, destination); err != nil {
				return err
			}
		}
	}
	return nil
}

func appendValidID(destination []string, raw json.RawMessage) []string {
	var id string
	if json.Unmarshal(raw, &id) != nil || !validMBID(id) {
		return destination
	}
	for _, existing := range destination {
		if strings.EqualFold(existing, id) {
			return destination
		}
	}
	return append(destination, id)
}

func validMBID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && len(id) == 36 && parsed != uuid.Nil && strings.EqualFold(id, parsed.String())
}
