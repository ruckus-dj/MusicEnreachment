package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var ErrLibraryReleaseGroupEditionNotFound = errors.New("library release group edition not found")

type LibraryReleaseGroupEdition struct {
	ProviderKey string
	Title       string
	Artists     []LibraryCatalogCredit
	Edition     LibraryCatalogEdition
	TrackCount  *int
}

type LibraryReleaseGroupEditions struct {
	ProviderCode string
	ProviderKey  string
	Items        []LibraryReleaseGroupEdition
	Total        int
}

type LibraryReleaseGroupTrack struct {
	Medium         int
	Position       int
	Number         string
	Title          string
	RecordingTitle *string
	DurationMS     *int64
}

type LibraryReleaseGroupEditionTracks struct {
	ProviderCode       string
	GroupProviderKey   string
	ReleaseProviderKey string
	Complete           *bool
	TrackCount         *int
	Tracks             []LibraryReleaseGroupTrack
}

type LibraryCatalogMissingReleaseGroup struct {
	ProviderCode        string
	ProviderKey         string
	Title               string
	Artists             []LibraryCatalogCredit
	OriginalReleaseDate *string
}

type LibraryCatalogReleaseItem struct {
	Kind                string
	LibraryRelease      *LibraryCatalogRelease
	MissingReleaseGroup *LibraryCatalogMissingReleaseGroup
}

func (repository *LibraryRepository) ReadLibraryReleaseGroupEditions(ctx context.Context, groupID uuid.UUID, limit, offset int) (LibraryReleaseGroupEditions, error) {
	result := LibraryReleaseGroupEditions{
		ProviderCode: "musicbrainz",
		ProviderKey:  strings.ToLower(groupID.String()),
		Items:        []LibraryReleaseGroupEdition{},
	}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		source, found, err := catalogMusicBrainzSource(ctx, tx)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		groupPredicate := `e.provider_source_id=? AND e.provider_id=? AND e.entity_kind='release'
			AND jsonb_exists(e.field_evidence,'release-group')
			AND lower(e.fields#>>'{release-group,id}')=?
			AND e.fields#>>'{release-group,id}' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'`
		baseArgs := []any{source.ID, source.ProviderID, result.ProviderKey}
		if err := tx.NewRaw(`SELECT count(*) FROM provider_source_entity e WHERE `+groupPredicate, baseArgs...).Scan(ctx, &result.Total); err != nil {
			return fmt.Errorf("count release group editions: %w", err)
		}
		var rows []struct {
			ProviderKey string          `bun:"provider_key"`
			Fields      json.RawMessage `bun:"fields"`
			Evidence    json.RawMessage `bun:"field_evidence"`
			TrackCount  int             `bun:"track_count"`
			HasCount    bool            `bun:"has_count"`
		}
		query := `SELECT e.provider_key,e.fields,e.field_evidence,
			(SELECT count(*) FROM provider_source_release_track t
			 WHERE t.provider_source_id=e.provider_source_id AND t.provider_id=e.provider_id
			 AND t.release_id=r.id AND t.present
			 AND t.authority=(e.field_evidence#>>'{__tracks,authority}')::smallint
			 AND t.fetch_order=(e.field_evidence#>>'{__tracks,fetch_order}')::bigint) AS track_count,
			(e.complete AND jsonb_exists_all(e.field_evidence->'__tracks',ARRAY['authority','fetch_order'])
			 AND e.field_evidence#>>'{__tracks,authority}' IS NOT NULL
			 AND e.field_evidence#>>'{__tracks,fetch_order}' IS NOT NULL) IS TRUE AS has_count
			FROM provider_source_entity e JOIN provider_release r
			 ON r.provider_id=e.provider_id AND r.provider_key=e.provider_key
			WHERE ` + groupPredicate + `
			ORDER BY lower(CASE WHEN jsonb_exists(e.field_evidence,'title') THEN e.fields->>'title' ELSE '' END),e.provider_key LIMIT ? OFFSET ?`
		if err := tx.NewRaw(query, append(baseArgs, limit, offset)...).Scan(ctx, &rows); err != nil {
			return fmt.Errorf("read release group editions: %w", err)
		}
		for _, row := range rows {
			edition, err := libraryCatalogProviderEdition(row.Fields, row.Evidence)
			if err != nil {
				return fmt.Errorf("read release group edition metadata: %w", err)
			}
			fields, err := decodeFrozenObject(row.Fields)
			if err != nil {
				return fmt.Errorf("decode release group edition fields: %w", err)
			}
			var acceptedEvidence map[string]json.RawMessage
			if err := json.Unmarshal(row.Evidence, &acceptedEvidence); err != nil {
				return fmt.Errorf("decode release group edition evidence: %w", err)
			}
			title := ""
			if jsonbHasEvidence(acceptedEvidence, "title") {
				title, _, err = acceptedProviderString(fields, "title")
				if err != nil {
					return fmt.Errorf("read release group edition title: %w", err)
				}
			}
			var item LibraryReleaseGroupEdition
			item.ProviderKey = row.ProviderKey
			item.Title = title
			item.Edition = edition
			item.Artists = []LibraryCatalogCredit{}
			if jsonbHasEvidence(acceptedEvidence, "artist-credit") {
				item.Artists = releaseProjectionCredits(row.Fields)
				if err := attachMissingReleaseGroupArtistIDs(ctx, tx, source.ProviderID, row.Fields, &item.Artists); err != nil {
					return err
				}
			}
			if row.HasCount {
				count := row.TrackCount
				item.TrackCount = &count
			}
			result.Items = append(result.Items, item)
		}
		return nil
	})
	if err != nil {
		return LibraryReleaseGroupEditions{}, fmt.Errorf("read release group editions: %w", err)
	}
	return result, nil
}

