// Package artistphoto resolves artist photographs from MusicBrainz URL
// relationships to Wikimedia Commons and Wikidata.
package artistphoto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
)

const (
	commonsAPIEndpoint  = "https://commons.wikimedia.org/w/api.php"
	wikidataAPIEndpoint = "https://www.wikidata.org/w/api.php"

	requestTimeout        = 10 * time.Second
	maxResponseBytes      = 1 << 20
	maxImageBytes         = 1 << 20
	thumbnailWidth        = 330
	thumbnailHeight       = 330
	maxOriginalDimension  = 400
	maxRedirects          = 3
	maxExplicitCandidates = 2
	maxWikidataCandidates = 2
	maxAttributionRunes   = 512
)

var rasterMIMEs = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
	"image/tiff": true,
	"image/bmp":  true,
}

// downloadableMIMEs are the bounded raster formats the application is willing
// to persist and serve. TIFF and BMP remain valid Commons metadata but are not
// stored as artist tiles.
var downloadableMIMEs = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
}

var rasterExtensions = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".webp": true,
	".gif":  true,
}

// Photo is the resolved, client-safe photograph attribution. ImageURL and
// SourceURL are always HTTPS URLs on fixed Wikimedia hosts. Author and License
// contain plain text only.
type Photo struct {
	ImageURL   string `json:"image_url"`
	SourceURL  string `json:"source_url"`
	Author     string `json:"author"`
	License    string `json:"license"`
	LicenseURL string `json:"license_url,omitempty"`
}

// Resolver resolves exact MusicBrainz artist IDs to Commons photographs. It
// performs no caching; callers own any cache or persistence layer.
type Resolver struct {
	client           *http.Client
	commonsEndpoint  string
	wikidataEndpoint string
	testHosts        map[string]bool
}

// NewResolver builds a resolver from the caller's HTTP client. The client is
// cloned and bounded by an integration timeout; redirects are restricted to
// the same HTTPS origin.
func NewResolver(client *http.Client) *Resolver {
	return newResolver(client, commonsAPIEndpoint, wikidataAPIEndpoint)
}

func newResolver(client *http.Client, commonsEndpoint, wikidataEndpoint string, testHosts ...string) *Resolver {
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	clone.Timeout = requestTimeout
	resolver := &Resolver{
		client:           &clone,
		commonsEndpoint:  commonsEndpoint,
		wikidataEndpoint: wikidataEndpoint,
		testHosts:        make(map[string]bool, len(testHosts)),
	}
	for _, host := range testHosts {
		resolver.testHosts[strings.ToLower(host)] = true
	}
	previousRedirect := clone.CheckRedirect
	clone.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects || !resolver.redirectAllowed(request, via) {
			return errors.New("wikimedia redirect is not allowlisted")
		}
		if previousRedirect != nil {
			return previousRedirect(request, via)
		}
		return nil
	}
	return resolver
}

// Resolve returns the photo for an exact MusicBrainz artist MBID. An explicit
// Commons image relationship wins; otherwise the artist's Wikidata entity is
// consulted for P18, preferring preferred-rank values before normal values.
// It returns nil, nil when no acceptable photo exists.
func (r *Resolver) Resolve(ctx context.Context, provider *musicbrainz.Provider, mbid string) (*Photo, error) {
	if provider == nil {
		return nil, errors.New("artist photo provider is unavailable")
	}
	relations, err := provider.LookupArtistURLRelations(ctx, mbid)
	if err != nil {
		return nil, err
	}
	var fileTitles []string
	wikidataID := ""
	for _, relation := range relations {
		switch strings.ToLower(strings.TrimSpace(relation.Type)) {
		case "image":
			if len(fileTitles) < maxExplicitCandidates {
				if title, ok := commonsFileTitle(relation.URL); ok {
					fileTitles = append(fileTitles, title)
				}
			}
		case "wikidata":
			if wikidataID == "" {
				if id, ok := wikidataEntityID(relation.URL); ok {
					wikidataID = id
				}
			}
		}
	}
	for _, title := range fileTitles {
		photo, err := r.commonsPhoto(ctx, title)
		if err != nil {
			return nil, err
		}
		if photo != nil {
			return photo, nil
		}
	}
	if wikidataID == "" {
		return nil, nil
	}
	filenames, err := r.wikidataImageFilenames(ctx, wikidataID)
	if err != nil {
		return nil, err
	}
	for _, filename := range filenames {
		title, ok := normalizeFileTitle("File:" + filename)
		if !ok {
			continue
		}
		photo, err := r.commonsPhoto(ctx, title)
		if err != nil {
			return nil, err
		}
		if photo != nil {
			return photo, nil
		}
	}
	return nil, nil
}

