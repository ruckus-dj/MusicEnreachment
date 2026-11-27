package musicbrainz

import (
	"context"
	"errors"
	"strings"

	"go.uploadedlobster.com/mbtypes"
	mb "go.uploadedlobster.com/musicbrainzws2"
)

// UserAgent is the application contact string shared by metadata providers.
const UserAgent = providerUserAgent

// ArtistURLRelation is one URL relationship attached to a MusicBrainz artist.
type ArtistURLRelation struct {
	Type string
	URL  string
}

// LookupArtistURLRelations returns the URL relationships for exactly id. It
// uses the provider's configured endpoint and governed transport, so the
// request shares the same public rate limit and self-hosted throttling as every
// other provider operation.
func (p *Provider) LookupArtistURLRelations(ctx context.Context, id string) ([]ArtistURLRelation, error) {
	if !validMBID(id) {
		return nil, errors.New("invalid musicbrainz artist id")
	}
	ctx, cancel, collector := p.callContext(ctx)
	defer cancel()
	artist, err := p.client.LookupArtist(ctx, mbtypes.MBID(id), mb.IncludesFilter{Includes: []string{"url-rels"}})
	if err != nil {
		return nil, safeProviderError(ctx, err)
	}
	if err := validateCapturedResponse(collector); err != nil {
		return nil, err
	}
	if !strings.EqualFold(string(artist.ID), id) || strings.TrimSpace(artist.Name) == "" {
		return nil, errors.New("invalid musicbrainz artist lookup response")
	}
	relations := make([]ArtistURLRelation, 0, len(artist.Relations))
	for _, relation := range artist.Relations {
		if relation.URL == nil {
			continue
		}
		resource := strings.TrimSpace(relation.URL.Resource)
		if resource == "" {
			continue
		}
		relations = append(relations, ArtistURLRelation{Type: strings.TrimSpace(relation.Type), URL: resource})
	}
	return relations, nil
}
