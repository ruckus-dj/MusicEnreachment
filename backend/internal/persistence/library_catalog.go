package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type LibraryCatalogRequest struct {
	Limit, Offset       int
	Query, Publication  string
	ArtistID, ReleaseID *uuid.UUID
}

type LibraryCatalogSummary struct {
	ArtistCount            int `json:"artist_count"`
	ReleaseCount           int `json:"release_count"`
	PositionCount          int `json:"position_count"`
	PublishedPositionCount int `json:"published_position_count"`
}

type LibraryCatalogProviderLink struct {
	ProviderCode string `json:"provider_code"`
	ProviderKey  string `json:"provider_key"`
	Method       string `json:"method"`
}

type LibraryCatalogArtist struct {
	ID                     uuid.UUID                    `json:"id" bun:"id"`
	Name                   string                       `json:"name" bun:"name"`
	Disambiguation         *string                      `json:"disambiguation" bun:"disambiguation"`
	Genres                 []string                     `json:"genres" bun:"-"`
	ProviderLinks          []LibraryCatalogProviderLink `json:"provider_links" bun:"-"`
	ReleaseCount           int                          `json:"release_count" bun:"release_count"`
	PositionCount          int                          `json:"position_count" bun:"position_count"`
	PublishedPositionCount int                          `json:"published_position_count" bun:"published_position_count"`
	Total                  int                          `json:"-" bun:"total"`
	GenresJSON             string                       `json:"-" bun:"genres"`
}

type LibraryCatalogCredit struct {
	ArtistID   *uuid.UUID `json:"artist_id"`
	ArtistName string     `json:"artist_name"`
	CreditName string     `json:"credit_name"`
	JoinPhrase string     `json:"join_phrase"`
}

type LibraryCatalogEdition struct {
	ReleaseDate         *string  `json:"release_date"`
	OriginalReleaseDate *string  `json:"original_release_date"`
	Country             *string  `json:"country"`
	Labels              []string `json:"labels"`
	CatalogNumbers      []string `json:"catalog_numbers"`
	PrimaryType         *string  `json:"primary_type"`
	SecondaryTypes      []string `json:"secondary_types"`
	Disambiguation      *string  `json:"disambiguation"`
}

type LibraryCatalogCover struct {
	ReleaseID          uuid.UUID  `json:"release_id"`
	Mode               string     `json:"mode"`
	Revision           int64      `json:"revision"`
	SelectedGeneration *uuid.UUID `json:"selected_generation"`
	SelectedMIME       *string    `json:"selected_mime"`
	SelectedSHA256     *string    `json:"selected_sha256"`
	SelectedSize       *int64     `json:"selected_size"`
	PendingGeneration  *uuid.UUID `json:"pending_generation"`
	PendingMIME        *string    `json:"pending_mime"`
	PendingSHA256      *string    `json:"pending_sha256"`
	PendingSize        *int64     `json:"pending_size"`
	Warning            *string    `json:"warning"`
}

type LibraryCatalogReleaseCounts struct {
	PositionCount          int  `json:"position_count"`
	PublishedPositionCount int  `json:"published_position_count"`
	AvailablePositionCount int  `json:"available_position_count"`
	ProviderPositionCount  *int `json:"provider_position_count"`
}

type LibraryCatalogRelease struct {
	ID                       uuid.UUID                    `json:"id" bun:"id"`
	Title                    string                       `json:"title" bun:"title"`
	Artists                  []LibraryCatalogCredit       `json:"artists" bun:"-"`
	Edition                  LibraryCatalogEdition        `json:"edition" bun:"-"`
	ProviderLinks            []LibraryCatalogProviderLink `json:"provider_links" bun:"-"`
	Cover                    LibraryCatalogCover          `json:"cover" bun:"-"`
	Counts                   LibraryCatalogReleaseCounts  `json:"counts" bun:"-"`
	Metadata                 json.RawMessage              `json:"-" bun:"metadata"`
	PositionCountDB          int                          `json:"-" bun:"position_count"`
	PublishedPositionCountDB int                          `json:"-" bun:"published_position_count"`
	AvailablePositionCountDB int                          `json:"-" bun:"available_position_count"`
	Total                    int                          `json:"-" bun:"total"`
}

