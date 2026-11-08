package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var localProviderID = uuid.MustParse("00000000-0000-4000-8000-000000000001")

type LocalProviderCredit struct {
	ArtistID     uuid.UUID `json:"artist_id"`
	ProviderID   uuid.UUID `json:"provider_id"`
	ProviderCode string    `json:"provider_code"`
	ArtistName   string    `json:"artist_name"`
	Name         string    `json:"credit_name"`
	JoinPhrase   string    `json:"join_phrase"`
}

type LocalProviderTrack struct {
	ID                  uuid.UUID `json:"id"`
	RecordingID         uuid.UUID `json:"recording_id"`
	ProviderID          uuid.UUID `json:"provider_id"`
	ProviderCode        string    `json:"provider_code"`
	RecordingTitle      string    `json:"recording_title"`
	RecordingDurationMS *int64    `json:"recording_duration_ms"`
	Position            int       `json:"position"`
	DisplayedNumber     string    `json:"displayed_number"`
	TitleOverride       *string   `json:"title_override"`
	DurationOverrideMS  *int64    `json:"duration_override_ms"`
}

type LocalProviderItem struct {
	ID                uuid.UUID             `json:"id"`
	ProviderID        uuid.UUID             `json:"provider_id"`
	ProviderCode      string                `json:"provider_code"`
	ProviderKey       string                `json:"provider_key"`
	Kind              string                `json:"kind"`
	Name              string                `json:"name,omitempty"`
	SortName          *string               `json:"sort_name,omitempty"`
	Title             string                `json:"title,omitempty"`
	ReleaseGroupTitle string                `json:"release_group_title,omitempty"`
	DurationMS        *int64                `json:"duration_ms,omitempty"`
	PrimaryType       *string               `json:"release_group_primary_type,omitempty"`
	SecondaryTypes    []string              `json:"release_group_secondary_types"`
	Language          *string               `json:"language,omitempty"`
	Script            *string               `json:"script,omitempty"`
	Genres            json.RawMessage       `json:"genres"`
	Tags              []string              `json:"tags"`
	Credits           []LocalProviderCredit `json:"credits"`
	Tracks            []LocalProviderTrack  `json:"tracks"`
}

type LocalProviderList struct {
	Items  []LocalProviderItem `json:"items"`
	Total  int                 `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
}

type LocalProviderRepository struct{ db *bun.DB }

func NewLocalProviderRepository(db *bun.DB) *LocalProviderRepository {
	return &LocalProviderRepository{db: db}
}

func (r *LocalProviderRepository) List(ctx context.Context, kind, query, provider string, limit, offset int) (LocalProviderList, error) {
	switch kind {
	case "artist", "artists":
		kind = "artists"
	case "recording", "recordings":
		kind = "recordings"
	case "release", "releases":
		kind = "releases"
	default:
		return LocalProviderList{}, fmt.Errorf("invalid local provider catalog kind")
	}
	if provider == "" {
		provider = "local"
	}
	result := LocalProviderList{Items: []LocalProviderItem{}, Limit: limit, Offset: offset}
	table, titleExpr := providerCatalogTable(kind)
	fieldSearch := "name"
	if kind != "artists" {
		fieldSearch = "title"
	}
	pattern := "%" + strings.TrimSpace(query) + "%"
	where := "(?='all' OR p.code=?) AND (e." + fieldSearch + " ILIKE ? OR e.provider_key ILIKE ?)"
	if err := r.db.NewRaw("SELECT count(*) FROM "+table+" e JOIN provider p ON p.id=e.provider_id WHERE "+where, provider, provider, pattern, pattern).Scan(ctx, &result.Total); err != nil {
		return LocalProviderList{}, fmt.Errorf("count local provider catalog: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT e.id,e.provider_id,e.provider_key,e.`+titleExpr+`,e.metadata::text,p.code FROM `+table+` e JOIN provider p ON p.id=e.provider_id WHERE `+where+` ORDER BY e.`+titleExpr+`,e.id LIMIT ? OFFSET ?`, provider, provider, pattern, pattern, limit, offset)
	if err != nil {
		return LocalProviderList{}, fmt.Errorf("list local provider catalog: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		item := LocalProviderItem{Genres: json.RawMessage("[]")}
		var metadata string
		if err := rows.Scan(&item.ID, &item.ProviderID, &item.ProviderKey, &item.Title, &metadata, &item.ProviderCode); err != nil {
			return LocalProviderList{}, fmt.Errorf("scan local provider catalog: %w", err)
		}
		item.Kind = strings.TrimSuffix(kind, "s")
		if item.Kind == "artist" {
			item.Name, item.Title = item.Title, ""
		}
		if err := decodeLocalProviderMetadata(&item, []byte(metadata)); err != nil {
			return LocalProviderList{}, err
		}
		if item.Kind != "release" {
			item.SecondaryTypes = nil
		}
		if item.Kind == "release" {
			item.Tags = nil
		}
		item.Credits, item.Tracks = []LocalProviderCredit{}, []LocalProviderTrack{}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return LocalProviderList{}, fmt.Errorf("iterate local provider catalog: %w", err)
	}
	return result, nil
}