func (repository *LibraryRepository) ReadLibraryReleaseGroupEditionTracks(ctx context.Context, groupID uuid.UUID, releaseKey string) (LibraryReleaseGroupEditionTracks, error) {
	result := LibraryReleaseGroupEditionTracks{
		ProviderCode:       "musicbrainz",
		GroupProviderKey:   strings.ToLower(groupID.String()),
		ReleaseProviderKey: strings.ToLower(releaseKey),
		Tracks:             []LibraryReleaseGroupTrack{},
	}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		source, found, err := catalogMusicBrainzSource(ctx, tx)
		if err != nil {
			return err
		}
		if !found {
			return ErrLibraryReleaseGroupEditionNotFound
		}
		var projection struct {
			ReleaseID uuid.UUID       `bun:"release_id,type:uuid"`
			Fields    json.RawMessage `bun:"fields"`
			Evidence  json.RawMessage `bun:"field_evidence"`
			Complete  bool            `bun:"complete"`
		}
		query := `SELECT r.id AS release_id,e.fields,e.field_evidence,e.complete
			FROM provider_source_entity e JOIN provider_release r
			 ON r.provider_id=e.provider_id AND r.provider_key=e.provider_key
			WHERE e.provider_source_id=? AND e.provider_id=? AND e.entity_kind='release'
			AND e.provider_key=? AND jsonb_exists(e.field_evidence,'release-group')
			AND lower(e.fields#>>'{release-group,id}')=?
			AND e.fields#>>'{release-group,id}' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'`
		err = tx.NewRaw(query, source.ID, source.ProviderID, result.ReleaseProviderKey, result.GroupProviderKey).Scan(ctx, &projection)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryReleaseGroupEditionNotFound
		}
		if err != nil {
			return err
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(projection.Evidence, &envelope); err != nil {
			return fmt.Errorf("decode release track evidence: %w", err)
		}
		if !jsonbHasEvidence(envelope, "__tracks") {
			return nil
		}
		var rank struct {
			Authority  *int64 `json:"authority"`
			FetchOrder *int64 `json:"fetch_order"`
		}
		if raw := envelope["__tracks"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &rank); err != nil {
				return fmt.Errorf("decode release track authority: %w", err)
			}
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(projection.Fields, &fields); err != nil {
			return fmt.Errorf("decode release track fields: %w", err)
		}
		var normalized []ProviderNormalizedTrack
		var normalizedEvidence ProviderFieldEvidence
		normalizedAccepted := false
		if raw := envelope["normalized_tracks"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &normalizedEvidence); err != nil {
				return fmt.Errorf("decode normalized track evidence: %w", err)
			}
			normalizedAccepted = rank.Authority != nil && rank.FetchOrder != nil &&
				normalizedEvidence.Authority == int(*rank.Authority) && normalizedEvidence.FetchOrder == *rank.FetchOrder
		}
		if normalizedAccepted {
			if err := json.Unmarshal(fields["normalized_tracks"], &normalized); err != nil {
				return fmt.Errorf("decode normalized release tracks: %w", err)
			}
		}
		if projection.Complete && rank.Authority != nil && rank.FetchOrder != nil {
			complete := true
			result.Complete = &complete
			var count int
			if err := tx.NewRaw(`SELECT count(*) FROM provider_source_release_track
				WHERE provider_source_id=? AND provider_id=? AND release_id=? AND present
				AND authority=? AND fetch_order=?`, source.ID, source.ProviderID, projection.ReleaseID, *rank.Authority, *rank.FetchOrder).Scan(ctx, &count); err != nil {
				return fmt.Errorf("count accepted release tracks: %w", err)
			}
			result.TrackCount = &count
		}
		var tracks []struct {
			TrackID       uuid.UUID       `bun:"track_id,type:uuid"`
			RecordingKey  string          `bun:"recording_key"`
			Medium        int             `bun:"medium"`
			Position      int             `bun:"position"`
			Number        string          `bun:"displayed_number"`
			RecordingData json.RawMessage `bun:"recording_fields"`
			RecordingEv   json.RawMessage `bun:"recording_evidence"`
		}
		query = `SELECT st.track_id,recording.provider_key AS recording_key,st.medium,st.position,
			st.displayed_number,COALESCE(recording_entity.fields,'{}'::jsonb) AS recording_fields,
			COALESCE(recording_entity.field_evidence,'{}'::jsonb) AS recording_evidence
			FROM provider_source_release_track st
			JOIN provider_release_track track ON track.provider_id=st.provider_id AND track.id=st.track_id
			JOIN provider_recording recording ON recording.provider_id=st.provider_id AND recording.id=st.recording_id
			LEFT JOIN provider_source_entity recording_entity ON recording_entity.provider_source_id=st.provider_source_id
			 AND recording_entity.provider_id=st.provider_id AND recording_entity.entity_kind='recording'
			 AND recording_entity.provider_key=recording.provider_key
			WHERE st.provider_source_id=? AND st.provider_id=? AND st.release_id=? AND st.present`
		args := []any{source.ID, source.ProviderID, projection.ReleaseID}
		if rank.Authority == nil || rank.FetchOrder == nil {
			return nil
		}
		query += ` AND st.authority=? AND st.fetch_order=?`
		args = append(args, *rank.Authority, *rank.FetchOrder)
		query += ` ORDER BY st.medium,st.position,st.track_id`
		if err := tx.NewRaw(query, args...).Scan(ctx, &tracks); err != nil {
			return fmt.Errorf("read accepted release tracks: %w", err)
		}
		for _, track := range tracks {
			recordingFields, err := decodeFrozenObject(track.RecordingData)
			if err != nil {
				return fmt.Errorf("decode accepted recording fields: %w", err)
			}
			recordingEvidence := make(map[string]json.RawMessage)
			if err := json.Unmarshal(track.RecordingEv, &recordingEvidence); err != nil {
				return fmt.Errorf("decode accepted recording evidence: %w", err)
			}
			recordingTitle := ""
			if jsonbHasEvidence(recordingEvidence, "title") {
				recordingTitle, _, err = acceptedProviderString(recordingFields, "title")
			}
			if err != nil {
				return fmt.Errorf("read accepted recording title: %w", err)
			}
			var recordingTitlePtr *string
			if recordingTitle != "" {
				recordingTitlePtr = &recordingTitle
			}
			item := LibraryReleaseGroupTrack{
				Medium:         track.Medium + 1,
				Position:       track.Position + 1,
				Number:         track.Number,
				Title:          recordingTitle,
				RecordingTitle: recordingTitlePtr,
			}
			for _, normalizedTrack := range normalized {
				if normalizedTrack.Medium != track.Medium || normalizedTrack.Position != track.Position || normalizedTrack.RecordingProviderKey != track.RecordingKey {
					continue
				}
				if normalizedTrack.Title != "" {
					item.Title = normalizedTrack.Title
				}
				if normalizedTrack.LengthMS != nil {
					duration := *normalizedTrack.LengthMS
					item.DurationMS = &duration
				}
				break
			}
			if item.DurationMS == nil && jsonbHasEvidence(recordingEvidence, "length") {
				if duration, present, err := acceptedProviderInt64(recordingFields, "length"); err != nil {
					return fmt.Errorf("read accepted recording duration: %w", err)
				} else if present {
					item.DurationMS = &duration
				}
			}
			result.Tracks = append(result.Tracks, item)
		}
		return nil
	})
	if err != nil {
		return LibraryReleaseGroupEditionTracks{}, fmt.Errorf("read release group edition tracks: %w", err)
	}
	return result, nil
}