type LibraryCatalogPositionRelease struct {
	ID      uuid.UUID              `json:"id"`
	Title   string                 `json:"title"`
	Artists []LibraryCatalogCredit `json:"artists"`
}

type LibraryCatalogPublicationAudio struct {
	StreamIndex              int    `json:"stream_index"`
	Codec                    string `json:"codec"`
	Lossless                 *bool  `json:"lossless"`
	Channels                 *int   `json:"channels,omitempty"`
	Bitrate                  *int   `json:"bitrate,omitempty"`
	BitDepth                 *int   `json:"bit_depth,omitempty"`
	SampleRate               *int   `json:"sample_rate,omitempty"`
	Container                string `json:"container"`
	SupportedSourceContainer bool   `json:"supported_source_container"`
	ALACCapabilitiesVerified bool   `json:"alac_capabilities_verified"`
}

type LibraryCatalogPublication struct {
	Epoch        uuid.UUID                      `json:"epoch"`
	Generation   uuid.UUID                      `json:"generation"`
	RelativePath string                         `json:"relative_path"`
	ValidatedAt  string                         `json:"validated_at"`
	Audio        LibraryCatalogPublicationAudio `json:"audio"`
}

type LibraryCatalogPosition struct {
	ID                     uuid.UUID                     `json:"id" bun:"id"`
	LocalTrackID           uuid.UUID                     `json:"local_track_id" bun:"track_id"`
	RecordingID            *uuid.UUID                    `json:"recording_id" bun:"recording_id"`
	Release                LibraryCatalogPositionRelease `json:"release" bun:"-"`
	Medium                 int                           `json:"medium" bun:"medium"`
	Position               int                           `json:"position" bun:"position"`
	DisplayedNumber        string                        `json:"displayed_number" bun:"displayed_number"`
	Title                  string                        `json:"title" bun:"title"`
	Artists                []LibraryCatalogCredit        `json:"artists" bun:"-"`
	ProviderDurationMS     *int64                        `json:"provider_duration_ms" bun:"-"`
	SourceVariantCount     int                           `json:"source_variant_count" bun:"-"`
	SelectedMediaVariantID *uuid.UUID                    `json:"selected_media_variant_id" bun:"-"`
	Publication            *LibraryCatalogPublication    `json:"publication" bun:"-"`
	ReleaseID              uuid.UUID                     `json:"-" bun:"release_id"`
	ReleaseTitle           string                        `json:"-" bun:"release_title"`
	Total                  int                           `json:"-" bun:"total"`
}

type LibraryCatalogArtistPage struct {
	Items   []LibraryCatalogArtist
	Total   int
	Summary LibraryCatalogSummary
}
type LibraryCatalogReleasePage struct {
	Items   []LibraryCatalogRelease
	Total   int
	Summary LibraryCatalogSummary
}
type LibraryCatalogPositionPage struct {
	Items   []LibraryCatalogPosition
	Total   int
	Summary LibraryCatalogSummary
}

