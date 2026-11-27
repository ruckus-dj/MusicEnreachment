// Package coverart retrieves release cover-art metadata and original image
// bytes from Cover Art Archive.
package coverart

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	officialEndpoint = "https://coverartarchive.org"

	// maxReleaseListingBytes bounds the release JSON read into memory.
	maxReleaseListingBytes = 1 << 20
	// maxReleaseImages bounds the number of entries considered from one listing.
	maxReleaseImages = 500
)

// Client downloads release cover-art listings and original images for validated
// release MBIDs.
type Client struct {
	httpClient *http.Client
	endpoint   string
}

// Image is one approved Cover Art Archive entry for a release. ThumbnailURL is
// always derived from the configured provider endpoint rather than copied from
// the upstream response.
type Image struct {
	ID           string
	Front        bool
	Back         bool
	Types        []string
	Comment      string
	ThumbnailURL string
}

// Result describes the complete original image written to the caller's writer.
type Result struct {
	Found  bool
	MIME   string
	Size   int64
	SHA256 string
}

type releaseListing struct {
	// Images is a pointer so a missing or null field is distinguishable from an
	// explicit empty array. Only the explicit empty array means "no images".
	Images *[]releaseListingImage `json:"images"`
}

type releaseListingImage struct {
	// ID arrives as a JSON number in current listings and as a string in older
	// ones. The raw token is kept so 64-bit digits are never rounded through
	// float64.
	ID       json.RawMessage `json:"id"`
	Approved *bool           `json:"approved"`
	Front    bool            `json:"front"`
	Back     bool            `json:"back"`
	Types    []string        `json:"types"`
	Comment  string          `json:"comment"`
}

// NewClient uses the official Cover Art Archive endpoint.
func NewClient(client *http.Client) *Client {
	return newClient(client, officialEndpoint, "")
}

func newClient(client *http.Client, endpoint, testHost string) *Client {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	} else {
		clone := *client
		client = &clone
	}
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !allowedRedirect(req.URL, testHost) {
			return errors.New("cover art redirect is not allowlisted")
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		return nil
	}
	return &Client{httpClient: client, endpoint: endpoint}
}