func releaseProjectionCredits(fields json.RawMessage) []LibraryCatalogCredit {
	var projection map[string]json.RawMessage
	if json.Unmarshal(fields, &projection) != nil {
		return []LibraryCatalogCredit{}
	}
	var rawCredits []json.RawMessage
	if json.Unmarshal(projection["artist-credit"], &rawCredits) != nil {
		return []LibraryCatalogCredit{}
	}
	credits := make([]LibraryCatalogCredit, 0, len(rawCredits))
	for _, raw := range rawCredits {
		var credit struct {
			Name       string `json:"name"`
			JoinPhrase string `json:"joinphrase"`
			Artist     struct {
				Name string `json:"name"`
			} `json:"artist"`
		}
		if json.Unmarshal(raw, &credit) != nil {
			continue
		}
		artistName := credit.Artist.Name
		if artistName == "" {
			artistName = credit.Name
		}
		credits = append(credits, LibraryCatalogCredit{ArtistName: artistName, CreditName: credit.Name, JoinPhrase: credit.JoinPhrase})
	}
	return credits
}

func jsonbHasEvidence(evidence map[string]json.RawMessage, field string) bool {
	_, ok := evidence[field]
	return ok
}

func acceptedProviderInt64(fields map[string]json.RawMessage, key string) (int64, bool, error) {
	raw, exists := fields[key]
	if !exists || len(raw) == 0 || string(raw) == "null" {
		return 0, false, nil
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, true, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, false, err
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, false, err
	}
	return value, true, nil
}