func (repository *LibraryRepository) ReadLibraryArtists(ctx context.Context, request LibraryCatalogRequest) (LibraryCatalogArtistPage, error) {
	var page LibraryCatalogArtistPage
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := readLibraryCatalogSummary(ctx, tx, &page.Summary); err != nil {
			return err
		}
		rows := make([]LibraryCatalogArtist, 0)
		query := `WITH artist_rows AS (
 SELECT a.id,a.name,NULLIF(a.metadata->>'disambiguation','') AS disambiguation,
 COALESCE((SELECT jsonb_agg(x->>'name' ORDER BY n) FROM jsonb_array_elements(COALESCE(a.metadata->'genres','[]'::jsonb)) WITH ORDINALITY g(x,n)), '[]'::jsonb) AS genres,
				(SELECT count(DISTINCT ra.release_id) FROM release_artist ra WHERE ra.artist_id=a.id) AS release_count,
				(SELECT count(DISTINCT rt.id) FROM release_track rt JOIN release_artist ra ON ra.release_id=rt.release_id WHERE ra.artist_id=a.id) AS position_count,
				(SELECT count(DISTINCT rt.id) FROM release_track rt JOIN release_artist ra ON ra.release_id=rt.release_id JOIN track_publication tp ON tp.position_id=rt.id WHERE ra.artist_id=a.id) AS published_position_count
 FROM local_artist a
)
SELECT id,name,disambiguation,genres::text AS genres,release_count,position_count,published_position_count,
 count(*) OVER() AS total FROM artist_rows
			WHERE (?='' OR strpos(lower(btrim(regexp_replace(normalize(name,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0 OR EXISTS (SELECT 1 FROM jsonb_array_elements(COALESCE((SELECT metadata->'genres' FROM local_artist WHERE id=artist_rows.id),'[]'::jsonb)) g WHERE strpos(lower(btrim(regexp_replace(normalize(g->>'name',NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0))
AND (?='all' OR (?='published' AND published_position_count>0) OR (?='unpublished' AND position_count>published_position_count))
			ORDER BY lower(btrim(regexp_replace(normalize(name,NFKC),'[[:space:]]+',' ','g'))),id LIMIT ? OFFSET ?`
		if err := tx.NewRaw(query, request.Query, request.Query, request.Query, request.Publication, request.Publication, request.Publication, request.Limit, request.Offset).Scan(ctx, &rows); err != nil {
			return fmt.Errorf("read library artist page: %w", err)
		}
		if err := tx.NewRaw(`SELECT count(*) FROM (SELECT a.id,
			(SELECT count(DISTINCT rt.id) FROM release_track rt JOIN release_artist ra ON ra.release_id=rt.release_id WHERE ra.artist_id=a.id) AS positions,
			(SELECT count(DISTINCT rt.id) FROM release_track rt JOIN release_artist ra ON ra.release_id=rt.release_id JOIN track_publication tp ON tp.position_id=rt.id WHERE ra.artist_id=a.id) AS published
				FROM local_artist a WHERE (?='' OR strpos(lower(btrim(regexp_replace(normalize(a.name,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0 OR EXISTS (SELECT 1 FROM jsonb_array_elements(COALESCE(a.metadata->'genres','[]'::jsonb)) g WHERE strpos(lower(btrim(regexp_replace(normalize(g->>'name',NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0))
			AND (?='all' OR (?='published' AND (SELECT count(DISTINCT rt.id) FROM release_track rt JOIN release_artist ra ON ra.release_id=rt.release_id JOIN track_publication tp ON tp.position_id=rt.id WHERE ra.artist_id=a.id)>0)
			OR (?='unpublished' AND (SELECT count(DISTINCT rt.id) FROM release_track rt JOIN release_artist ra ON ra.release_id=rt.release_id WHERE ra.artist_id=a.id)>(SELECT count(DISTINCT rt.id) FROM release_track rt JOIN release_artist ra ON ra.release_id=rt.release_id JOIN track_publication tp ON tp.position_id=rt.id WHERE ra.artist_id=a.id)))) matches`, request.Query, request.Query, request.Query, request.Publication, request.Publication, request.Publication).Scan(ctx, &page.Total); err != nil {
			return fmt.Errorf("count library artists: %w", err)
		}
		for i := range rows {
			if err := json.Unmarshal([]byte(rows[i].GenresJSON), &rows[i].Genres); err != nil {
				return fmt.Errorf("decode local artist genres: %w", err)
			}
			rows[i].Genres = nonNilStrings(rows[i].Genres)
			rows[i].ProviderLinks = make([]LibraryCatalogProviderLink, 0)
		}
		if err := readArtistProviderLinksBatch(ctx, tx, rows); err != nil {
			return err
		}
		page.Items = rows
		if page.Items == nil {
			page.Items = []LibraryCatalogArtist{}
		}
		return nil
	})
	return page, err
}

