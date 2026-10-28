package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
	"golang.org/x/text/unicode/norm"
)

type LibraryOptionPageRequest struct {
	Query         string
	Limit, Offset int
}
type LibraryRecordingOption struct {
	ID            uuid.UUID                   `json:"id"`
	Title         string                      `json:"title"`
	Artists       []LibraryOptionCredit       `json:"artists"`
	ProviderLinks []LibraryOptionProviderLink `json:"provider_links"`
	DurationMS    *int64                      `json:"duration_ms"`
	Total         int                         `json:"-" bun:"total"`
}
type LibraryOptionCredit struct {
	ArtistID   uuid.UUID `json:"artist_id"`
	Name       string    `json:"name"`
	JoinPhrase string    `json:"join_phrase"`
}
type LibraryOptionProviderLink struct {
	ProviderCode string `json:"provider_code"`
	ProviderKey  string `json:"provider_key"`
}
type LibraryRecordingOptions struct {
	Items []LibraryRecordingOption
	Total int
}

type LibrarySourceVariantOption struct {
	MediaVariantID   uuid.UUID                      `json:"media_variant_id"`
	MetadataResultID *uuid.UUID                     `json:"metadata_result_id"`
	Title            string                         `json:"title"`
	Filename         string                         `json:"filename"`
	Codec            string                         `json:"codec"`
	Container        string                         `json:"container"`
	DurationMS       *int64                         `json:"duration_ms"`
	Locations        []LibrarySourceVariantLocation `json:"locations"`
	Total            int                            `json:"-" bun:"total"`
	MetadataTags     string                         `json:"-" bun:"metadata_tags"`
	ProbeJSON        string                         `json:"-" bun:"probe_json"`
}
type LibrarySourceVariantLocation struct {
	LocationID      uuid.UUID `json:"location_id"`
	SourceRootID    uuid.UUID `json:"source_root_id"`
	RelativePath    string    `json:"relative_path"`
	RootDisplayName string    `json:"root_display_name"`
	RootStatus      string    `json:"root_status"`
	RootEnabled     bool      `json:"root_enabled"`
}
type LibrarySourceVariantOptions struct {
	Items []LibrarySourceVariantOption
	Total int
}
type LibraryCoverCandidateOption struct {
	MetadataResultID uuid.UUID `json:"metadata_result_id"`
	MediaVariantID   uuid.UUID `json:"media_variant_id"`
	SHA256           string    `json:"sha256"`
	MIME             string    `json:"mime"`
	Width            *int      `json:"width"`
	Height           *int      `json:"height"`
	Size             int64     `json:"size"`
	Label            string    `json:"label"`
	Data             []byte    `json:"-"`
	Total            int       `json:"-" bun:"total"`
	WidthText        string    `json:"-" bun:"width_text"`
	HeightText       string    `json:"-" bun:"height_text"`
}
type LibraryCoverCandidateOptions struct {
	Items []LibraryCoverCandidateOption
	Total int
}

