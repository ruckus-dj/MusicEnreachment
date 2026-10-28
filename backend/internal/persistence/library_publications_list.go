package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type LibraryPublicationListRequest struct {
	Query  string
	Limit  int
	Offset int
}

type LibraryPublicationListCredit struct {
	ArtistID   *uuid.UUID
	ArtistName string
	CreditName string
	JoinPhrase string
}

type LibraryPublicationListAudio struct {
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

type LibraryPublicationListItem struct {
	ReleaseID       uuid.UUID
	PositionID      uuid.UUID
	ReleaseTitle    string
	Title           string
	Artists         []LibraryPublicationListCredit
	Medium          int
	Position        int
	DisplayedNumber string
	OutputRoot      *string
	OutputEpoch     uuid.UUID
	RelativePath    string
	Generation      uuid.UUID
	ValidatedAt     string
	Audio           LibraryPublicationListAudio
}

type LibraryPublicationListPage struct {
	Items  []LibraryPublicationListItem
	Total  int
	Limit  int
	Offset int
}

// ReadLibraryPublicationsList reads only the current publication projection and
// its matching output namespace settings from one read-only repeatable snapshot.
func (repository *LibraryRepository) ReadLibraryPublicationsList(ctx context.Context, request LibraryPublicationListRequest) (LibraryPublicationListPage, error) {
	page := LibraryPublicationListPage{Items: make([]LibraryPublicationListItem, 0), Limit: request.Limit, Offset: request.Offset}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var epoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
			return fmt.Errorf("read publication namespace epoch: %w", err)
		}
		var outputRoot sql.NullString
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='output_directory'`).Scan(ctx, &outputRoot); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read publication output directory: %w", err)
		}
		var root *string
		if outputRoot.Valid && strings.TrimSpace(outputRoot.String) != "" {
			rootValue := outputRoot.String
			root = &rootValue
		}
		filter := `(?='' OR ` + normalizedContains("r.title", "?") + ` OR ` + normalizedContains("t.title", "?") + ` OR ` + normalizedContains("rt.displayed_number", "?") + ` OR EXISTS (SELECT 1 FROM track_artist ta LEFT JOIN local_artist la ON la.id=ta.artist_id WHERE ta.track_id=rt.track_id AND (` + normalizedContains("ta.name", "?") + ` OR ` + normalizedContains("la.name", "?") + `)))`
		args := []any{request.Query, request.Query, request.Query, request.Query, request.Query, request.Query}
		if err := tx.NewRaw(`SELECT count(*) FROM track_publication tp JOIN release_track rt ON rt.id=tp.position_id JOIN local_release r ON r.id=rt.release_id JOIN local_track t ON t.id=rt.track_id WHERE `+filter, args...).Scan(ctx, &page.Total); err != nil {
			return fmt.Errorf("count current library publications: %w", err)
		}
		type row struct {
			ReleaseID       uuid.UUID       `bun:"release_id"`
			PositionID      uuid.UUID       `bun:"position_id"`
			ReleaseTitle    string          `bun:"release_title"`
			Title           string          `bun:"title"`
			Medium          int             `bun:"medium"`
			Position        int             `bun:"position"`
			DisplayedNumber string          `bun:"displayed_number"`
			Epoch           uuid.UUID       `bun:"epoch"`
			RelativePath    string          `bun:"relative_path"`
			Generation      uuid.UUID       `bun:"generation"`
			ValidatedAt     string          `bun:"validated_at"`
			ActualAudio     json.RawMessage `bun:"actual_audio"`
			TrackID         uuid.UUID       `bun:"track_id"`
		}
		rows := make([]row, 0)
		if err := tx.NewRaw(`SELECT r.id AS release_id,rt.id AS position_id,r.title AS release_title,t.title,rt.medium,rt.position,rt.displayed_number,tp.epoch,tp.relative_path,tp.generation,tp.validated_at::text,tp.actual_audio,rt.track_id FROM track_publication tp JOIN release_track rt ON rt.id=tp.position_id JOIN local_release r ON r.id=rt.release_id JOIN local_track t ON t.id=rt.track_id WHERE `+filter+` ORDER BY lower(btrim(regexp_replace(normalize(r.title,NFKC),'[[:space:]]+',' ','g'))),r.id,rt.medium,rt.position,rt.id LIMIT ? OFFSET ?`, append(args, request.Limit, request.Offset)...).Scan(ctx, &rows); err != nil {
			return fmt.Errorf("read current library publication page: %w", err)
		}
		trackIDs := make([]uuid.UUID, 0, len(rows))
		itemsByTrack := make(map[uuid.UUID][]int)
		for i, row := range rows {
			audio, err := decodeLibraryPublicationListAudio(row.ActualAudio)
			if err != nil {
				return err
			}
			item := LibraryPublicationListItem{
				ReleaseID: row.ReleaseID, PositionID: row.PositionID, ReleaseTitle: row.ReleaseTitle,
				Title: row.Title, Artists: make([]LibraryPublicationListCredit, 0), Medium: row.Medium + 1,
				Position: row.Position + 1, DisplayedNumber: row.DisplayedNumber,
				OutputEpoch: row.Epoch, RelativePath: row.RelativePath, Generation: row.Generation,
				ValidatedAt: row.ValidatedAt, Audio: audio,
			}
			if row.Epoch == epoch && root != nil {
				rootValue := *root
				item.OutputRoot = &rootValue
			}
			page.Items = append(page.Items, item)
			if _, found := itemsByTrack[row.TrackID]; !found {
				trackIDs = append(trackIDs, row.TrackID)
			}
			itemsByTrack[row.TrackID] = append(itemsByTrack[row.TrackID], i)
		}
		if len(trackIDs) > 0 {
			var credits []struct {
				TrackID uuid.UUID `bun:"track_id"`
				LibraryPublicationListCredit
			}
			if err := tx.NewRaw(`SELECT ta.track_id,ta.artist_id,a.name AS artist_name,ta.name AS credit_name,ta.name_join_phrase AS join_phrase FROM track_artist ta LEFT JOIN local_artist a ON a.id=ta.artist_id WHERE ta.track_id IN (?) ORDER BY ta.track_id,ta.position`, bun.List(trackIDs)).Scan(ctx, &credits); err != nil {
				return fmt.Errorf("read current publication credits: %w", err)
			}
			for _, credit := range credits {
				for _, index := range itemsByTrack[credit.TrackID] {
					page.Items[index].Artists = append(page.Items[index].Artists, credit.LibraryPublicationListCredit)
				}
			}
		}
		return nil
	})
	return page, err
}

// decodeLibraryPublicationListAudio projects the stored current audio contract
// through the strict admission decoder so that a missing, null or malformed fact
// set is reported instead of being surfaced as zeroed audio.
func decodeLibraryPublicationListAudio(raw json.RawMessage) (LibraryPublicationListAudio, error) {
	var contract PublicationCurrentAudio
	if err := decodePublicationCurrentContract(raw, &contract); err != nil {
		return LibraryPublicationListAudio{}, fmt.Errorf("decode current publication audio facts: %w", err)
	}
	facts := contract.Facts
	return LibraryPublicationListAudio{
		StreamIndex: facts.StreamIndex, Codec: facts.Codec, Lossless: facts.Lossless,
		Channels: facts.Channels, Bitrate: facts.Bitrate, BitDepth: facts.BitDepth,
		SampleRate: facts.SampleRate, Container: string(facts.Container),
		SupportedSourceContainer: facts.SupportedSourceContainer,
		ALACCapabilitiesVerified: facts.ALACCapabilitiesVerified,
	}, nil
}

func normalizedContains(column, query string) string {
	return `strpos(lower(btrim(regexp_replace(normalize(` + column + `,NFKC),'[[:space:]]+',' ','g'))),lower(btrim(regexp_replace(normalize(` + query + `::text,NFKC),'[[:space:]]+',' ','g'))))>0`
}