func (repository *LibraryRepository) ReadLibraryArtist(ctx context.Context, id uuid.UUID) (*LibraryCatalogArtist, error) {
	var result LibraryCatalogArtist
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var genres string
		err := tx.NewRaw(`SELECT a.id,a.name,NULLIF(a.metadata->>'disambiguation',''),COALESCE((SELECT jsonb_agg(g->>'name') FROM jsonb_array_elements(COALESCE(a.metadata->'genres','[]'::jsonb)) g),'[]'::jsonb)::text,
			(SELECT count(*) FROM release_artist ra WHERE ra.artist_id=a.id),
			(SELECT count(*) FROM release_track rt JOIN release_artist ra ON ra.release_id=rt.release_id WHERE ra.artist_id=a.id),
			(SELECT count(*) FROM release_track rt JOIN release_artist ra ON ra.release_id=rt.release_id JOIN track_publication tp ON tp.position_id=rt.id WHERE ra.artist_id=a.id)
			FROM local_artist a WHERE a.id=?`, id).Scan(ctx, &result.ID, &result.Name, &result.Disambiguation, &genres, &result.ReleaseCount, &result.PositionCount, &result.PublishedPositionCount)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(genres), &result.Genres); err != nil {
			return err
		}
		result.Genres = nonNilStrings(result.Genres)
		result.ProviderLinks = make([]LibraryCatalogProviderLink, 0)
		return readArtistProviderLinksBatch(ctx, tx, []LibraryCatalogArtist{result}, &result)
	})
	if err != nil {
		return nil, fmt.Errorf("read library artist: %w", err)
	}
	if result.ID == uuid.Nil {
		return nil, nil
	}
	return &result, nil
}