func (r *LocalProviderRepository) Read(ctx context.Context, kind string, id uuid.UUID) (LocalProviderItem, error) {
	table, titleExpr := providerCatalogTable(kind + "s")
	item := LocalProviderItem{ID: id, Kind: kind, Genres: json.RawMessage("[]"), Tags: []string{}, SecondaryTypes: []string{}, Credits: []LocalProviderCredit{}, Tracks: []LocalProviderTrack{}}
	var metadata string
	err := r.db.NewRaw(`SELECT provider_id,provider_key,`+titleExpr+`,metadata::text FROM `+table+` WHERE id=? AND provider_id=?`, id, localProviderID).Scan(ctx, &item.ProviderID, &item.ProviderKey, &item.Title, &metadata)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LocalProviderItem{}, sql.ErrNoRows
		}
		return LocalProviderItem{}, fmt.Errorf("read local provider catalog item: %w", err)
	}
	if err := decodeLocalProviderMetadata(&item, []byte(metadata)); err != nil {
		return LocalProviderItem{}, err
	}
	if kind != "release" {
		item.SecondaryTypes = nil
	}
	if kind == "release" {
		item.Tags = nil
	}
	if kind == "artist" {
		item.Name, item.Title = item.Title, ""
	}
	if kind == "release" {
		if err := r.readReleaseGraph(ctx, &item); err != nil {
			return LocalProviderItem{}, err
		}
	}
	if kind == "recording" {
		for i := range item.Credits {
			if err := r.db.NewRaw(`SELECT a.provider_id,p.code,a.name FROM provider_artist a JOIN provider p ON p.id=a.provider_id WHERE a.id=?`, item.Credits[i].ArtistID).Scan(ctx, &item.Credits[i].ProviderID, &item.Credits[i].ProviderCode, &item.Credits[i].ArtistName); err != nil {
				return LocalProviderItem{}, fmt.Errorf("read recording credit provider: %w", err)
			}
		}
	}
	item.ProviderCode = "local"
	return item, nil
}

func (r *LocalProviderRepository) Save(ctx context.Context, item LocalProviderItem, create bool) (LocalProviderItem, error) {
	if item.Kind != "artist" && item.Kind != "recording" && item.Kind != "release" {
		return LocalProviderItem{}, fmt.Errorf("invalid local provider catalog kind")
	}
	id := item.ID
	if create {
		id = uuid.New()
	} else if id == uuid.Nil {
		return LocalProviderItem{}, fmt.Errorf("local provider catalog item ID is required")
	}
	key := ""
	if !create {
		if err := r.db.NewRaw(`SELECT provider_key FROM `+providerCatalogTableName(item.Kind)+` WHERE id=? AND provider_id=?`, id, localProviderID).Scan(ctx, &key); err != nil {
			return LocalProviderItem{}, err
		}
	} else {
		key = uuid.NewString()
	}
	metadata, err := encodeLocalProviderMetadata(item)
	if err != nil {
		return LocalProviderItem{}, fmt.Errorf("encode local provider metadata: %w", err)
	}
	err = r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		now := time.Now().UTC()
		switch item.Kind {
		case "artist":
			_, err = tx.NewRaw(`INSERT INTO provider_artist(id,provider_id,provider_key,name,metadata,raw_source,updated_at) VALUES(?,?,?, ?,?::jsonb,'{}',?) ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name,metadata=EXCLUDED.metadata,updated_at=EXCLUDED.updated_at WHERE provider_artist.provider_id=?`, id, localProviderID, key, item.Name, string(metadata), now, localProviderID).Exec(ctx)
		case "recording":
			if err = validateLocalRecordingCredits(ctx, tx, item.Credits); err != nil {
				return err
			}
			_, err = tx.NewRaw(`INSERT INTO provider_recording(id,provider_id,provider_key,title,metadata,raw_source,updated_at) VALUES(?,?,?, ?,?::jsonb,'{}',?) ON CONFLICT (id) DO UPDATE SET title=EXCLUDED.title,metadata=EXCLUDED.metadata,updated_at=EXCLUDED.updated_at WHERE provider_recording.provider_id=?`, id, localProviderID, key, item.Title, string(metadata), now, localProviderID).Exec(ctx)
		case "release":
			_, err = tx.NewRaw(`INSERT INTO provider_release(id,provider_id,provider_key,title,metadata,raw_source,updated_at) VALUES(?,?,?, ?,?::jsonb,'{}',?) ON CONFLICT (id) DO UPDATE SET title=EXCLUDED.title,metadata=EXCLUDED.metadata,updated_at=EXCLUDED.updated_at WHERE provider_release.provider_id=?`, id, localProviderID, key, item.Title, string(metadata), now, localProviderID).Exec(ctx)
			if err == nil {
				err = saveLocalReleaseGraph(ctx, tx, id, item)
			}
		}
		return err
	})
	if err != nil {
		return LocalProviderItem{}, fmt.Errorf("save local provider catalog item: %w", err)
	}
	item.ID, item.ProviderID, item.ProviderCode, item.ProviderKey = id, localProviderID, "local", key
	return r.Read(ctx, item.Kind, id)
}

