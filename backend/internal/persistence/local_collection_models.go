package persistence

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Collection struct {
	bun.BaseModel   `bun:"table:collection"`
	ID              uuid.UUID       `bun:"id,pk,type:uuid"`
	Title           string          `bun:"title"`
	Metadata        json.RawMessage `bun:"metadata,type:jsonb"`
	MetadataVersion int             `bun:"metadata_version"`
	CreatedAt       time.Time       `bun:"created_at"`
	UpdatedAt       time.Time       `bun:"updated_at"`
}

type LocalArtist struct {
	bun.BaseModel   `bun:"table:local_artist"`
	ID              uuid.UUID       `bun:"id,pk,type:uuid"`
	Name            string          `bun:"name"`
	Metadata        json.RawMessage `bun:"metadata,type:jsonb"`
	MetadataVersion int             `bun:"metadata_version"`
	CreatedAt       time.Time       `bun:"created_at"`
	UpdatedAt       time.Time       `bun:"updated_at"`
}

type LocalRelease struct {
	bun.BaseModel   `bun:"table:local_release"`
	ID              uuid.UUID       `bun:"id,pk,type:uuid"`
	Title           string          `bun:"title"`
	Metadata        json.RawMessage `bun:"metadata,type:jsonb"`
	MetadataVersion int             `bun:"metadata_version"`
	CreatedAt       time.Time       `bun:"created_at"`
	UpdatedAt       time.Time       `bun:"updated_at"`
}

type LocalRecording struct {
	bun.BaseModel   `bun:"table:local_recording"`
	ID              uuid.UUID       `bun:"id,pk,type:uuid"`
	Title           string          `bun:"title"`
	Metadata        json.RawMessage `bun:"metadata,type:jsonb"`
	MetadataVersion int             `bun:"metadata_version"`
	CreatedAt       time.Time       `bun:"created_at"`
	UpdatedAt       time.Time       `bun:"updated_at"`
}

type LocalTrack struct {
	bun.BaseModel   `bun:"table:local_track"`
	ID              uuid.UUID       `bun:"id,pk,type:uuid"`
	Title           string          `bun:"title"`
	Metadata        json.RawMessage `bun:"metadata,type:jsonb"`
	MetadataVersion int             `bun:"metadata_version"`
	CreatedAt       time.Time       `bun:"created_at"`
	UpdatedAt       time.Time       `bun:"updated_at"`
}

type TrackSource struct {
	bun.BaseModel  `bun:"table:track_source"`
	ID             uuid.UUID       `bun:"id,pk,type:uuid"`
	TrackID        uuid.UUID       `bun:"track_id,type:uuid"`
	MediaVariantID uuid.UUID       `bun:"media_variant_id,type:uuid"`
	Selected       bool            `bun:"selected"`
	Compatible     bool            `bun:"compatible"`
	DecisionMethod string          `bun:"decision_method"`
	PolicyVersion  *string         `bun:"policy_version,nullzero"`
	Metadata       json.RawMessage `bun:"metadata,type:jsonb"`
	CreatedAt      time.Time       `bun:"created_at"`
	UpdatedAt      time.Time       `bun:"updated_at"`
}

type TrackSourceDecision struct {
	ID             uuid.UUID
	TrackID        uuid.UUID
	MediaVariantID uuid.UUID
	Selected       bool
	Compatible     bool
	Method         string
	PolicyVersion  *string
	Metadata       json.RawMessage
}