func (repository *LibraryRepository) ReadLibraryReleases(ctx context.Context, request LibraryCatalogRequest) (LibraryCatalogReleasePage, error) {
	var page LibraryCatalogReleasePage
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := readLibraryCatalogSummary(ctx, tx, &page.Summary); err != nil {
			return err
		}
		source, sourceFound, err := catalogMusicBrainzSource(ctx, tx)
		if err != nil {
			return err
		}
		var sourceID, providerID *uuid.UUID
		if sourceFound {
			sourceID, providerID = &source.ID, &source.ProviderID
		}
		rows := make([]LibraryCatalogRelease, 0)
		basePredicate := `(?::uuid IS NULL OR EXISTS(SELECT 1 FROM release_artist ra WHERE ra.release_id=r.id AND ra.artist_id=?)) AND
			(?='' OR strpos(lower(btrim(regexp_replace(normalize(r.title,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0
			OR EXISTS(SELECT 1 FROM release_artist ra WHERE ra.release_id=r.id AND (strpos(lower(btrim(regexp_replace(normalize(ra.name,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0 OR EXISTS(SELECT 1 FROM local_artist a WHERE a.id=ra.artist_id AND strpos(lower(btrim(regexp_replace(normalize(a.name,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0)))
			OR ((r.metadata->'local_metadata'->'desired'->'catalog_number'->>'state'='value' AND strpos(lower(btrim(regexp_replace(normalize(r.metadata->'local_metadata'->'desired'->'catalog_number'->>'value',NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0)
				OR (COALESCE(r.metadata->'local_metadata'->'desired'->'catalog_number'->>'state','') NOT IN ('value','explicit_empty') AND COALESCE(r.metadata->'local_metadata'->'desired'->'catalog_number'->>'authority','')<>'manual' AND EXISTS(SELECT 1 FROM provider_local_release pl JOIN provider_release pr ON pr.id=pl.provider_release_id AND pr.provider_id=pl.provider_id JOIN provider_source_entity pse ON pse.provider_source_id=? AND pse.provider_id=? AND pse.entity_kind='release' AND pse.provider_key=pr.provider_key AND jsonb_exists(pse.field_evidence,'label-info') CROSS JOIN LATERAL jsonb_array_elements(COALESCE(pse.fields->'label-info','[]'::jsonb)) li WHERE pl.local_release_id=r.id AND pl.provider_id=? AND strpos(lower(btrim(regexp_replace(normalize(li->>'catalog-number',NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0))))`
		baseArgs := []any{request.ArtistID, request.ArtistID, request.Query, request.Query, request.Query, request.Query, request.Query, sourceID, providerID, providerID, request.Query}
		query := `WITH release_rows AS (SELECT r.id,r.title,r.metadata,
 (SELECT count(*) FROM release_track rt WHERE rt.release_id=r.id) AS position_count,
 (SELECT count(*) FROM release_track rt JOIN track_publication tp ON tp.position_id=rt.id WHERE rt.release_id=r.id) AS published_position_count,
 (SELECT count(*) FROM release_track rt JOIN track_publication tp ON tp.position_id=rt.id WHERE rt.release_id=r.id) AS available_position_count
				 FROM local_release r WHERE ` + basePredicate + `)
 SELECT id,title,metadata::text AS metadata,position_count,published_position_count,available_position_count,count(*) OVER() AS total FROM release_rows
 WHERE (?='all' OR (?='published' AND published_position_count>0) OR (?='unpublished' AND position_count>published_position_count)) ORDER BY lower(btrim(regexp_replace(normalize(title,NFKC),'[[:space:]]+',' ','g'))),id LIMIT ? OFFSET ?`
		pageArgs := append(append([]any(nil), baseArgs...), request.Publication, request.Publication, request.Publication, request.Limit, request.Offset)
		if err := tx.NewRaw(query, pageArgs...).Scan(ctx, &rows); err != nil {
			return fmt.Errorf("read library release page: %w", err)
		}
		countQuery := `SELECT count(*) FROM (SELECT r.id,
			(SELECT count(*) FROM release_track rt WHERE rt.release_id=r.id) positions,
			(SELECT count(*) FROM release_track rt JOIN track_publication tp ON tp.position_id=rt.id WHERE rt.release_id=r.id) published
			FROM local_release r WHERE ` + basePredicate + `) q
			WHERE (?='all' OR (?='published' AND published>0) OR (?='unpublished' AND positions>published))`
		countArgs := append(append([]any(nil), baseArgs...), request.Publication, request.Publication, request.Publication)
		if err := tx.NewRaw(countQuery, countArgs...).Scan(ctx, &page.Total); err != nil {
			return fmt.Errorf("count library releases: %w", err)
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for i := range rows {
			ids = append(ids, rows[i].ID)
		}
		if len(ids) > 0 {
			type credit struct {
				ReleaseID uuid.UUID `bun:"release_id"`
				LibraryCatalogCredit
			}
			var credits []credit
			if err := tx.NewRaw(`SELECT ra.release_id,ra.artist_id,a.name AS artist_name,ra.name AS credit_name,ra.name_join_phrase AS join_phrase FROM release_artist ra LEFT JOIN local_artist a ON a.id=ra.artist_id WHERE ra.release_id IN (?) ORDER BY ra.release_id,ra.position`, bun.List(ids)).Scan(ctx, &credits); err != nil {
				return fmt.Errorf("read library release credits: %w", err)
			}
			byRelease := make(map[uuid.UUID]int, len(rows))
			for i := range rows {
				byRelease[rows[i].ID] = i
				rows[i].Artists = make([]LibraryCatalogCredit, 0)
				rows[i].ProviderLinks = make([]LibraryCatalogProviderLink, 0)
			}
			for _, credit := range credits {
				i := byRelease[credit.ReleaseID]
				rows[i].Artists = append(rows[i].Artists, credit.LibraryCatalogCredit)
			}
			type providerLink struct {
				ReleaseID uuid.UUID `bun:"local_release_id"`
				LibraryCatalogProviderLink
			}
			var providerLinks []providerLink
			if err := tx.NewRaw(`SELECT plr.local_release_id,p.code AS provider_code,pr.provider_key,plr.method FROM provider_local_release plr JOIN provider_release pr ON pr.id=plr.provider_release_id AND pr.provider_id=plr.provider_id JOIN provider p ON p.id=plr.provider_id WHERE plr.local_release_id IN (?) ORDER BY plr.local_release_id,p.code,pr.provider_key`, bun.List(ids)).Scan(ctx, &providerLinks); err != nil {
				return fmt.Errorf("read library release provider links: %w", err)
			}
			for _, link := range providerLinks {
				i := byRelease[link.ReleaseID]
				rows[i].ProviderLinks = append(rows[i].ProviderLinks, link.LibraryCatalogProviderLink)
			}
			covers := make([]struct {
				ReleaseID uuid.UUID `bun:"release_id"`
				LibraryCatalogCover
			}, 0)
			if err := tx.NewRaw(`SELECT release_id,mode,revision,selected_generation,selected_mime,encode(selected_sha256,'hex') AS selected_sha256,selected_size,pending_generation,pending_mime,encode(pending_sha256,'hex') AS pending_sha256,pending_size,warning FROM release_publication_cover WHERE release_id IN (?)`, bun.List(ids)).Scan(ctx, &covers); err != nil {
				return fmt.Errorf("read library release covers: %w", err)
			}
			for i := range rows {
				rows[i].Cover = LibraryCatalogCover{ReleaseID: rows[i].ID, Mode: "automatic", Revision: 1}
			}
			for _, cover := range covers {
				i := byRelease[cover.ReleaseID]
				rows[i].Cover = cover.LibraryCatalogCover
				rows[i].Cover.ReleaseID = cover.ReleaseID
			}
		}
		for i := range rows {
			rows[i].Counts = LibraryCatalogReleaseCounts{PositionCount: rows[i].PositionCountDB, PublishedPositionCount: rows[i].PublishedPositionCountDB, AvailablePositionCount: rows[i].PublishedPositionCountDB}
			rows[i].Counts.ProviderPositionCount = nil
			rows[i].Edition = libraryCatalogLocalEdition(rows[i].Metadata)
		}
		if err := enrichLibraryCatalogReleaseProvider(ctx, tx, rows); err != nil {
			return err
		}
		page.Items = rows
		if page.Items == nil {
			page.Items = []LibraryCatalogRelease{}
		}
		return nil
	})
	return page, err
}