// ListImages returns the approved images for a validated release MBID in
// upstream order. Entries with an explicitly false approval are ignored;
// entries that omit the field are retained. IDs may arrive as legacy JSON
// strings or canonical JSON numbers; both keep their exact decimal digits.
// Duplicate image IDs are skipped. A
// malformed or non-positive ID on an approved entry is a retryable error rather
// than a silent omission, so a broken nonempty listing can never be mistaken
// for a definitive empty one. A missing or null images field is rejected; an
// explicit empty array is a definitive absence. A 404 on the exact request
// yields an empty list, while a 404 reached through a redirect is a temporary
// provider error.
func (c *Client) ListImages(ctx context.Context, releaseMBID string) ([]Image, error) {
	if !validUUID(releaseMBID) {
		return nil, errors.New("invalid cover art release UUID")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/release/"+strings.ToLower(releaseMBID), nil)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, errors.New("create cover art listing request")
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, errors.New("cover art listing request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		if !definitiveNotFound(request, response) {
			return nil, errors.New("cover art listing not found after redirect")
		}
		return []Image{}, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover art provider returned status %d", response.StatusCode)
	}
	mime := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if mime != "application/json" {
		return nil, errors.New("cover art provider returned an unsupported listing type")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxReleaseListingBytes+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, errors.New("read cover art release listing")
	}
	if len(raw) > maxReleaseListingBytes {
		return nil, errors.New("cover art release listing is too large")
	}
	var listing releaseListing
	if err := json.Unmarshal(raw, &listing); err != nil {
		return nil, errors.New("invalid cover art release listing")
	}
	if listing.Images == nil {
		return nil, errors.New("cover art release listing is missing images")
	}
	if len(*listing.Images) > maxReleaseImages {
		return nil, errors.New("cover art release listing has too many images")
	}
	images := make([]Image, 0, len(*listing.Images))
	seen := make(map[string]struct{}, len(*listing.Images))
	for _, entry := range *listing.Images {
		if entry.Approved != nil && !*entry.Approved {
			continue
		}
		id, ok := listingImageID(entry.ID)
		if !ok {
			return nil, errors.New("cover art release listing has an invalid image ID")
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		// Official thumbnails are always JPEG, including for PNG originals, and
		// are addressed by a fixed, derived path. Upstream thumbnail URLs are
		// never fetched or returned.
		thumbnailURL := c.endpoint + "/release/" + strings.ToLower(releaseMBID) + "/" + id + "-250.jpg"
		images = append(images, Image{
			ID:           id,
			Front:        entry.Front,
			Back:         entry.Back,
			Types:        append([]string(nil), entry.Types...),
			Comment:      entry.Comment,
			ThumbnailURL: thumbnailURL,
		})
	}
	return images, nil
}

// DownloadFront writes the original PNG/JPEG bytes for releaseUUID to destination.
// On any error the destination may contain partial bytes and must be discarded by
// the caller. The client never creates or owns destination paths.
func (c *Client) DownloadFront(ctx context.Context, releaseUUID string, destination io.Writer) (Result, error) {
	if !validUUID(releaseUUID) {
		return Result{}, errors.New("invalid cover art release UUID")
	}
	return c.download(ctx, "/release/"+strings.ToLower(releaseUUID)+"/front", destination)
}

// DownloadImage writes the original PNG/JPEG bytes for one validated numeric
// image ID on a validated release MBID to destination. Only the fixed provider
// path is requested; callers cannot supply a URL. On any error the destination
// may contain partial bytes and must be discarded by the caller.
func (c *Client) DownloadImage(ctx context.Context, releaseMBID, imageID string, destination io.Writer) (Result, error) {
	if !validUUID(releaseMBID) {
		return Result{}, errors.New("invalid cover art release UUID")
	}
	id, ok := positiveImageID(imageID)
	if !ok {
		return Result{}, errors.New("invalid cover art image ID")
	}
	return c.download(ctx, "/release/"+strings.ToLower(releaseMBID)+"/"+id, destination)
}

func (c *Client) download(ctx context.Context, path string, destination io.Writer) (Result, error) {
	if destination == nil {
		return Result{}, errors.New("cover art destination is unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+path, nil)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Result{}, ctxErr
		}
		return Result{}, errors.New("create cover art request")
	}
	request.Header.Set("Accept", "image/png, image/jpeg")
	response, err := c.httpClient.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Result{}, ctxErr
		}
		return Result{}, errors.New("cover art request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		if !definitiveNotFound(request, response) {
			return Result{}, errors.New("cover art original not found after redirect")
		}
		return Result{Found: false}, nil
	}
	if response.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("cover art provider returned status %d", response.StatusCode)
	}
	mime := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if mime != "image/png" && mime != "image/jpeg" {
		return Result{}, errors.New("cover art provider returned unsupported MIME type")
	}
	// Inspect a small prefix before writing anything; the bytes are then streamed
	// unchanged. No image pixel buffer or unbounded in-memory response is created.
	prefix := make([]byte, 512)
	n, readErr := io.ReadFull(response.Body, prefix)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return Result{}, coverReadError(ctx, readErr)
	}
	prefix = prefix[:n]
	if sniffed := http.DetectContentType(prefix); sniffed != mime || !validImagePrefix(mime, prefix) {
		return Result{}, errors.New("cover art original is not a supported image")
	}
	hash := sha256.New()
	count, err := copyTo(ctx, destination, hash, prefix, response.Body)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return Result{Found: true, MIME: mime, Size: count, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func copyTo(ctx context.Context, dst io.Writer, hash io.Writer, prefix []byte, body io.Reader) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	written, err := io.Copy(io.MultiWriter(dst, hash), bytes.NewReader(prefix))
	if err != nil {
		return written, coverReadError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return written, err
	}
	rest, err := io.Copy(io.MultiWriter(dst, hash), contextReader{ctx: ctx, reader: body})
	written += rest
	if err != nil {
		return written, coverReadError(ctx, err)
	}
	return written, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func coverReadError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("read or write cover art original: %w", err)
}

// definitiveNotFound reports whether a 404 came from the exact request the
// client issued. Cover Art Archive may redirect originals to archive.org, and a
// 404 from such a redirect is a temporary provider error rather than an absence.
func definitiveNotFound(original *http.Request, response *http.Response) bool {
	if original == nil || original.URL == nil || response == nil || response.Request == nil || response.Request.URL == nil {
		return false
	}
	return original.URL.String() == response.Request.URL.String()
}

// listingImageID extracts an archive.org file ID from either the legacy JSON
// string form or the canonical JSON number form. json.Number keeps the exact
// decimal literal, so 64-bit IDs are never rounded through float64.
func listingImageID(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", false
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", false
		}
		return positiveImageID(value)
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return "", false
	}
	return positiveImageID(number.String())
}

// positiveImageID accepts only the canonical decimal form of a positive
// archive.org file ID. This rejects zero, signs, whitespace, path separators and
// non-canonical leading zeros while keeping deduplication unambiguous.
func positiveImageID(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != value {
		return "", false
	}
	return value, true
}

func validImagePrefix(mime string, data []byte) bool {
	if mime == "image/png" {
		return len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n"
	}
	return len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := uuid.Parse(value)
	return err == nil
}

func allowedRedirect(u *url.URL, testHost string) bool {
	if u == nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "coverartarchive.org" || host == "archive.org" || strings.HasSuffix(host, ".archive.org") {
		return u.Port() == "" || u.Port() == "443"
	}
	return testHost != "" && strings.EqualFold(u.Host, testHost)
}

// newClientWithEndpoint is deliberately package-private for httptest only.
func newClientWithEndpoint(client *http.Client, endpoint string) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid cover art test endpoint")
	}
	return newClient(client, strings.TrimRight(endpoint, "/"), u.Host), nil
}