// Image is one bounded provider thumbnail with its declared media type. Bytes
// are never returned from a partially read or oversized response.
type Image struct {
	Data []byte
	MIME string
}

// DownloadImage fetches one already-resolved Commons thumbnail. The URL is
// re-validated against the fixed Wikimedia hosts and the resolver's redirect
// policy, and the response is bounded by maxImageBytes.
func (r *Resolver) DownloadImage(ctx context.Context, rawURL string) (Image, error) {
	target, ok := r.allowedImageURL(rawURL)
	if !ok {
		return Image{}, errors.New("invalid wikimedia image URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return Image{}, errors.New("create wikimedia image request")
	}
	request.Header.Set("User-Agent", musicbrainz.UserAgent)
	request.Header.Set("Accept", "image/png,image/jpeg,image/webp,image/gif")
	response, err := r.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Image{}, ctxErr
		}
		return Image{}, errors.New("wikimedia image request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Image{}, fmt.Errorf("wikimedia image returned HTTP %d", response.StatusCode)
	}
	mime := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if !downloadableMIMEs[mime] {
		return Image{}, errors.New("wikimedia image returned an unexpected content type")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxImageBytes+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Image{}, ctxErr
		}
		return Image{}, errors.New("read wikimedia image response")
	}
	if len(data) == 0 || len(data) > maxImageBytes {
		return Image{}, errors.New("wikimedia image exceeds the size limit")
	}
	if err := ctx.Err(); err != nil {
		return Image{}, err
	}
	return Image{Data: data, MIME: mime}, nil
}

func (r *Resolver) allowedImageURL(raw string) (*url.URL, bool) {
	value, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || value.User != nil || value.Fragment != "" {
		return nil, false
	}
	query := value.Query()
	for key := range query {
		if strings.HasPrefix(strings.ToLower(key), "utm_") {
			query.Del(key)
		}
	}
	if len(query) != 0 || path.Clean(value.Path) != value.Path || !rasterExtensions[strings.ToLower(path.Ext(value.Path))] {
		return nil, false
	}
	value.RawQuery = ""
	if !strings.HasPrefix(value.Path, "/wikipedia/commons/") {
		return nil, false
	}
	if r.isTestHost(value) {
		if value.Scheme == "http" || value.Scheme == "https" {
			return value, true
		}
		return nil, false
	}
	if value.Scheme != "https" || (value.Port() != "" && value.Port() != "443") {
		return nil, false
	}
	switch strings.ToLower(value.Hostname()) {
	case "upload.wikimedia.org":
		return value, true
	case "thumb.wikimedia.org":
		if strings.HasPrefix(value.Path, "/wikipedia/commons/thumb/") {
			return value, true
		}
	}
	return nil, false
}

func (r *Resolver) commonsPhoto(ctx context.Context, fileTitle string) (*Photo, error) {
	title, ok := normalizeFileTitle(fileTitle)
	if !ok {
		return nil, errors.New("invalid wikimedia commons file title")
	}
	query := url.Values{}
	query.Set("action", "query")
	query.Set("format", "json")
	query.Set("formatversion", "2")
	query.Set("prop", "imageinfo")
	query.Set("iiprop", "url|size|extmetadata|mime")
	query.Set("iiurlwidth", strconv.Itoa(thumbnailWidth))
	query.Set("iiurlheight", strconv.Itoa(thumbnailHeight))
	query.Set("redirects", "1")
	query.Set("titles", title)
	var response commonsResponse
	if err := r.getJSON(ctx, r.commonsEndpoint, query, &response); err != nil {
		return nil, err
	}
	if apiErrorPresent(response.Error) {
		return nil, errors.New("wikimedia commons returned an error")
	}
	if len(response.Query.Pages) != 1 {
		return nil, errors.New("invalid wikimedia commons response")
	}
	page := response.Query.Pages[0]
	if page.Missing || len(page.ImageInfo) == 0 {
		return nil, nil
	}
	if _, ok := normalizeFileTitle(page.Title); !ok {
		return nil, errors.New("invalid wikimedia commons page title")
	}
	return photoFromCommons(page.ImageInfo[0])
}