func (repository *LibraryRepository) ReadLibraryPositions(ctx context.Context, request LibraryCatalogRequest) (LibraryCatalogPositionPage, error) {
	var page LibraryCatalogPositionPage
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := readLibraryCatalogSummary(ctx, tx, &page.Summary); err != nil {
			return err
		}
		// Selection, filtering and pagination are performed in SQL; enrich the selected page in batches below.
		rows := make([]LibraryCatalogPosition, 0)
		query := `WITH rows AS (SELECT rt.id,rt.track_id,rt.recording_id,r.id AS release_id,r.title AS release_title,rt.medium,rt.position,rt.displayed_number,t.title,
 EXISTS(SELECT 1 FROM track_publication tp WHERE tp.position_id=rt.id) AS published
 FROM release_track rt JOIN local_release r ON r.id=rt.release_id JOIN local_track t ON t.id=rt.track_id
 WHERE (?::uuid IS NULL OR r.id=?) AND (?::uuid IS NULL OR EXISTS(SELECT 1 FROM release_artist ra WHERE ra.release_id=r.id AND ra.artist_id=?))
	 AND (?='' OR strpos(lower(btrim(regexp_replace(normalize(t.title,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0 OR strpos(lower(btrim(regexp_replace(normalize(rt.displayed_number,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0 OR strpos(lower(btrim(regexp_replace(normalize(r.title,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0 OR EXISTS(SELECT 1 FROM track_artist ta LEFT JOIN local_artist a ON a.id=ta.artist_id WHERE ta.track_id=rt.track_id AND (strpos(lower(btrim(regexp_replace(normalize(ta.name,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0 OR strpos(lower(btrim(regexp_replace(normalize(a.name,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0)))
	 ) SELECT id,track_id,recording_id,release_id,release_title,medium,position,displayed_number,title,count(*) OVER() AS total FROM rows WHERE (?='all' OR (?='published' AND published) OR (?='unpublished' AND NOT published)) ORDER BY lower(btrim(regexp_replace(normalize(release_title,NFKC),'[[:space:]]+',' ','g'))),release_id,medium,position,id LIMIT ? OFFSET ?`
		if err := tx.NewRaw(query, request.ReleaseID, request.ReleaseID, request.ArtistID, request.ArtistID, request.Query, request.Query, request.Query, request.Query, request.Query, request.Query, request.Publication, request.Publication, request.Publication, request.Limit, request.Offset).Scan(ctx, &rows); err != nil {
			return fmt.Errorf("read library position page: %w", err)
		}
		if err := tx.NewRaw(`SELECT count(*) FROM release_track rt JOIN local_release r ON r.id=rt.release_id JOIN local_track t ON t.id=rt.track_id
			WHERE (?::uuid IS NULL OR r.id=?) AND (?::uuid IS NULL OR EXISTS(SELECT 1 FROM release_artist ra WHERE ra.release_id=r.id AND ra.artist_id=?))
			AND (?='' OR strpos(lower(btrim(regexp_replace(normalize(t.title,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0 OR strpos(lower(btrim(regexp_replace(normalize(rt.displayed_number,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0 OR strpos(lower(btrim(regexp_replace(normalize(r.title,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0 OR EXISTS(SELECT 1 FROM track_artist ta LEFT JOIN local_artist a ON a.id=ta.artist_id WHERE ta.track_id=rt.track_id AND (strpos(lower(btrim(regexp_replace(normalize(ta.name,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0 OR strpos(lower(btrim(regexp_replace(normalize(a.name,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0)))
			AND (?='all' OR (?='published' AND EXISTS(SELECT 1 FROM track_publication tp WHERE tp.position_id=rt.id)) OR (?='unpublished' AND NOT EXISTS(SELECT 1 FROM track_publication tp WHERE tp.position_id=rt.id)))`, request.ReleaseID, request.ReleaseID, request.ArtistID, request.ArtistID, request.Query, request.Query, request.Query, request.Query, request.Query, request.Query, request.Publication, request.Publication, request.Publication).Scan(ctx, &page.Total); err != nil {
			return fmt.Errorf("count library positions: %w", err)
		}
		trackIDs := make([]uuid.UUID, 0, len(rows))
		releaseIDs := make([]uuid.UUID, 0, len(rows))
		positionIDs := make([]uuid.UUID, 0, len(rows))
		for i := range rows {
			rows[i].Release = LibraryCatalogPositionRelease{ID: rows[i].ReleaseID, Title: rows[i].ReleaseTitle, Artists: make([]LibraryCatalogCredit, 0)}
			rows[i].Medium++
			rows[i].Position++
			rows[i].Artists = make([]LibraryCatalogCredit, 0)
			rows[i].Release.Artists = make([]LibraryCatalogCredit, 0)
			trackIDs = append(trackIDs, rows[i].LocalTrackID)
			releaseIDs = append(releaseIDs, rows[i].ReleaseID)
			positionIDs = append(positionIDs, rows[i].ID)
		}
		if len(rows) > 0 {
			type credit struct {
				TrackID uuid.UUID `bun:"track_id"`
				LibraryCatalogCredit
			}
			var credits []credit
			if err := tx.NewRaw(`SELECT ta.track_id,ta.artist_id,a.name AS artist_name,ta.name AS credit_name,ta.name_join_phrase AS join_phrase FROM track_artist ta LEFT JOIN local_artist a ON a.id=ta.artist_id WHERE ta.track_id IN (?) ORDER BY ta.track_id,ta.position`, bun.List(trackIDs)).Scan(ctx, &credits); err != nil {
				return fmt.Errorf("read library position credits: %w", err)
			}
			trackIndex := map[uuid.UUID][]int{}
			releaseIndex := map[uuid.UUID][]int{}
			for i := range rows {
				trackIndex[rows[i].LocalTrackID] = append(trackIndex[rows[i].LocalTrackID], i)
				releaseIndex[rows[i].ReleaseID] = append(releaseIndex[rows[i].ReleaseID], i)
			}
			for _, credit := range credits {
				for _, i := range trackIndex[credit.TrackID] {
					rows[i].Artists = append(rows[i].Artists, credit.LibraryCatalogCredit)
				}
			}
			type releaseCredit struct {
				ReleaseID uuid.UUID `bun:"release_id"`
				LibraryCatalogCredit
			}
			var releaseCredits []releaseCredit
			if err := tx.NewRaw(`SELECT ra.release_id,ra.artist_id,a.name AS artist_name,ra.name AS credit_name,ra.name_join_phrase AS join_phrase FROM release_artist ra LEFT JOIN local_artist a ON a.id=ra.artist_id WHERE ra.release_id IN (?) ORDER BY ra.release_id,ra.position`, bun.List(releaseIDs)).Scan(ctx, &releaseCredits); err != nil {
				return fmt.Errorf("read library position release credits: %w", err)
			}
			for _, credit := range releaseCredits {
				for _, i := range releaseIndex[credit.ReleaseID] {
					rows[i].Release.Artists = append(rows[i].Release.Artists, credit.LibraryCatalogCredit)
				}
			}
			type positionData struct {
				PositionID  uuid.UUID       `bun:"position_id"`
				SourceCount int             `bun:"source_variant_count"`
				SelectedID  *uuid.UUID      `bun:"selected_media_variant_id"`
				Publication json.RawMessage `bun:"publication"`
			}
			var data []positionData
			if err := tx.NewRaw(`SELECT rt.id AS position_id,(SELECT count(*) FROM track_source ts WHERE ts.track_id=rt.track_id) AS source_variant_count,(SELECT mv.id FROM track_source ts JOIN media_variant mv ON mv.id=ts.media_variant_id WHERE ts.track_id=rt.track_id AND ts.selected ORDER BY mv.id LIMIT 1) AS selected_media_variant_id,
				(SELECT jsonb_build_object('epoch',tp.epoch,'generation',tp.generation,'relative_path',tp.relative_path,'validated_at',tp.validated_at,'audio',tp.actual_audio->'facts') FROM track_publication tp WHERE tp.position_id=rt.id) AS publication
				FROM release_track rt WHERE rt.id IN (?)`, bun.List(positionIDs)).Scan(ctx, &data); err != nil {
				return fmt.Errorf("read library position availability: %w", err)
			}
			positionIndex := make(map[uuid.UUID]int, len(rows))
			for i := range rows {
				positionIndex[rows[i].ID] = i
			}
			for _, value := range data {
				i, ok := positionIndex[value.PositionID]
				if !ok {
					continue
				}
				rows[i].SourceVariantCount = value.SourceCount
				rows[i].SelectedMediaVariantID = value.SelectedID
				if len(value.Publication) > 0 && string(value.Publication) != "null" {
					var publication LibraryCatalogPublication
					if err := json.Unmarshal(value.Publication, &publication); err != nil {
						return fmt.Errorf("decode library publication: %w", err)
					}
					rows[i].Publication = &publication
				}
			}
		}
		if err := enrichLibraryCatalogPositionProvider(ctx, tx, rows); err != nil {
			return err
		}
		page.Items = rows
		if page.Items == nil {
			page.Items = []LibraryCatalogPosition{}
		}
		return nil
	})
	return page, err
}

