package persistence

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Provider struct {
	bun.BaseModel `bun:"table:provider"`
	ID            uuid.UUID `bun:"id,pk,type:uuid"`
	Code          string    `bun:"code"`
	CreatedAt     time.Time `bun:"created_at,nullzero"`
}

// ConfigurationIdentity is an opaque caller-supplied fence. Endpoint and
// namespace identify the source; configuration changes advance this identity.
type ProviderSource struct {
	bun.BaseModel         `bun:"table:provider_source"`
	ID                    uuid.UUID `bun:"id,pk,type:uuid"`
	ProviderID            uuid.UUID `bun:"provider_id,type:uuid"`
	Namespace             string    `bun:"namespace"`
	Endpoint              string    `bun:"endpoint"`
	ConfigurationIdentity string    `bun:"configuration_identity"`
	CreatedAt             time.Time `bun:"created_at,nullzero"`
	UpdatedAt             time.Time `bun:"updated_at,nullzero"`
}

type ProviderArtist struct {
	ID          uuid.UUID       `json:"id"`
	ProviderKey string          `json:"provider_key"`
	Name        string          `json:"name"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	RawSource   json.RawMessage `json:"raw_source,omitempty"`
}

type ProviderRelease struct {
	ID          uuid.UUID       `json:"id"`
	ProviderKey string          `json:"provider_key"`
	Title       string          `json:"title"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	RawSource   json.RawMessage `json:"raw_source,omitempty"`
}

type ProviderRecording struct {
	ID          uuid.UUID       `json:"id"`
	ProviderKey string          `json:"provider_key"`
	Title       string          `json:"title"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	RawSource   json.RawMessage `json:"raw_source,omitempty"`
}

type ProviderReleaseArtistCredit struct {
	ReleaseID      uuid.UUID `json:"release_id"`
	Position       int       `json:"position"`
	ArtistID       uuid.UUID `json:"artist_id"`
	NameJoinPhrase string    `json:"name_join_phrase"`
}

type ProviderReleaseTrack struct {
	ID              uuid.UUID `json:"id"`
	ReleaseID       uuid.UUID `json:"release_id"`
	RecordingID     uuid.UUID `json:"recording_id"`
	Medium          int       `json:"medium"`
	Position        int       `json:"position"`
	DisplayedNumber string    `json:"displayed_number"`
}

// ProviderGraph is one provider-scoped response graph. Complete is trusted only
// as a caller-provided fact; partial responses never mark unseen rows absent.
type ProviderGraph struct {
	Complete   bool                          `json:"complete"`
	Artists    []ProviderArtist              `json:"artists,omitempty"`
	Releases   []ProviderRelease             `json:"releases,omitempty"`
	Recordings []ProviderRecording           `json:"recordings,omitempty"`
	Credits    []ProviderReleaseArtistCredit `json:"credits,omitempty"`
	Tracks     []ProviderReleaseTrack        `json:"tracks,omitempty"`
}

type ProviderCachedResponse struct {
	ProviderSourceID uuid.UUID       `bun:"provider_source_id,type:uuid"`
	ProviderID       uuid.UUID       `bun:"provider_id,type:uuid"`
	CacheKey         string          `bun:"cache_key"`
	Payload          json.RawMessage `bun:"payload,type:jsonb,nullzero"`
	FetchedAt        *time.Time      `bun:"fetched_at,nullzero"`
	Revision         *string         `bun:"revision,nullzero"`
	Generation       int64           `bun:"generation"`
}

type ProviderRefreshTicket struct {
	ProviderSourceID      uuid.UUID
	ProviderID            uuid.UUID
	CacheKey              string
	Generation            int64
	ConfigurationIdentity string
}

type ProviderSuccessfulResponse struct {
	Ticket    ProviderRefreshTicket
	FetchedAt time.Time
	Revision  string
	Payload   json.RawMessage
	Graph     ProviderGraph
}
