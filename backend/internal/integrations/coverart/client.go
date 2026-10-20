// Package coverart retrieves original front-cover bytes from Cover Art Archive.
package coverart

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const officialEndpoint = "https://coverartarchive.org"

// Client downloads only the original front image for a validated release MBID.
type Client struct {
	httpClient *http.Client
	endpoint   string
}

// Result describes the complete original image written to the caller's writer.
type Result struct {
	Found  bool
	MIME   string
	Size   int64
	SHA256 string
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

// DownloadFront writes the original PNG/JPEG bytes for releaseUUID to destination.
// On any error the destination may contain partial bytes and must be discarded by
// the caller. The client never creates or owns destination paths.
func (c *Client) DownloadFront(ctx context.Context, releaseUUID string, destination io.Writer) (Result, error) {
	if !validUUID(releaseUUID) {
		return Result{}, errors.New("invalid cover art release UUID")
	}
	if destination == nil {
		return Result{}, errors.New("cover art destination is unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/release/"+strings.ToLower(releaseUUID)+"/front", nil)
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