func photoFromCommons(info commonsImageInfo) (*Photo, error) {
	if !rasterMIMEs[strings.ToLower(strings.TrimSpace(info.MIME))] {
		return nil, nil
	}
	imageURL, ok, err := commonsImageURL(info)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	sourceURL, ok := normalizeCommonsSourceURL(info.DescriptionURL)
	if !ok {
		return nil, errors.New("invalid wikimedia source URL")
	}
	license := licenseText(info.ExtMetadata)
	if license == "" {
		return nil, nil
	}
	author := authorText(info.ExtMetadata)
	if attributionRequired(info.ExtMetadata) && author == "" {
		return nil, nil
	}
	return &Photo{
		ImageURL:   imageURL,
		SourceURL:  sourceURL,
		Author:     author,
		License:    license,
		LicenseURL: normalizeLicenseURL(metadataString(info.ExtMetadata, "LicenseUrl")),
	}, nil
}

func commonsImageURL(info commonsImageInfo) (string, bool, error) {
	thumb := strings.TrimSpace(info.ThumbURL)
	if thumb != "" {
		value, ok := normalizeThumbnailURL(thumb)
		if !ok {
			return "", false, errors.New("invalid wikimedia thumbnail URL")
		}
		if !validThumbnailDimensions(info, thumb) {
			return "", false, nil
		}
		return value, true, nil
	}
	if !boundedOriginalDimensions(info.Width, info.Height) {
		return "", false, nil
	}
	value, ok := normalizeOriginalImageURL(info.URL)
	if !ok {
		return "", false, nil
	}
	return value, true, nil
}

func validThumbnailDimensions(info commonsImageInfo, thumbURL string) bool {
	if !boundedThumbnailDimensions(info.ThumbWidth, info.ThumbHeight) {
		return false
	}
	if thumbnailUnscaled(thumbURL) {
		return boundedOriginalDimensions(info.Width, info.Height)
	}
	return true
}

func boundedThumbnailDimensions(width, height int) bool {
	return width > 0 && height > 0 && width <= thumbnailWidth && height <= thumbnailHeight
}

func boundedOriginalDimensions(width, height int) bool {
	return width > 0 && height > 0 && width <= maxOriginalDimension && height <= maxOriginalDimension
}

func thumbnailUnscaled(raw string) bool {
	value, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return strings.EqualFold(value.Query().Get("utm_content"), "thumbnail_unscaled")
}

func licenseText(values map[string]metadataValue) string {
	for _, key := range []string{"LicenseShortName", "UsageTerms", "License"} {
		value := plainText(metadataString(values, key))
		if usableLicense(value) {
			return value
		}
	}
	return ""
}

func usableLicense(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" || strings.Contains(normalized, "unknown") {
		return false
	}
	switch normalized {
	case "no license", "not specified", "n/a", "none":
		return false
	default:
		return true
	}
}