func readLibraryCatalogSummary(ctx context.Context, tx bun.Tx, summary *LibraryCatalogSummary) error {
	return tx.NewRaw(`SELECT (SELECT count(*) FROM local_artist),(SELECT count(*) FROM local_release),(SELECT count(*) FROM release_track),(SELECT count(*) FROM track_publication)`).Scan(ctx, &summary.ArtistCount, &summary.ReleaseCount, &summary.PositionCount, &summary.PublishedPositionCount)
}

func readArtistProviderLinks(ctx context.Context, tx bun.Tx, id uuid.UUID, links *[]LibraryCatalogProviderLink) error {
	return tx.NewRaw(`SELECT p.code AS provider_code,pa.provider_key,pla.method FROM provider_local_artist pla JOIN provider_artist pa ON pa.id=pla.provider_artist_id AND pa.provider_id=pla.provider_id JOIN provider p ON p.id=pla.provider_id WHERE pla.local_artist_id=? ORDER BY p.code,pa.provider_key`, id).Scan(ctx, links)
}

func readArtistProviderLinksBatch(ctx context.Context, tx bun.Tx, artists []LibraryCatalogArtist, single ...*LibraryCatalogArtist) error {
	if len(single) > 0 {
		return readArtistProviderLinks(ctx, tx, single[0].ID, &single[0].ProviderLinks)
	}
	if len(artists) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(artists))
	index := make(map[uuid.UUID]int, len(artists))
	for i := range artists {
		ids = append(ids, artists[i].ID)
		index[artists[i].ID] = i
	}
	type link struct {
		ArtistID uuid.UUID `bun:"local_artist_id"`
		LibraryCatalogProviderLink
	}
	var links []link
	if err := tx.NewRaw(`SELECT pla.local_artist_id,p.code AS provider_code,pa.provider_key,pla.method FROM provider_local_artist pla JOIN provider_artist pa ON pa.id=pla.provider_artist_id AND pa.provider_id=pla.provider_id JOIN provider p ON p.id=pla.provider_id WHERE pla.local_artist_id IN (?) ORDER BY pla.local_artist_id,p.code,pa.provider_key`, bun.List(ids)).Scan(ctx, &links); err != nil {
		return err
	}
	for _, value := range links {
		i := index[value.ArtistID]
		artists[i].ProviderLinks = append(artists[i].ProviderLinks, value.LibraryCatalogProviderLink)
	}
	return nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
