package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uploadedlobster.com/mbtypes"
	mb "go.uploadedlobster.com/musicbrainzws2"
	"golang.org/x/time/rate"
)

const providerUserAgent = "MeloTrove/0.1.0 (https://github.com/ruckus/MusicEnreachment)"

// ProviderOptions controls endpoint and optional self-hosted request spacing.
// Public requests always use the shared one-request-per-second gate.
type ProviderOptions struct {
	BaseURL                string
	PublicGate             *PublicRateGate
	SelfHostedThrottle     bool
	SelfHostedDelaySeconds float64
	HTTPClient             *http.Client
}

type Provider struct {
	client *mb.Client
	base   string
	gate   *PublicRateGate
}

// Entity is an application-owned projection. Raw preserves unknown fields and
// nested ordering; ScoreSet distinguishes an absent score from a real zero.
type Entity struct {
	ID            string          `json:"id"`
	Title         string          `json:"title,omitempty"`
	Name          string          `json:"name,omitempty"`
	Score         int             `json:"score"`
	ScoreSet      bool            `json:"score_set"`
	ArtistCredit  json.RawMessage `json:"artist_credit,omitempty"`
	Media         json.RawMessage `json:"media,omitempty"`
	Recordings    json.RawMessage `json:"recordings,omitempty"`
	ReleaseEvents json.RawMessage `json:"release_events,omitempty"`
	Labels        json.RawMessage `json:"labels,omitempty"`
	Releases      json.RawMessage `json:"releases,omitempty"`
	Raw           json.RawMessage `json:"raw"`
}

type SearchResult struct {
	Count   int       `json:"count"`
	Offset  int       `json:"offset"`
	Created time.Time `json:"created"`
	Items   []Entity  `json:"items"`
}

type LookupResult struct {
	Entity Entity `json:"entity"`
}

func NewProvider(options ProviderOptions) (*Provider, error) {
	base := strings.TrimSuffix(options.BaseURL, "/")
	if base == "" {
		base = OfficialEndpoint
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid musicbrainz base URL")
	}
	if options.SelfHostedDelaySeconds < 0 || options.SelfHostedDelaySeconds > 60 {
		return nil, errors.New("self-hosted musicbrainz delay must be between 0 and 60 seconds")
	}
	httpClient := options.HTTPClient
	gate := options.PublicGate
	if httpClient != nil && gate == nil {
		if existing, ok := httpClient.Transport.(*governedTransport); ok {
			gate = existing.gate
		}
	}
	if gate == nil {
		gate = NewPublicRateGate()
	}
	if httpClient == nil {
		httpClient = NewGovernedHTTPClient(gate)
	} else {
		copyClient := *httpClient
		baseTransport := copyClient.Transport
		if baseTransport == nil {
			baseTransport = http.DefaultTransport
		}
		if existing, ok := baseTransport.(*governedTransport); ok {
			if existing.gate == gate {
				copyClient.Transport = existing
			} else {
				copyClient.Transport = &governedTransport{base: existing.base, gate: gate}
			}
		} else {
			copyClient.Transport = &governedTransport{base: baseTransport, gate: gate}
		}
		copyClient.Timeout = RequestTimeout
		httpClient = &copyClient
	}
	if options.SelfHostedThrottle && !isPublicMusicBrainzHost(parsed.Hostname()) {
		copyClient := *httpClient
		baseTransport := copyClient.Transport
		var selfHostGate *selfHostedGate
		if options.SelfHostedDelaySeconds > 0 {
			interval := time.Duration(options.SelfHostedDelaySeconds * float64(time.Second))
			selfHostGate = &selfHostedGate{limiter: rate.NewLimiter(rate.Every(interval), 1)}
		}
		if existing, ok := baseTransport.(*governedTransport); ok {
			copyClient.Transport = &governedTransport{base: existing.base, gate: existing.gate, selfHosted: selfHostGate}
		} else {
			copyClient.Transport = &governedTransport{base: baseTransport, gate: gate, selfHosted: selfHostGate}
		}
		httpClient = &copyClient
	}
	httpClient = NewClientWithHTTPClient(httpClient).httpClient
	client := mb.NewWithHTTPClient(mb.AppInfo{Name: "MeloTrove", Version: "0.1.0", URL: "https://github.com/ruckus/MusicEnreachment"}, httpClient)
	client.SetUserAgent(providerUserAgent)
	client.SetBaseURL(base)
	return &Provider{client: client, base: base, gate: gate}, nil
}

func (p *Provider) callContext(ctx context.Context) (context.Context, context.CancelFunc, *responseCollector) {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	collector := &responseCollector{}
	return context.WithValue(ctx, requestCollectorKey{}, collector), cancel, collector
}