func attributionRequired(values map[string]metadataValue) bool {
	switch strings.ToLower(strings.TrimSpace(metadataString(values, "AttributionRequired"))) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

func authorText(values map[string]metadataValue) string {
	for _, key := range []string{"Attribution", "Artist"} {
		value := plainText(metadataString(values, key))
		if usableAuthor(value) {
			return value
		}
	}
	return ""
}

func usableAuthor(author string) bool {
	normalized := strings.ToLower(strings.TrimSpace(author))
	if normalized == "" {
		return false
	}
	switch normalized {
	case "unknown", "unknown author", "n/a", "none":
		return false
	default:
		return true
	}
}

func (r *Resolver) wikidataImageFilenames(ctx context.Context, entityID string) ([]string, error) {
	query := url.Values{}
	query.Set("action", "wbgetclaims")
	query.Set("format", "json")
	query.Set("entity", entityID)
	query.Set("property", "P18")
	var response wikidataResponse
	if err := r.getJSON(ctx, r.wikidataEndpoint, query, &response); err != nil {
		return nil, err
	}
	if apiErrorPresent(response.Error) {
		var apiErr struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(response.Error, &apiErr) == nil && apiErr.Code == "no-such-entity" {
			return nil, nil
		}
		return nil, errors.New("wikidata returned an error")
	}
	statements, err := parseWikidataClaims(response.Claims)
	if err != nil {
		return nil, err
	}
	var preferred, normal []string
	for _, claim := range statements {
		if claim.MainSnak.SnakType != "value" || claim.MainSnak.DataValue == nil {
			continue
		}
		var filename string
		if json.Unmarshal(claim.MainSnak.DataValue.Value, &filename) != nil || !validFilename(filename) {
			continue
		}
		switch claim.Rank {
		case "preferred":
			preferred = append(preferred, filename)
		case "normal":
			normal = append(normal, filename)
		}
	}
	filenames := append(preferred, normal...)
	if len(filenames) > maxWikidataCandidates {
		filenames = filenames[:maxWikidataCandidates]
	}
	return filenames, nil
}

func parseWikidataClaims(raw json.RawMessage) ([]wikidataStatement, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err == nil {
		p18, ok := fields["P18"]
		if !ok || strings.TrimSpace(string(p18)) == "null" {
			return nil, nil
		}
		var statements []wikidataStatement
		if err := json.Unmarshal(p18, &statements); err != nil {
			return nil, errors.New("invalid wikidata claims response")
		}
		return statements, nil
	}
	var array []json.RawMessage
	if err := json.Unmarshal(raw, &array); err == nil {
		// wbgetclaims may return an empty array instead of an object when the
		// entity has no claims at all.
		return nil, nil
	}
	return nil, errors.New("invalid wikidata claims response")
}

func (r *Resolver) getJSON(ctx context.Context, endpoint string, query url.Values, target any) error {
	endpointURL, err := url.Parse(endpoint)
	if err != nil || !r.allowedRequestURL(endpointURL) {
		return errors.New("invalid wikimedia endpoint")
	}
	endpointURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL.String(), nil)
	if err != nil {
		return errors.New("create wikimedia request")
	}
	request.Header.Set("User-Agent", musicbrainz.UserAgent)
	request.Header.Set("Accept", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errors.New("wikimedia request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("wikimedia returned HTTP %d", response.StatusCode)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if contentType != "application/json" && !strings.HasSuffix(contentType, "+json") {
		return errors.New("wikimedia returned an unexpected content type")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errors.New("read wikimedia response")
	}
	if len(body) > maxResponseBytes {
		return errors.New("wikimedia response exceeds size limit")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return errors.New("invalid wikimedia response")
	}
	return nil
}

func (r *Resolver) allowedRequestURL(value *url.URL) bool {
	if value == nil || value.User != nil || value.Fragment != "" || value.RawQuery != "" {
		return false
	}
	if r.isTestHost(value) {
		return value.Scheme == "http" || value.Scheme == "https"
	}
	if value.Scheme != "https" || (value.Port() != "" && value.Port() != "443") {
		return false
	}
	switch strings.ToLower(value.Hostname()) {
	case "commons.wikimedia.org", "www.wikidata.org":
		return true
	default:
		return false
	}
}

func (r *Resolver) redirectAllowed(request *http.Request, via []*http.Request) bool {
	if request == nil || request.URL == nil || request.URL.User != nil || request.URL.Fragment != "" || len(via) == 0 || via[0].URL == nil {
		return false
	}
	if r.isTestHost(request.URL) {
		return request.URL.Scheme == "http" || request.URL.Scheme == "https"
	}
	return request.URL.Scheme == "https" &&
		(request.URL.Port() == "" || request.URL.Port() == "443") &&
		strings.EqualFold(request.URL.Hostname(), via[0].URL.Hostname())
}

func (r *Resolver) isTestHost(value *url.URL) bool {
	return value != nil && r.testHosts[strings.ToLower(value.Host)]
}

func commonsFileTitle(raw string) (string, bool) {
	value, err := url.Parse(raw)
	if err != nil || value.Scheme != "https" || !strings.EqualFold(value.Hostname(), "commons.wikimedia.org") ||
		(value.Port() != "" && value.Port() != "443") || value.User != nil || value.RawQuery != "" || value.Fragment != "" {
		return "", false
	}
	const prefix = "/wiki/File:"
	if !strings.HasPrefix(value.Path, prefix) {
		return "", false
	}
	name := strings.TrimPrefix(value.Path, prefix)
	if name == "" || strings.ContainsAny(name, "/\\") {
		return "", false
	}
	return normalizeFileTitle("File:" + name)
}

func wikidataEntityID(raw string) (string, bool) {
	value, err := url.Parse(raw)
	if err != nil || value.Scheme != "https" || !strings.EqualFold(value.Hostname(), "www.wikidata.org") ||
		(value.Port() != "" && value.Port() != "443") || value.User != nil || value.RawQuery != "" || value.Fragment != "" {
		return "", false
	}
	var id string
	switch {
	case strings.HasPrefix(value.Path, "/wiki/"):
		id = strings.TrimPrefix(value.Path, "/wiki/")
	case strings.HasPrefix(value.Path, "/entity/"):
		id = strings.TrimPrefix(value.Path, "/entity/")
	default:
		return "", false
	}
	if !validEntityID(id) {
		return "", false
	}
	return id, true
}

func validEntityID(value string) bool {
	if len(value) < 2 || len(value) > 20 || value[0] != 'Q' || value[1] == '0' {
		return false
	}
	for _, character := range value[1:] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validFilename(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 255 || value == "." || value == ".." || strings.ContainsAny(value, "/\\\x00") {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func normalizeFileTitle(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "File:") {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(value, "File:"))
	if !validFilename(name) {
		return "", false
	}
	return "File:" + name, true
}

func normalizeThumbnailURL(raw string) (string, bool) {
	value, ok := parseWikimediaImageURL(raw)
	if !ok {
		return "", false
	}
	host := strings.ToLower(value.Hostname())
	if thumbnailUnscaled(raw) {
		// Commons returns the original file URL, rather than a /thumb/ URL,
		// when the requested box is at least as large as the source image.
		switch host {
		case "upload.wikimedia.org":
			if !strings.HasPrefix(value.Path, "/wikipedia/commons/") || strings.Contains(value.Path, "/thumb/") {
				return "", false
			}
		case "thumb.wikimedia.org":
			if !strings.HasPrefix(value.Path, "/wikipedia/commons/thumb/") {
				return "", false
			}
		default:
			return "", false
		}
		return value.String(), true
	}
	switch host {
	case "upload.wikimedia.org", "thumb.wikimedia.org":
		if !strings.HasPrefix(value.Path, "/wikipedia/commons/thumb/") {
			return "", false
		}
	default:
		return "", false
	}
	return value.String(), true
}

func normalizeOriginalImageURL(raw string) (string, bool) {
	value, ok := parseWikimediaImageURL(raw)
	if !ok || !strings.EqualFold(value.Hostname(), "upload.wikimedia.org") {
		return "", false
	}
	if !strings.HasPrefix(value.Path, "/wikipedia/commons/") || strings.Contains(value.Path, "/thumb/") {
		return "", false
	}
	return value.String(), true
}

func parseWikimediaImageURL(raw string) (*url.URL, bool) {
	value, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || value.Scheme != "https" || (value.Port() != "" && value.Port() != "443") ||
		value.User != nil || value.Fragment != "" {
		return nil, false
	}
	query := value.Query()
	for key := range query {
		if strings.HasPrefix(strings.ToLower(key), "utm_") {
			query.Del(key)
		}
	}
	if len(query) != 0 {
		return nil, false
	}
	value.RawQuery = ""
	if path.Clean(value.Path) != value.Path || !rasterExtensions[strings.ToLower(path.Ext(value.Path))] {
		return nil, false
	}
	return value, true
}

func normalizeCommonsSourceURL(raw string) (string, bool) {
	value, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || value.Scheme != "https" || !strings.EqualFold(value.Hostname(), "commons.wikimedia.org") ||
		(value.Port() != "" && value.Port() != "443") || value.User != nil || value.RawQuery != "" || value.Fragment != "" {
		return "", false
	}
	const prefix = "/wiki/File:"
	if !strings.HasPrefix(value.Path, prefix) || len(value.Path) <= len(prefix) || path.Clean(value.Path) != value.Path {
		return "", false
	}
	return value.String(), true
}

func normalizeLicenseURL(raw string) string {
	value, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (value.Scheme != "https" && value.Scheme != "http") || !strings.EqualFold(value.Hostname(), "creativecommons.org") ||
		(value.Port() != "" && value.Port() != "443") || value.User != nil || value.RawQuery != "" || value.Fragment != "" {
		return ""
	}
	if !strings.HasPrefix(value.Path, "/licenses/") && !strings.HasPrefix(value.Path, "/publicdomain/") {
		return ""
	}
	value.Scheme = "https"
	return value.String()
}

func apiErrorPresent(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "{}"
}

type metadataValue struct {
	Value json.RawMessage `json:"value"`
}

func metadataString(values map[string]metadataValue, keys ...string) string {
	for _, key := range keys {
		entry, ok := values[key]
		if !ok {
			continue
		}
		var value string
		if json.Unmarshal(entry.Value, &value) != nil || strings.TrimSpace(value) == "" {
			continue
		}
		return value
	}
	return ""
}

func plainText(value string) string {
	value = html.UnescapeString(html.UnescapeString(value))
	value = stripTags(value)
	var builder strings.Builder
	for _, character := range value {
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
			builder.WriteRune(' ')
			continue
		}
		builder.WriteRune(character)
	}
	text := strings.Join(strings.Fields(builder.String()), " ")
	runes := []rune(text)
	if len(runes) > maxAttributionRunes {
		text = string(runes[:maxAttributionRunes])
	}
	return strings.TrimSpace(text)
}

func stripTags(value string) string {
	var builder strings.Builder
	inTag := false
	var quote rune
	for _, character := range value {
		if inTag {
			if quote != 0 {
				if character == quote {
					quote = 0
				}
				continue
			}
			switch character {
			case '"', '\'':
				quote = character
			case '>':
				inTag = false
				builder.WriteRune(' ')
			}
			continue
		}
		if character == '<' {
			inTag = true
			continue
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

type commonsResponse struct {
	Error json.RawMessage `json:"error"`
	Query struct {
		Pages []commonsPage `json:"pages"`
	} `json:"query"`
}

type commonsPage struct {
	Title     string             `json:"title"`
	Missing   bool               `json:"missing"`
	ImageInfo []commonsImageInfo `json:"imageinfo"`
}

type commonsImageInfo struct {
	ThumbURL       string                   `json:"thumburl"`
	URL            string                   `json:"url"`
	DescriptionURL string                   `json:"descriptionurl"`
	MIME           string                   `json:"mime"`
	Width          int                      `json:"width"`
	Height         int                      `json:"height"`
	ThumbWidth     int                      `json:"thumbwidth"`
	ThumbHeight    int                      `json:"thumbheight"`
	ExtMetadata    map[string]metadataValue `json:"extmetadata"`
}

type wikidataResponse struct {
	Error  json.RawMessage `json:"error"`
	Claims json.RawMessage `json:"claims"`
}

type wikidataStatement struct {
	Rank     string `json:"rank"`
	MainSnak struct {
		SnakType  string `json:"snaktype"`
		DataValue *struct {
			Value json.RawMessage `json:"value"`
		} `json:"datavalue"`
	} `json:"mainsnak"`
}