func readLibraryMissingReleaseGroupCounts(ctx context.Context, tx bun.Tx) (map[uuid.UUID]int, error) {
	counts := make(map[uuid.UUID]int)
	source, found, err := catalogMusicBrainzSource(ctx, tx)
	if err != nil || !found {
		return counts, err
	}
	var rows []struct {
		ArtistID uuid.UUID `bun:"local_artist_id,type:uuid"`
		Count    int       `bun:"release_count"`
	}
	query := `WITH accepted AS (
		SELECT e.provider_id,e.provider_key,lower(e.fields#>>'{release-group,id}') AS group_key,
			e.fields,e.field_evidence
		FROM provider_source_entity e
		WHERE e.provider_source_id=? AND e.provider_id=? AND e.entity_kind='release'
		AND jsonb_exists(e.field_evidence,'release-group')
		AND e.fields#>>'{release-group,id}' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
	), group_artists AS (
		SELECT DISTINCT accepted.group_key,pla.local_artist_id
		FROM accepted
		CROSS JOIN LATERAL jsonb_array_elements(COALESCE(accepted.fields->'artist-credit','[]'::jsonb)) credit
		JOIN provider_artist pa ON pa.provider_id=accepted.provider_id
		 AND lower(pa.provider_key)=lower(credit#>>'{artist,id}')
		 AND pa.provider_key ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
		JOIN provider_local_artist pla ON pla.provider_id=pa.provider_id AND pla.provider_artist_id=pa.id
		WHERE jsonb_exists(accepted.field_evidence,'artist-credit')
		AND NOT EXISTS (
			SELECT 1 FROM provider_local_release pl
			JOIN provider_release linked ON linked.id=pl.provider_release_id AND linked.provider_id=pl.provider_id
			JOIN accepted linked_identity ON linked_identity.provider_id=linked.provider_id
			 AND linked_identity.provider_key=linked.provider_key
			WHERE pl.provider_id=accepted.provider_id AND linked_identity.group_key=accepted.group_key
		)
	)
	SELECT local_artist_id,count(DISTINCT group_key) AS release_count
	FROM group_artists GROUP BY local_artist_id`
	if err := tx.NewRaw(query, source.ID, source.ProviderID).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("read library missing release group artist counts: %w", err)
	}
	for _, row := range rows {
		counts[row.ArtistID] = row.Count
	}
	return counts, nil
}