func (p *Provider) LookupRelease(ctx context.Context, id string) (LookupResult, error) {
	if !validMBID(id) {
		return LookupResult{}, errors.New("invalid musicbrainz release id")
	}
	ctx, cancel, collector := p.callContext(ctx)
	defer cancel()
	release, err := p.client.LookupRelease(ctx, mbtypes.MBID(id), mb.IncludesFilter{Includes: []string{"artists", "artist-credits", "recordings", "labels", "release-groups"}})
	if err != nil {
		return LookupResult{}, safeProviderError(ctx, err)
	}
	if err := validateCapturedResponse(collector); err != nil {
		return LookupResult{}, err
	}
	rawBody := collector.last()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &raw); err != nil {
		return LookupResult{}, errors.New("invalid musicbrainz release response")
	}
	if err := validateLookupEntity(raw, id, string(release.ID), release.Title); err != nil {
		return LookupResult{}, err
	}
	return LookupResult{Entity: projectEntity(raw, rawBody, string(release.ID), release.Title, "", release.Score)}, nil
}

func (p *Provider) LookupRecording(ctx context.Context, id string) (LookupResult, error) {
	if !validMBID(id) {
		return LookupResult{}, errors.New("invalid musicbrainz recording id")
	}
	ctx, cancel, collector := p.callContext(ctx)
	defer cancel()
	recording, err := p.client.LookupRecording(ctx, mbtypes.MBID(id), mb.IncludesFilter{Includes: []string{"artists", "artist-credits", "releases"}})
	if err != nil {
		return LookupResult{}, safeProviderError(ctx, err)
	}
	if err := validateCapturedResponse(collector); err != nil {
		return LookupResult{}, err
	}
	rawBody := collector.last()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &raw); err != nil {
		return LookupResult{}, errors.New("invalid musicbrainz recording response")
	}
	if err := validateLookupEntity(raw, id, string(recording.ID), recording.Title); err != nil {
		return LookupResult{}, err
	}
	return LookupResult{Entity: projectEntity(raw, rawBody, string(recording.ID), recording.Title, "", recording.Score)}, nil
}

func (p *Provider) SearchReleases(ctx context.Context, query string, offset, limit int) (SearchResult, error) {
	ctx, cancel, collector := p.callContext(ctx)
	defer cancel()
	if err := validateSearch(query, offset, limit); err != nil {
		return SearchResult{}, err
	}
	result, err := p.client.SearchReleases(ctx, mb.SearchFilter{Query: query, Includes: []string{"artists", "artist-credits", "recordings", "labels", "release-groups"}}, mb.Paginator{Offset: offset, Limit: limit})
	if err != nil {
		return SearchResult{}, safeProviderError(ctx, err)
	}
	if err := validateCapturedResponse(collector); err != nil {
		return SearchResult{}, err
	}
	projected, err := projectSearch(collector.last(), "releases", result.Count, result.Offset, result.Created, result.Releases)
	if err != nil {
		return SearchResult{}, err
	}
	return projected, nil
}

func (p *Provider) SearchRecordings(ctx context.Context, query string, offset, limit int) (SearchResult, error) {
	ctx, cancel, collector := p.callContext(ctx)
	defer cancel()
	if err := validateSearch(query, offset, limit); err != nil {
		return SearchResult{}, err
	}
	result, err := p.client.SearchRecordings(ctx, mb.SearchFilter{Query: query, Includes: []string{"artists", "artist-credits", "releases"}}, mb.Paginator{Offset: offset, Limit: limit})
	if err != nil {
		return SearchResult{}, safeProviderError(ctx, err)
	}
	if err := validateCapturedResponse(collector); err != nil {
		return SearchResult{}, err
	}
	projected, err := projectSearch(collector.last(), "recordings", result.Count, result.Offset, result.Created, result.Recordings)
	if err != nil {
		return SearchResult{}, err
	}
	return projected, nil
}

func validateSearch(query string, offset, limit int) error {
	if strings.TrimSpace(query) == "" || offset < 0 || limit < 1 || limit > mb.MaxLimit {
		return errors.New("invalid musicbrainz search parameters")
	}
	return nil
}

func validateCapturedResponse(collector *responseCollector) error {
	body := collector.last()
	if len(body) > ResponseBodyLimit {
		return errors.New("musicbrainz response exceeds size limit")
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(collector.latestContentType(), ";")[0]))
	if contentType != "application/json" && !strings.HasSuffix(contentType, "+json") {
		return errors.New("musicbrainz returned an unexpected content type")
	}
	return nil
}

func validMBID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && strings.EqualFold(value, parsed.String())
}

func parseScore(raw json.RawMessage) (int, bool, error) {
	if raw == nil {
		return 0, false, nil
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return 0, false, errors.New("invalid musicbrainz score")
	}
	var score int
	if err := json.Unmarshal(raw, &score); err != nil || score < 0 || score > 100 {
		return 0, false, errors.New("invalid musicbrainz score")
	}
	return score, true, nil
}