func (repository *LibraryRepository) ReadLibraryRecordingOptions(ctx context.Context, request LibraryOptionPageRequest) (LibraryRecordingOptions, error) {
	page := LibraryRecordingOptions{Items: make([]LibraryRecordingOption, 0)}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		rows := make([]LibraryRecordingOption, 0)
		query := normalizeLibraryOptionSearch(request.Query)
		pattern := "%" + escapeLike(query) + "%"
		if err := tx.NewRaw(`SELECT count(*) FROM local_recording r
			WHERE ?='' OR regexp_replace(lower(normalize(r.title,NFKC)), E'\\s+', ' ', 'g') LIKE ? ESCAPE E'\\'
				OR EXISTS (SELECT 1 FROM recording_artist ra WHERE ra.recording_id=r.id AND regexp_replace(lower(normalize(ra.name,NFKC)), E'\\s+', ' ', 'g') LIKE ? ESCAPE E'\\')`, query, pattern, pattern).Scan(ctx, &page.Total); err != nil {
			return fmt.Errorf("count library recording options: %w", err)
		}
		err := tx.NewRaw(`WITH selected AS (
			SELECT r.id,r.title
			FROM local_recording r
			WHERE ?='' OR regexp_replace(lower(normalize(r.title,NFKC)), E'\\s+', ' ', 'g') LIKE ? ESCAPE E'\\'
				OR EXISTS (SELECT 1 FROM recording_artist ra WHERE ra.recording_id=r.id AND regexp_replace(lower(normalize(ra.name,NFKC)), E'\\s+', ' ', 'g') LIKE ? ESCAPE E'\\')
			ORDER BY regexp_replace(lower(normalize(r.title,NFKC)), E'\\s+', ' ', 'g'),r.id LIMIT ? OFFSET ?
		) SELECT id,title FROM selected`, query, pattern, pattern, request.Limit, request.Offset).Scan(ctx, &rows)
		if err != nil {
			return fmt.Errorf("read library recording options: %w", err)
		}
		for i := range rows {
			rows[i].Artists = make([]LibraryOptionCredit, 0)
			rows[i].ProviderLinks = make([]LibraryOptionProviderLink, 0)
			rows[i].Total = rowsTotal(rows)
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		if len(ids) > 0 {
			credits := make([]struct {
				RecordingID uuid.UUID `bun:"recording_id"`
				ArtistID    uuid.UUID `bun:"artist_id"`
				Name        string    `bun:"name"`
				JoinPhrase  string    `bun:"name_join_phrase"`
			}, 0)
			if err := tx.NewRaw(`SELECT recording_id,artist_id,name,name_join_phrase FROM recording_artist WHERE recording_id IN (?) ORDER BY recording_id,position`, bun.List(ids)).Scan(ctx, &credits); err != nil {
				return fmt.Errorf("read recording option artists: %w", err)
			}
			byID := make(map[uuid.UUID]int, len(rows))
			for i := range rows {
				byID[rows[i].ID] = i
			}
			for _, credit := range credits {
				i := byID[credit.RecordingID]
				rows[i].Artists = append(rows[i].Artists, LibraryOptionCredit{ArtistID: credit.ArtistID, Name: credit.Name, JoinPhrase: credit.JoinPhrase})
			}
			links := make([]struct {
				RecordingID  uuid.UUID `bun:"recording_id"`
				ProviderCode string    `bun:"provider_code"`
				ProviderKey  string    `bun:"provider_key"`
			}, 0)
			if err := tx.NewRaw(`SELECT plr.local_recording_id AS recording_id,p.code AS provider_code,pr.provider_key FROM provider_local_recording plr JOIN provider p ON p.id=plr.provider_id JOIN provider_recording pr ON pr.id=plr.provider_recording_id AND pr.provider_id=plr.provider_id WHERE plr.local_recording_id IN (?) ORDER BY plr.local_recording_id,p.code,pr.provider_key`, bun.List(ids)).Scan(ctx, &links); err != nil {
				return fmt.Errorf("read recording option provider links: %w", err)
			}
			for _, link := range links {
				i := byID[link.RecordingID]
				rows[i].ProviderLinks = append(rows[i].ProviderLinks, LibraryOptionProviderLink{ProviderCode: link.ProviderCode, ProviderKey: link.ProviderKey})
			}
		}
		page.Items = rows
		return nil
	})
	return page, err
}