func validateLocalRecordingCredits(ctx context.Context, tx bun.Tx, credits []LocalProviderCredit) error {
	for _, credit := range credits {
		var exists bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM provider_artist WHERE id=?)`, credit.ArtistID).Scan(ctx, &exists); err != nil {
			return fmt.Errorf("validate local recording credit: %w", err)
		}
		if !exists {
			return fmt.Errorf("local recording credit artist does not exist")
		}
	}
	return nil
}

func providerCatalogTable(kind string) (string, string) {
	if kind == "artists" || kind == "artist" {
		return providerCatalogTableName(kind), "name"
	}
	return providerCatalogTableName(kind), "title"
}

func providerCatalogTableName(kind string) string {
	switch kind {
	case "artists", "artist":
		return "provider_artist"
	case "recordings", "recording":
		return "provider_recording"
	default:
		return "provider_release"
	}
}

func decodeLocalProviderMetadata(item *LocalProviderItem, data []byte) error {
	var fields struct {
		SortName          *string               `json:"sort_name"`
		DurationMS        *int64                `json:"duration_ms"`
		ReleaseGroupTitle string                `json:"release_group_title"`
		PrimaryType       *string               `json:"release_group_primary_type"`
		SecondaryTypes    []string              `json:"release_group_secondary_types"`
		Language          *string               `json:"language"`
		Script            *string               `json:"script"`
		Genres            json.RawMessage       `json:"genres"`
		Tags              []string              `json:"tags"`
		Credits           []LocalProviderCredit `json:"credits"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("decode local provider metadata: %w", err)
	}
	item.SortName, item.DurationMS, item.ReleaseGroupTitle = fields.SortName, fields.DurationMS, fields.ReleaseGroupTitle
	item.PrimaryType, item.SecondaryTypes, item.Language, item.Script = fields.PrimaryType, fields.SecondaryTypes, fields.Language, fields.Script
	if len(fields.Genres) > 0 {
		item.Genres = fields.Genres
	}
	item.Tags = fields.Tags
	item.Credits = fields.Credits
	if item.Tags == nil {
		item.Tags = []string{}
	}
	if item.SecondaryTypes == nil {
		item.SecondaryTypes = []string{}
	}
	return nil
}

func encodeLocalProviderMetadata(item LocalProviderItem) ([]byte, error) {
	fields := map[string]any{"genres": item.Genres}
	switch item.Kind {
	case "artist":
		fields["tags"] = item.Tags
		if item.SortName != nil {
			fields["sort_name"] = item.SortName
		}
	case "recording":
		fields["tags"] = item.Tags
		if item.DurationMS != nil {
			fields["duration_ms"] = item.DurationMS
		}
		credits := make([]map[string]any, 0, len(item.Credits))
		for _, credit := range item.Credits {
			credits = append(credits, map[string]any{"artist_id": credit.ArtistID, "credit_name": credit.Name, "join_phrase": credit.JoinPhrase})
		}
		fields["credits"] = credits
	case "release":
		fields["release_group_title"] = item.ReleaseGroupTitle
		fields["release_group_secondary_types"] = item.SecondaryTypes
		if item.PrimaryType != nil {
			fields["release_group_primary_type"] = item.PrimaryType
		}
		if item.Language != nil {
			fields["language"] = item.Language
		}
		if item.Script != nil {
			fields["script"] = item.Script
		}
	default:
		return nil, fmt.Errorf("invalid local provider catalog kind")
	}
	return json.Marshal(fields)
}