func validateLookupEntity(raw map[string]json.RawMessage, requestedID, typedID, typedTitle string) error {
	var id, title string
	if err := unmarshalString(raw["id"], &id); err != nil || !validMBID(id) || !strings.EqualFold(id, requestedID) || !strings.EqualFold(typedID, requestedID) {
		return errors.New("invalid musicbrainz lookup entity id")
	}
	if err := unmarshalString(raw["title"], &title); err != nil || title != typedTitle {
		return errors.New("invalid musicbrainz lookup entity title")
	}
	if _, _, err := parseScore(raw["score"]); err != nil {
		return err
	}
	return nil
}

func unmarshalString(raw json.RawMessage, target *string) error {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return errors.New("expected JSON string")
	}
	return json.Unmarshal(raw, target)
}

func validateSearchEnvelope(envelope map[string]json.RawMessage, count, offset int, created time.Time) error {
	var rawCount, rawOffset int
	if isMissingOrNull(envelope["count"]) || json.Unmarshal(envelope["count"], &rawCount) != nil || rawCount < 0 || rawCount != count {
		return errors.New("invalid musicbrainz search count")
	}
	if isMissingOrNull(envelope["offset"]) || json.Unmarshal(envelope["offset"], &rawOffset) != nil || rawOffset < 0 || rawOffset != offset {
		return errors.New("invalid musicbrainz search offset")
	}
	var rawCreated time.Time
	if err := json.Unmarshal(envelope["created"], &rawCreated); err != nil || rawCreated.IsZero() || !rawCreated.Equal(created) {
		return errors.New("invalid musicbrainz search created timestamp")
	}
	return nil
}

func isMissingOrNull(raw json.RawMessage) bool {
	return len(raw) == 0 || strings.TrimSpace(string(raw)) == "null"
}

func projectSearch[T any](data []byte, key string, count, offset int, created time.Time, typed []T) (SearchResult, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return SearchResult{}, errors.New("invalid musicbrainz search response")
	}
	if len(envelope) == 0 {
		return SearchResult{}, errors.New("invalid musicbrainz search response")
	}
	if err := validateSearchEnvelope(envelope, count, offset, created); err != nil {
		return SearchResult{}, err
	}
	var rawEntities []json.RawMessage
	if err := json.Unmarshal(envelope[key], &rawEntities); err != nil {
		return SearchResult{}, errors.New("invalid musicbrainz search response")
	}
	if len(envelope[key]) == 0 || string(envelope[key]) == "null" {
		return SearchResult{}, errors.New("invalid musicbrainz search response")
	}
	if len(rawEntities) != len(typed) {
		return SearchResult{}, errors.New("musicbrainz search response projection mismatch")
	}
	items := make([]Entity, 0, len(rawEntities))
	for _, raw := range rawEntities {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || len(fields) == 0 {
			return SearchResult{}, errors.New("invalid musicbrainz search entity")
		}
		var id, title, name string
		if err := unmarshalString(fields["id"], &id); err != nil || !validMBID(id) {
			return SearchResult{}, errors.New("invalid musicbrainz search entity id")
		}
		if err := unmarshalString(fields["title"], &title); err != nil {
			return SearchResult{}, errors.New("invalid musicbrainz search entity title")
		}
		if fields["name"] != nil {
			if err := unmarshalString(fields["name"], &name); err != nil {
				return SearchResult{}, errors.New("invalid musicbrainz search entity name")
			}
		}
		score, scoreSet, err := parseScore(fields["score"])
		if err != nil {
			return SearchResult{}, err
		}
		entity := Entity{
			ID: id, Title: title, Name: name, Raw: append(json.RawMessage(nil), raw...),
			ArtistCredit: cloneRaw(fields["artist-credit"]), Media: cloneRaw(fields["media"]),
			Recordings: cloneRaw(fields["recordings"]), ReleaseEvents: cloneRaw(fields["release-events"]),
			Labels: cloneRaw(fields["label-info"]), Releases: cloneRaw(fields["releases"]),
		}
		entity.Score, entity.ScoreSet = score, scoreSet
		items = append(items, entity)
	}
	return SearchResult{Count: count, Offset: offset, Created: created, Items: items}, nil
}

func projectEntity(raw map[string]json.RawMessage, data []byte, id, title, name string, score int) Entity {
	_, hasScore := raw["score"]
	return Entity{
		ID: id, Title: title, Name: name, Score: score, ScoreSet: hasScore, Raw: cloneRaw(json.RawMessage(data)),
		ArtistCredit: cloneRaw(raw["artist-credit"]), Media: cloneRaw(raw["media"]),
		Recordings: cloneRaw(raw["recordings"]), ReleaseEvents: cloneRaw(raw["release-events"]),
		Labels: cloneRaw(raw["label-info"]), Releases: cloneRaw(raw["releases"]),
	}
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func safeProviderError(ctx context.Context, _ error) error {
	if ctx.Err() != nil {
		return errors.New("musicbrainz request timed out")
	}
	return errors.New("musicbrainz request failed")
}
