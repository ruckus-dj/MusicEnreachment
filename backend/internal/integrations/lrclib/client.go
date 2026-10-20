// Package lrclib provides a small HTTP client for the public LRCLIB API.
package lrclib

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	OfficialEndpoint = "https://lrclib.net"
	UserAgent        = "MeloTrove/0.1.0 (https://github.com/ruckus/MusicEnreachment)"
)

// Client makes explicit LRCLIB lookup and search requests. It never retries or
// falls back from a lookup to search.
type Client struct {
	httpClient *http.Client
	endpoint   string
}

// Metadata identifies the exact recording to look up. Duration is in exact
// seconds; the provider accepts fractional values.
type Metadata struct {
	Artist   string
	Title    string
	Album    string
	Duration float64
}

// Track is one LRCLIB result. Lyrics is populated only from valid synced lyrics;
// plain lyrics are intentionally never exposed as publishable text.
type Track struct {
	ID           int64   `json:"id"`
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	AlbumName    string  `json:"albumName"`
	Duration     float64 `json:"duration"`
	SyncedLyrics string  `json:"syncedLyrics"`
	PlainLyrics  string  `json:"plainLyrics"`
	Instrumental bool    `json:"instrumental"`
}

// LookupResult distinguishes a missing/sync-less match from an HTTP failure.
type LookupResult struct {
	Found  bool
	Lyrics string
	Track  Track
}

// SearchQuery contains only fields explicitly supplied for a user-directed
// search. Search results retain upstream ordering; the client chooses none.
type SearchQuery struct {
	Query    string
	Artist   string
	Title    string
	Album    string
	Duration int
}

// RequestError describes a failed request without retaining provider response
// text, which may contain lyrics or other sensitive payload.
type RequestError struct {
	StatusCode int
	RetryAfter *time.Time
}

func (e *RequestError) Error() string {
	if e.StatusCode == 0 {
		return "lrclib request failed"
	}
	return fmt.Sprintf("lrclib request failed with status %d", e.StatusCode)
}

// NewClient uses the official LRCLIB endpoint.
func NewClient(httpClient *http.Client) (*Client, error) {
	return NewClientWithEndpoint(httpClient, OfficialEndpoint)
}

// NewClientWithEndpoint allows tests to inject an HTTP test-server endpoint.
func NewClientWithEndpoint(httpClient *http.Client, endpoint string) (*Client, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid lrclib endpoint")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{httpClient: httpClient, endpoint: strings.TrimRight(endpoint, "/")}, nil
}

// Lookup requests one exact metadata match. A 404 or a result without synced
// lyrics is a normal absence, not an invitation to search for another version.
func (c *Client) Lookup(ctx context.Context, metadata Metadata) (LookupResult, error) {
	if math.IsNaN(metadata.Duration) || math.IsInf(metadata.Duration, 0) {
		return LookupResult{}, errors.New("invalid lrclib duration")
	}
	values := url.Values{
		"artist_name": {metadata.Artist}, "track_name": {metadata.Title},
		"album_name": {metadata.Album}, "duration": {strconv.FormatFloat(metadata.Duration, 'f', -1, 64)},
	}
	var track Track
	found, err := c.get(ctx, "/api/get", values, &track, true)
	if err != nil || !found {
		return LookupResult{}, err
	}
	lyrics, err := usableSyncedLyrics(track.SyncedLyrics)
	if err != nil {
		return LookupResult{}, errors.New("invalid lrclib synced lyrics")
	}
	if lyrics == "" || track.Instrumental {
		return LookupResult{Track: track}, nil
	}
	return LookupResult{Found: true, Lyrics: lyrics, Track: track}, nil
}

// Search returns provider candidates without selecting a winner.
func (c *Client) Search(ctx context.Context, query SearchQuery) ([]Track, error) {
	values := make(url.Values)
	if query.Query != "" {
		values.Set("q", query.Query)
	}
	if query.Artist != "" {
		values.Set("artist_name", query.Artist)
	}
	if query.Title != "" {
		values.Set("track_name", query.Title)
	}
	if query.Album != "" {
		values.Set("album_name", query.Album)
	}
	if query.Duration != 0 {
		values.Set("duration", strconv.Itoa(query.Duration))
	}
	var tracks []Track
	_, err := c.get(ctx, "/api/search", values, &tracks, false)
	return tracks, err
}