func saveLocalReleaseGraph(ctx context.Context, tx bun.Tx, releaseID uuid.UUID, item LocalProviderItem) error {
	if _, err := tx.NewRaw(`UPDATE provider_release_artist_credit SET present=false WHERE provider_id=? AND release_id=?`, localProviderID, releaseID).Exec(ctx); err != nil {
		return err
	}
	for i, credit := range item.Credits {
		if _, err := tx.NewRaw(`INSERT INTO provider_release_artist_credit(provider_id,release_id,position,artist_id,credit_name,name_join_phrase,present,authority,fetch_order) VALUES(?,?,?,?,?,?,true,0,0) ON CONFLICT (provider_id,release_id,position) DO UPDATE SET artist_id=EXCLUDED.artist_id,credit_name=EXCLUDED.credit_name,name_join_phrase=EXCLUDED.name_join_phrase,present=true`, localProviderID, releaseID, i, credit.ArtistID, credit.Name, credit.JoinPhrase).Exec(ctx); err != nil {
			return fmt.Errorf("save local release credit: %w", err)
		}
	}
	if _, err := tx.NewRaw(`UPDATE provider_release_track SET present=false,medium=NULL,position=NULL WHERE provider_id=? AND release_id=? AND present`, localProviderID, releaseID).Exec(ctx); err != nil {
		return fmt.Errorf("retire local release tracks: %w", err)
	}
	for i, track := range item.Tracks {
		trackID := track.ID
		if trackID == uuid.Nil {
			trackID = uuid.New()
		}
		data, err := json.Marshal(map[string]any{"title_override": track.TitleOverride, "duration_override_ms": track.DurationOverrideMS})
		if err != nil {
			return err
		}
		result, err := tx.NewRaw(`INSERT INTO provider_release_track(id,provider_id,release_id,recording_id,medium,position,displayed_number,present,authority,fetch_order,metadata) VALUES(?,?,?,?,0,?,?,true,0,0,?::jsonb) ON CONFLICT (id) DO UPDATE SET recording_id=EXCLUDED.recording_id,medium=0,position=EXCLUDED.position,displayed_number=EXCLUDED.displayed_number,present=true,metadata=EXCLUDED.metadata WHERE provider_release_track.provider_id=? AND provider_release_track.release_id=?`, trackID, localProviderID, releaseID, track.RecordingID, i, fmt.Sprint(i+1), string(data), localProviderID, releaseID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("save local release track: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return fmt.Errorf("local release track does not belong to this release")
		}
	}
	return nil
}

func (r *LocalProviderRepository) readReleaseGraph(ctx context.Context, item *LocalProviderItem) error {
	rows, err := r.db.QueryContext(ctx, `SELECT c.artist_id,a.provider_id,p.code,a.name,c.credit_name,c.name_join_phrase FROM provider_release_artist_credit c JOIN provider_artist a ON a.id=c.artist_id JOIN provider p ON p.id=a.provider_id WHERE c.provider_id=? AND c.release_id=? AND c.present ORDER BY c.position`, localProviderID, item.ID)
	if err != nil {
		return fmt.Errorf("read local release credits: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var credit LocalProviderCredit
		if err := rows.Scan(&credit.ArtistID, &credit.ProviderID, &credit.ProviderCode, &credit.ArtistName, &credit.Name, &credit.JoinPhrase); err != nil {
			return err
		}
		item.Credits = append(item.Credits, credit)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	tracks, err := r.db.QueryContext(ctx, `SELECT t.id,t.recording_id,r.provider_id,p.code,r.title,NULLIF(r.metadata->>'duration_ms','')::bigint,t.position,t.displayed_number,t.metadata->>'title_override',NULLIF(t.metadata->>'duration_override_ms','')::bigint FROM provider_release_track t JOIN provider_recording r ON r.id=t.recording_id JOIN provider p ON p.id=r.provider_id WHERE t.provider_id=? AND t.release_id=? AND t.present ORDER BY t.position`, localProviderID, item.ID)
	if err != nil {
		return fmt.Errorf("read local release tracks: %w", err)
	}
	defer func() { _ = tracks.Close() }()
	for tracks.Next() {
		var track LocalProviderTrack
		if err := tracks.Scan(&track.ID, &track.RecordingID, &track.ProviderID, &track.ProviderCode, &track.RecordingTitle, &track.RecordingDurationMS, &track.Position, &track.DisplayedNumber, &track.TitleOverride, &track.DurationOverrideMS); err != nil {
			return err
		}
		item.Tracks = append(item.Tracks, track)
	}
	return tracks.Err()
}