func (repository *LibraryRepository) ReadLibrarySourceVariantOptions(ctx context.Context, request LibraryOptionPageRequest) (LibrarySourceVariantOptions, error) {
	page := LibrarySourceVariantOptions{Items: make([]LibrarySourceVariantOption, 0)}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		rows := make([]LibrarySourceVariantOption, 0)
		q := normalizeLibraryOptionSearch(request.Query)
		pattern := "%" + escapeLike(q) + "%"
		if err := tx.NewRaw(`WITH captures AS (
			SELECT v.id AS media_variant_id,v.ffprobe_json::text AS probe_json,v.observed_tags::text AS metadata_tags,
				count(DISTINCT mr.id) AS capture_count,min(mr.id::text)::uuid AS capture_id,
				min(mr.observed_tags::text) AS selected_tags
			FROM media_variant v
			JOIN source_location l ON l.media_variant_id=v.id AND l.probe_status='audio'
			LEFT JOIN source_analysis_work w ON w.current_location_id=l.id
			LEFT JOIN source_analysis_step s ON s.work_id=w.id AND s.step='metadata' AND s.state='succeeded'
			LEFT JOIN media_metadata_result mr ON mr.id=s.success_metadata_result_id
			WHERE v.ffprobe_json IS NOT NULL
			GROUP BY v.id,v.ffprobe_json,v.observed_tags
		)
		SELECT count(*) FROM captures c
		WHERE ?='' OR EXISTS (SELECT 1 FROM source_location l WHERE l.media_variant_id=c.media_variant_id AND regexp_replace(lower(normalize(l.relative_path,NFKC)),E'\\s+',' ','g') LIKE ? ESCAPE E'\\')
			OR regexp_replace(lower(normalize(CASE WHEN c.capture_count=1 THEN c.selected_tags ELSE '{}' END,NFKC)),E'\\s+',' ','g') LIKE ? ESCAPE E'\\'`, q, pattern, pattern).Scan(ctx, &page.Total); err != nil {
			return fmt.Errorf("count source variant options: %w", err)
		}
		if err := tx.NewRaw(`WITH captures AS (
			SELECT v.id AS media_variant_id,v.ffprobe_json::text AS probe_json,v.observed_tags::text AS metadata_tags,
				count(DISTINCT mr.id) AS capture_count,min(mr.id::text)::uuid AS capture_id,
				min(mr.observed_tags::text) AS selected_tags
			FROM media_variant v
			JOIN source_location l ON l.media_variant_id=v.id AND l.probe_status='audio'
			LEFT JOIN source_analysis_work w ON w.current_location_id=l.id
			LEFT JOIN source_analysis_step s ON s.work_id=w.id AND s.step='metadata' AND s.state='succeeded'
			LEFT JOIN media_metadata_result mr ON mr.id=s.success_metadata_result_id
			WHERE v.ffprobe_json IS NOT NULL
			GROUP BY v.id,v.ffprobe_json,v.observed_tags
		), matched AS (
			SELECT c.media_variant_id,c.probe_json,
				CASE WHEN c.capture_count=1 THEN c.selected_tags ELSE '{}' END AS metadata_tags,
				CASE WHEN c.capture_count=1 THEN c.capture_id ELSE NULL END AS metadata_result_id,count(*) OVER() AS total
			FROM captures c
			WHERE ?='' OR EXISTS (SELECT 1 FROM source_location l WHERE l.media_variant_id=c.media_variant_id AND regexp_replace(lower(normalize(l.relative_path,NFKC)),E'\\s+',' ','g') LIKE ? ESCAPE E'\\')
				OR regexp_replace(lower(normalize(CASE WHEN c.capture_count=1 THEN c.selected_tags ELSE '{}' END,NFKC)),E'\\s+',' ','g') LIKE ? ESCAPE E'\\'
			ORDER BY c.media_variant_id LIMIT ? OFFSET ?
		) SELECT media_variant_id,probe_json,metadata_tags,metadata_result_id,total FROM matched`, q, pattern, pattern, request.Limit, request.Offset).Scan(ctx, &rows); err != nil {
			return fmt.Errorf("read source variant options: %w", err)
		}
		variantIDs := make([]uuid.UUID, 0, len(rows))
		byVariant := make(map[uuid.UUID]int, len(rows))
		for i := range rows {
			rows[i].Locations = make([]LibrarySourceVariantLocation, 0)
			variantIDs = append(variantIDs, rows[i].MediaVariantID)
			byVariant[rows[i].MediaVariantID] = i
		}
		if len(variantIDs) > 0 {
			locations := make([]struct {
				MediaVariantID  uuid.UUID `bun:"media_variant_id"`
				LocationID      uuid.UUID `bun:"location_id"`
				SourceRootID    uuid.UUID `bun:"source_root_id"`
				RelativePath    string    `bun:"relative_path"`
				RootDisplayName string    `bun:"root_display_name"`
				RootStatus      string    `bun:"root_status"`
				RootEnabled     bool      `bun:"root_enabled"`
			}, 0)
			if err := tx.NewRaw(`SELECT DISTINCT l.media_variant_id,l.id AS location_id,l.source_root_id,l.relative_path,r.display_name AS root_display_name,r.status AS root_status,r.enabled AS root_enabled FROM source_location l JOIN source_root r ON r.id=l.source_root_id WHERE l.media_variant_id IN (?) ORDER BY l.media_variant_id,r.display_name,l.relative_path,l.id`, bun.List(variantIDs)).Scan(ctx, &locations); err != nil {
				return fmt.Errorf("read source variant locations: %w", err)
			}
			for _, location := range locations {
				i := byVariant[location.MediaVariantID]
				rows[i].Locations = append(rows[i].Locations, LibrarySourceVariantLocation{LocationID: location.LocationID, SourceRootID: location.SourceRootID, RelativePath: location.RelativePath, RootDisplayName: location.RootDisplayName, RootStatus: location.RootStatus, RootEnabled: location.RootEnabled})
			}
		}
		for i := range rows {
			var tags map[string][]string
			if err := json.Unmarshal([]byte(rows[i].MetadataTags), &tags); err != nil {
				return fmt.Errorf("decode source variant tags: %w", err)
			}
			rows[i].Title = firstOptionTag(tags, "title", "album")
			rows[i].Filename = rows[i].Locations[0].RelativePath
			if len(rows[i].Locations) > 0 {
				parts := strings.Split(rows[i].Filename, "/")
				rows[i].Filename = parts[len(parts)-1]
			}
			if rows[i].Title == "" {
				rows[i].Title = rows[i].Filename
			}
			var probe struct {
				Format struct {
					FormatName string `json:"format_name"`
					Duration   string `json:"duration"`
				} `json:"format"`
				Streams []struct {
					CodecName string `json:"codec_name"`
					CodecType string `json:"codec_type"`
				} `json:"streams"`
			}
			if err := json.Unmarshal([]byte(rows[i].ProbeJSON), &probe); err != nil {
				return fmt.Errorf("decode source variant probe facts: %w", err)
			}
			rows[i].Container = probe.Format.FormatName
			for _, stream := range probe.Streams {
				if stream.CodecType == "audio" {
					rows[i].Codec = stream.CodecName
					break
				}
			}
			if ms, ok := durationMilliseconds(probe.Format.Duration); ok {
				rows[i].DurationMS = &ms
			}
		}
		page.Items = rows
		return nil
	})
	return page, err
}