func (c *Client) get(ctx context.Context, path string, values url.Values, target any, lookup bool) (bool, error) {
	requestURL := c.endpoint + path + "?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		return false, errors.New("create lrclib request")
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		return false, errors.New("lrclib request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if lookup && resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		return false, &RequestError{StatusCode: resp.StatusCode, RetryAfter: retryAfter}
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		// The response headers arrived, but reading the body can fail because the
		// request context was canceled or its deadline expired mid-read. Preserve
		// that cause instead of reporting a generic decode failure.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		return false, errors.New("invalid lrclib response")
	}
	return true, nil
}

func parseRetryAfter(value string, now time.Time) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		// Negative delays are invalid; a non-negative delay must not overflow the
		// multiplication into time.Duration, so cap it at MaxInt64/time.Second.
		if seconds < 0 {
			return nil
		}
		const maxSeconds = int64((1<<63 - 1) / int64(time.Second))
		if seconds > maxSeconds {
			return nil
		}
		at := now.Add(time.Duration(seconds) * time.Second)
		return &at
	}
	if at, err := http.ParseTime(value); err == nil {
		return &at
	}
	return nil
}

func usableSyncedLyrics(lyrics string) (string, error) {
	if strings.TrimSpace(lyrics) == "" {
		return "", nil
	}
	timestamped := false
	for _, line := range strings.Split(lyrics, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		hasTimestamp, ok := validLyricLine(line)
		if !ok {
			return "", errors.New("invalid timestamp")
		}
		timestamped = timestamped || hasTimestamp
	}
	if !timestamped {
		return "", errors.New("missing timestamp")
	}
	// The original bytes are returned unchanged: metadata and timestamp tags are
	// preserved verbatim and never re-timed.
	return lyrics, nil
}

// ValidateSyncedLyrics validates synced LRC text without performing a lookup.
// The returned text is byte-for-byte identical to the input.
func ValidateSyncedLyrics(lyrics string) (string, error) {
	return usableSyncedLyrics(lyrics)
}

// validLyricLine reports whether line is one or more leading LRC tags (timestamps
// or metadata) optionally followed by arbitrary lyric text, and whether any of
// those tags is an actual timestamp. Once a timestamp has been parsed, an
// unrecognized bracket tag (for example "[Chorus]") is treated as literal lyric
// text and the rest of the line is left untouched.
func validLyricLine(line string) (bool, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return false, true
	}
	if line[0] != '[' {
		return false, false
	}
	hasTimestamp := false
	for len(line) > 0 && line[0] == '[' {
		close := strings.IndexByte(line, ']')
		if close < 0 {
			// An unterminated bracket is lyric text only after a timestamp.
			return hasTimestamp, hasTimestamp
		}
		tag := line[1:close]
		switch {
		case validTimestampTag(tag):
			hasTimestamp = true
		case validMetadataTag(tag):
		default:
			// Unknown bracket tag: arbitrary lyric text once leading timestamps
			// have been parsed; otherwise the line has no valid leading tag.
			return hasTimestamp, hasTimestamp
		}
		line = line[close+1:]
	}
	return hasTimestamp, true
}

// validTimestampTag accepts mm:ss, mm:ss.fff (one to three fraction digits) and
// hh:mm:ss.fff. Seconds must be below sixty, so malformed or out-of-range values
// (negative or >= 60) are rejected rather than accepted arbitrarily.
func validTimestampTag(tag string) bool {
	parts := strings.Split(tag, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return false
	}
	for _, part := range parts[:len(parts)-1] {
		if !asciiDigits(part) {
			return false
		}
	}
	whole := parts[len(parts)-1]
	if dot := strings.IndexByte(whole, '.'); dot >= 0 {
		fraction := whole[dot+1:]
		whole = whole[:dot]
		if len(fraction) < 1 || len(fraction) > 3 || !asciiDigits(fraction) {
			return false
		}
	}
	if !asciiDigits(whole) {
		return false
	}
	seconds, err := strconv.Atoi(whole)
	return err == nil && seconds < 60
}

// validMetadataTag accepts LRC metadata tags such as [ar:Artist], [ti:Title] and
// [offset:0], whose key is alphabetic.
func validMetadataTag(tag string) bool {
	key, _, ok := strings.Cut(tag, ":")
	if !ok || key == "" {
		return false
	}
	for _, r := range key {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

func asciiDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