func readMixedLibraryReleasePage(ctx context.Context, tx bun.Tx, request LibraryCatalogRequest, page *LibraryCatalogReleasePage) error {
	source, sourceFound, err := catalogMusicBrainzSource(ctx, tx)
	if err != nil {
		return err
	}
	var sourceID, providerID any
	if sourceFound {
		sourceID, providerID = source.ID, source.ProviderID
	}
	localPredicate := `(?::uuid IS NULL OR (EXISTS(SELECT 1 FROM release_artist ra WHERE ra.release_id=r.id AND ra.artist_id=?) AND EXISTS(SELECT 1 FROM local_artist requested_artist WHERE requested_artist.id=? AND requested_artist.catalog_visible IS TRUE))) AND
		(?='' OR strpos(lower(btrim(regexp_replace(normalize(r.title,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0
		OR EXISTS(SELECT 1 FROM release_artist ra WHERE ra.release_id=r.id AND (strpos(lower(btrim(regexp_replace(normalize(ra.name,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0 OR EXISTS(SELECT 1 FROM local_artist a WHERE a.id=ra.artist_id AND strpos(lower(btrim(regexp_replace(normalize(a.name,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0)))
		OR ((r.metadata->'local_metadata'->'desired'->'catalog_number'->>'state'='value' AND strpos(lower(btrim(regexp_replace(normalize(r.metadata->'local_metadata'->'desired'->'catalog_number'->>'value',NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0)
		OR (COALESCE(r.metadata->'local_metadata'->'desired'->'catalog_number'->>'state','') NOT IN ('value','explicit_empty') AND COALESCE(r.metadata->'local_metadata'->'desired'->'catalog_number'->>'authority','')<>'manual' AND EXISTS(SELECT 1 FROM provider_local_release pl JOIN provider_release pr ON pr.id=pl.provider_release_id AND pr.provider_id=pl.provider_id JOIN provider_source_entity pse ON pse.provider_source_id=? AND pse.provider_id=? AND pse.entity_kind='release' AND pse.provider_key=pr.provider_key AND jsonb_exists(pse.field_evidence,'label-info') CROSS JOIN LATERAL jsonb_array_elements(COALESCE(pse.fields->'label-info','[]'::jsonb)) li WHERE pl.local_release_id=r.id AND pl.provider_id=? AND strpos(lower(btrim(regexp_replace(normalize(li->>'catalog-number',NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0))))`
	cte := `WITH accepted AS (
		SELECT e.provider_key,e.fields,e.field_evidence,e.complete,r.id AS provider_release_id,
		lower(e.fields#>>'{release-group,id}') AS group_key
		FROM provider_source_entity e JOIN provider_release r ON r.provider_id=e.provider_id AND r.provider_key=e.provider_key
		WHERE ?::uuid IS NOT NULL AND e.provider_source_id=? AND e.provider_id=? AND e.entity_kind='release'
		AND jsonb_exists(e.field_evidence,'release-group')
		AND e.fields#>>'{release-group,id}' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
	), groups AS (
		SELECT DISTINCT ON (group_key) group_key,provider_key,fields,field_evidence,complete
		FROM accepted ORDER BY group_key,provider_key
	), local_rows AS (
		SELECT 'library_release'::text AS kind,r.id AS release_id,NULL::text AS group_key,r.title AS title,
		NULL::jsonb AS fields,NULL::jsonb AS evidence,
		(SELECT count(*) FROM release_track rt WHERE rt.release_id=r.id) AS positions,
		(SELECT count(*) FROM release_track rt JOIN track_publication tp ON tp.position_id=rt.id WHERE rt.release_id=r.id) AS published,
		(SELECT count(DISTINCT rt.id) FROM release_track rt WHERE rt.release_id=r.id AND rt.id IN (?)) AS available
		FROM local_release r WHERE ` + localPredicate + `
	), missing_rows AS (
		SELECT 'missing_release_group'::text AS kind,NULL::uuid AS release_id,g.group_key,
		COALESCE(g.fields#>>'{release-group,title}','') AS title,
		g.fields,g.field_evidence AS evidence,0::bigint AS positions,0::bigint AS published,0::bigint AS available
		FROM groups g
		WHERE NOT EXISTS (
			SELECT 1 FROM provider_local_release pl
			JOIN provider_release linked ON linked.id=pl.provider_release_id AND linked.provider_id=pl.provider_id
			JOIN accepted identity ON identity.provider_key=linked.provider_key
			WHERE pl.provider_id=? AND identity.group_key=g.group_key
		)
		AND (?::uuid IS NULL OR (EXISTS (
			SELECT 1 FROM accepted candidate
			CROSS JOIN LATERAL jsonb_array_elements(COALESCE(candidate.fields->'artist-credit','[]'::jsonb)) c
			JOIN provider_artist pa ON pa.provider_id=? AND lower(pa.provider_key)=lower(c#>>'{artist,id}')
			JOIN provider_local_artist pla ON pla.provider_id=pa.provider_id AND pla.provider_artist_id=pa.id
			WHERE candidate.group_key=g.group_key AND jsonb_exists(candidate.field_evidence,'artist-credit')
			AND pla.local_artist_id=?
		) AND EXISTS(SELECT 1 FROM local_artist requested_artist WHERE requested_artist.id=? AND requested_artist.catalog_visible IS TRUE)))
		AND (?='' OR EXISTS (
			SELECT 1 FROM accepted candidate
			WHERE candidate.group_key=g.group_key AND (
				(jsonb_exists(candidate.field_evidence,'release-group') AND strpos(lower(btrim(regexp_replace(normalize(COALESCE(candidate.fields#>>'{release-group,title}',''),NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0)
				OR (jsonb_exists(candidate.field_evidence,'artist-credit') AND EXISTS (
					SELECT 1 FROM jsonb_array_elements(COALESCE(candidate.fields->'artist-credit','[]'::jsonb)) c
					WHERE strpos(lower(btrim(regexp_replace(normalize(COALESCE(c->>'name',c#>>'{artist,name}'),NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(?::text,NFKC),'[[:space:]]+',' ','g'))))>0
				))
			)
		))
	), filtered AS (
		SELECT * FROM local_rows WHERE
		(?='all' OR (?='published' AND published>0) OR (?='unpublished' AND positions>published))
		AND (?='all' OR (?='available' AND available>0) OR (?='missing' AND available<positions))
		UNION ALL
		SELECT * FROM missing_rows WHERE ? IN ('all','missing') AND ? IN ('all','unpublished')
	)`
	availabilityIDs := bun.List(availabilityPositionIDs(request))
	args := []any{
		sourceID, sourceID, providerID,
		availabilityIDs,
		request.ArtistID, request.ArtistID, request.ArtistID,
		request.Query, request.Query, request.Query, request.Query, request.Query,
		sourceID, providerID, providerID, request.Query,
		providerID,
		request.ArtistID, providerID, request.ArtistID, request.ArtistID,
		request.Query, request.Query, request.Query,
		request.Publication, request.Publication, request.Publication,
		request.Availability, request.Availability, request.Availability,
		request.Availability, request.Publication,
	}
	if err := tx.NewRaw(cte+` SELECT count(*) FROM filtered`, args...).Scan(ctx, &page.Total); err != nil {
		return fmt.Errorf("count mixed library releases: %w", err)
	}
	var selected []struct {
		Kind     string          `bun:"kind"`
		Release  *uuid.UUID      `bun:"release_id,type:uuid"`
		GroupKey string          `bun:"group_key"`
		Title    string          `bun:"title"`
		Fields   json.RawMessage `bun:"fields"`
		Evidence json.RawMessage `bun:"evidence"`
	}
	pageArgs := append(append([]any{}, args...), request.Limit, request.Offset)
	query := cte + ` SELECT kind,release_id,group_key,title,fields,evidence FROM filtered
		ORDER BY lower(btrim(regexp_replace(normalize(title,NFKC),'[[:space:]]+',' ','g'))),kind,COALESCE(release_id::text,group_key)
		LIMIT ? OFFSET ?`
	if err := tx.NewRaw(query, pageArgs...).Scan(ctx, &selected); err != nil {
		return fmt.Errorf("read mixed library release page: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(selected))
	for _, row := range selected {
		if row.Kind == "library_release" && row.Release != nil {
			id := *row.Release
			ids = append(ids, id)
			release := LibraryCatalogRelease{ID: id, Title: row.Title}
			page.Items = append(page.Items, LibraryCatalogReleaseItem{Kind: row.Kind, LibraryRelease: &release})
			continue
		}
		missing, err := missingReleaseGroupFromProjection(row.GroupKey, row.Title, row.Fields, row.Evidence)
		if err != nil {
			return err
		}
		if err := attachMissingReleaseGroupArtistIDs(ctx, tx, providerID, row.Fields, &missing.Artists); err != nil {
			return err
		}
		page.Items = append(page.Items, LibraryCatalogReleaseItem{Kind: row.Kind, MissingReleaseGroup: &missing})
	}
	if len(ids) > 0 {
		if err := enrichCatalogReleasePage(ctx, tx, ids, request, page); err != nil {
			return err
		}
	}
	return nil
}

func missingReleaseGroupFromProjection(groupKey, title string, fields, evidence json.RawMessage) (LibraryCatalogMissingReleaseGroup, error) {
	var accepted map[string]json.RawMessage
	if err := json.Unmarshal(evidence, &accepted); err != nil {
		return LibraryCatalogMissingReleaseGroup{}, fmt.Errorf("decode missing release group evidence: %w", err)
	}
	var date *string
	if jsonbHasEvidence(accepted, "release-group") {
		var projection map[string]json.RawMessage
		if err := json.Unmarshal(fields, &projection); err != nil {
			return LibraryCatalogMissingReleaseGroup{}, fmt.Errorf("decode missing release group fields: %w", err)
		}
		var group struct {
			Date string `json:"first-release-date"`
		}
		if err := json.Unmarshal(projection["release-group"], &group); err != nil {
			return LibraryCatalogMissingReleaseGroup{}, fmt.Errorf("decode missing release group date: %w", err)
		}
		if group.Date != "" {
			date = &group.Date
		}
	}
	var artists []LibraryCatalogCredit
	if jsonbHasEvidence(accepted, "artist-credit") {
		artists = releaseProjectionCredits(fields)
	} else {
		artists = []LibraryCatalogCredit{}
	}
	return LibraryCatalogMissingReleaseGroup{
		ProviderCode:        "musicbrainz",
		ProviderKey:         groupKey,
		Title:               title,
		Artists:             artists,
		OriginalReleaseDate: date,
	}, nil
}

func attachMissingReleaseGroupArtistIDs(ctx context.Context, tx bun.Tx, providerID any, fields json.RawMessage, credits *[]LibraryCatalogCredit) error {
	if len(*credits) == 0 {
		return nil
	}
	var keys []string
	var projection map[string]json.RawMessage
	if err := json.Unmarshal(fields, &projection); err != nil {
		return fmt.Errorf("decode missing release group credits: %w", err)
	}
	var rawCredits []json.RawMessage
	if err := json.Unmarshal(projection["artist-credit"], &rawCredits); err != nil {
		return fmt.Errorf("decode missing release group artist identities: %w", err)
	}
	for _, raw := range rawCredits {
		var credit struct {
			Artist struct {
				ID string `json:"id"`
			} `json:"artist"`
		}
		if json.Unmarshal(raw, &credit) != nil {
			continue
		}
		if _, err := uuid.Parse(credit.Artist.ID); err != nil {
			keys = append(keys, "")
			continue
		}
		keys = append(keys, strings.ToLower(credit.Artist.ID))
	}
	if len(keys) == 0 {
		return nil
	}
	var bindings []struct {
		ProviderKey string    `bun:"provider_key"`
		ArtistID    uuid.UUID `bun:"artist_id,type:uuid"`
	}
	query := `SELECT lower(pa.provider_key) AS provider_key,pla.local_artist_id AS artist_id
		FROM provider_artist pa JOIN provider_local_artist pla
		 ON pla.provider_id=pa.provider_id AND pla.provider_artist_id=pa.id
		WHERE pa.provider_id=? AND lower(pa.provider_key) IN (?)`
	if err := tx.NewRaw(query, providerID, bun.List(keys)).Scan(ctx, &bindings); err != nil {
		return fmt.Errorf("read missing release group artist bindings: %w", err)
	}
	byKey := make(map[string]uuid.UUID, len(bindings))
	for _, binding := range bindings {
		byKey[binding.ProviderKey] = binding.ArtistID
	}
	for i, key := range keys {
		if key == "" || i >= len(*credits) {
			continue
		}
		id, ok := byKey[key]
		if ok {
			(*credits)[i].ArtistID = &id
		}
	}
	return nil
}

func enrichCatalogReleasePage(ctx context.Context, tx bun.Tx, ids []uuid.UUID, request LibraryCatalogRequest, page *LibraryCatalogReleasePage) error {
	rows := make([]LibraryCatalogRelease, 0, len(ids))
	if err := tx.NewRaw(`SELECT r.id,r.title,r.metadata,
		(SELECT count(*) FROM release_track rt WHERE rt.release_id=r.id) AS position_count,
		(SELECT count(*) FROM release_track rt JOIN track_publication tp ON tp.position_id=rt.id WHERE rt.release_id=r.id) AS published_position_count,
		(SELECT count(DISTINCT rt.id) FROM release_track rt WHERE rt.release_id=r.id AND rt.id IN (?)) AS available_position_count
		FROM local_release r WHERE r.id IN (?)`, bun.List(availabilityPositionIDs(request)), bun.List(ids)).Scan(ctx, &rows); err != nil {
		return fmt.Errorf("read selected library releases: %w", err)
	}
	index := make(map[uuid.UUID]int, len(rows))
	for i := range rows {
		rows[i].Artists = []LibraryCatalogCredit{}
		rows[i].ProviderLinks = []LibraryCatalogProviderLink{}
		rows[i].Cover = LibraryCatalogCover{ReleaseID: rows[i].ID, Mode: "automatic", Revision: 1}
		rows[i].Counts = LibraryCatalogReleaseCounts{PositionCount: rows[i].PositionCountDB, PublishedPositionCount: rows[i].PublishedPositionCountDB, AvailablePositionCount: rows[i].AvailablePositionCountDB}
		rows[i].Edition = libraryCatalogLocalEdition(rows[i].Metadata)
		index[rows[i].ID] = i
	}
	type artistCredit struct {
		ReleaseID uuid.UUID `bun:"release_id,type:uuid"`
		LibraryCatalogCredit
	}
	var credits []artistCredit
	if err := tx.NewRaw(`SELECT ra.release_id,ra.artist_id,a.name AS artist_name,ra.name AS credit_name,ra.name_join_phrase AS join_phrase FROM release_artist ra LEFT JOIN local_artist a ON a.id=ra.artist_id WHERE ra.release_id IN (?) ORDER BY ra.release_id,ra.position`, bun.List(ids)).Scan(ctx, &credits); err != nil {
		return fmt.Errorf("read selected library release credits: %w", err)
	}
	for _, credit := range credits {
		rows[index[credit.ReleaseID]].Artists = append(rows[index[credit.ReleaseID]].Artists, credit.LibraryCatalogCredit)
	}
	type providerLink struct {
		ReleaseID uuid.UUID `bun:"local_release_id,type:uuid"`
		LibraryCatalogProviderLink
	}
	var links []providerLink
	if err := tx.NewRaw(`SELECT plr.local_release_id,p.code AS provider_code,pr.provider_key,plr.method FROM provider_local_release plr JOIN provider_release pr ON pr.id=plr.provider_release_id AND pr.provider_id=plr.provider_id JOIN provider p ON p.id=plr.provider_id WHERE plr.local_release_id IN (?) ORDER BY plr.local_release_id,p.code,pr.provider_key`, bun.List(ids)).Scan(ctx, &links); err != nil {
		return fmt.Errorf("read selected library release links: %w", err)
	}
	for _, link := range links {
		rows[index[link.ReleaseID]].ProviderLinks = append(rows[index[link.ReleaseID]].ProviderLinks, link.LibraryCatalogProviderLink)
	}
	var covers []struct {
		ReleaseID uuid.UUID `bun:"release_id,type:uuid"`
		LibraryCatalogCover
	}
	if err := tx.NewRaw(`SELECT release_id,mode,revision,selected_generation,selected_mime,encode(selected_sha256,'hex') AS selected_sha256,selected_size,pending_generation,pending_mime,encode(pending_sha256,'hex') AS pending_sha256,pending_size,warning FROM release_publication_cover WHERE release_id IN (?)`, bun.List(ids)).Scan(ctx, &covers); err != nil {
		return fmt.Errorf("read selected library release covers: %w", err)
	}
	for _, cover := range covers {
		rows[index[cover.ReleaseID]].Cover = cover.LibraryCatalogCover
		rows[index[cover.ReleaseID]].Cover.ReleaseID = cover.ReleaseID
	}
	if err := enrichLibraryCatalogReleaseProvider(ctx, tx, rows); err != nil {
		return err
	}
	byID := make(map[uuid.UUID]LibraryCatalogRelease, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	for i := range page.Items {
		if page.Items[i].LibraryRelease == nil {
			continue
		}
		release, ok := byID[page.Items[i].LibraryRelease.ID]
		if ok {
			page.Items[i].LibraryRelease = &release
		}
	}
	return nil
}