func (repository *LibraryRepository) ReadLibraryCoverCandidateOptions(ctx context.Context, request LibraryOptionPageRequest) (LibraryCoverCandidateOptions, error) {
	page := LibraryCoverCandidateOptions{Items: make([]LibraryCoverCandidateOption, 0)}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		query := normalizeLibraryOptionSearch(request.Query)
		pattern := "%" + escapeLike(query) + "%"
		const candidates = `WITH candidates AS (
			SELECT mr.id AS metadata_result_id,min(l.media_variant_id::text)::uuid AS media_variant_id,mr.cover_sha256,mr.cover_mime,mr.cover_size_bytes,
				string_agg(DISTINCT l.relative_path, ', ' ORDER BY l.relative_path) AS label
			FROM media_metadata_result mr JOIN source_analysis_step s ON s.success_metadata_result_id=mr.id AND s.step='metadata' AND s.state='succeeded'
			JOIN source_analysis_work w ON w.id=s.work_id JOIN source_location l ON l.id=coalesce(w.current_location_id,w.location_id) AND l.media_variant_id IS NOT NULL
			WHERE mr.cover_observed IS TRUE AND mr.cover_data IS NOT NULL AND octet_length(mr.cover_data)>0 AND mr.cover_sha256 IS NOT NULL
			GROUP BY mr.id,mr.cover_sha256,mr.cover_mime,mr.cover_size_bytes
		), filtered AS (
			SELECT * FROM candidates WHERE ?='' OR regexp_replace(lower(normalize(label,NFKC)),E'\\s+',' ','g') LIKE ? ESCAPE E'\\'
		)`
		if err := tx.NewRaw(candidates+` SELECT count(*) FROM filtered`, query, pattern).Scan(ctx, &page.Total); err != nil {
			return fmt.Errorf("count saved cover candidate options: %w", err)
		}
		if err := tx.NewRaw(candidates+`, matched AS (
			SELECT *,count(*) OVER() AS total FROM filtered ORDER BY metadata_result_id,cover_sha256 LIMIT ? OFFSET ?
		) SELECT metadata_result_id,media_variant_id,encode(cover_sha256,'hex') AS sha256,cover_mime AS mime,cover_size_bytes AS size,''::text AS width_text,''::text AS height_text,label,total FROM matched`, query, pattern, request.Limit, request.Offset).Scan(ctx, &page.Items); err != nil {
			return fmt.Errorf("read saved cover candidate options: %w", err)
		}
		for i := range page.Items {
			item := &page.Items[i]
			item.Width = parsePositiveInt(item.WidthText)
			item.Height = parsePositiveInt(item.HeightText)
			page.Total = item.Total
		}
		return nil
	})
	return page, err
}

func (repository *LibraryRepository) ReadLibraryCoverCandidateImage(ctx context.Context, id uuid.UUID) (*LibraryCoverCandidateOption, error) {
	var result LibraryCoverCandidateOption
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`SELECT id AS metadata_result_id,encode(cover_sha256,'hex') AS sha256,cover_mime AS mime,cover_size_bytes AS size FROM media_metadata_result WHERE id=? AND cover_observed IS TRUE AND cover_data IS NOT NULL AND octet_length(cover_data)>0`, id).Scan(ctx, &result); err != nil {
			return err
		}
		return tx.NewRaw(`SELECT cover_data AS data FROM media_metadata_result WHERE id=?`, id).Scan(ctx, &result)
	})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read saved cover candidate image: %w", err)
	}
	return &result, nil
}

func normalizeLibraryOptionSearch(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(norm.NFKC.String(value))), " "))
}
func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}
func rowsTotal(rows []LibraryRecordingOption) int {
	if len(rows) == 0 {
		return 0
	}
	return rows[0].Total
}
func firstOptionTag(tags map[string][]string, keys ...string) string {
	for _, key := range keys {
		for k, values := range tags {
			if strings.EqualFold(k, key) && len(values) > 0 && strings.TrimSpace(values[0]) != "" {
				return values[0]
			}
		}
	}
	return ""
}
func durationMilliseconds(value string) (int64, bool) {
	var seconds float64
	if _, err := fmt.Sscan(value, &seconds); err != nil || seconds < 0 {
		return 0, false
	}
	return int64(seconds*1000 + 0.5), true
}
func parsePositiveInt(value string) *int {
	var parsed int
	if _, err := fmt.Sscan(value, &parsed); err != nil || parsed <= 0 {
		return nil
	}
	return &parsed
}
