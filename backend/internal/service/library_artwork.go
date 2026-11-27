package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

const (
	// LibraryArtworkQueue is a dedicated bounded queue. MusicBrainz browse and
	// Cover Art Archive transfers must not compete with matching or publication
	// work for a shared worker slot.
	LibraryArtworkQueue = "library-artwork"

	// libraryArtworkDispatchPageSize bounds one DB-derived reconciliation page.
	// A sweep continues through its next offset, so a larger library is still
	// covered without holding one huge batch in memory or in a single insert.
	libraryArtworkDispatchPageSize = 50

	// LibraryArtworkDispatchInterval is how often a fresh sweep may start.
	// Artist and edition jobs are unique for their own longer periods, so a
	// frequent root tick costs reads but cannot create duplicate background work.
	LibraryArtworkDispatchInterval = time.Minute
	// libraryArtworkDispatchPeriod keeps one complete paged sweep from being
	// restarted while it is still making progress. It is also the recovery
	// horizon after a process restart.
	libraryArtworkDispatchPeriod = 5 * time.Minute

	// libraryArtistArtworkPeriod bounds how often one artist's complete
	// discography, photo, and derived editions are re-enqueued. This is recovery
	// and backfill, not a provider polling loop.
	libraryArtistArtworkPeriod = 6 * time.Hour
	// libraryEditionArtworkPeriod bounds edition re-enqueued after completion so
	// a lost cache or provider recovery is eventually revisited.
	libraryEditionArtworkPeriod = 24 * time.Hour
)

// LibraryArtworkDispatchArgs is one page of the durable DB-derived artwork
// reconciliation sweep. Offset is advanced only by the next enqueued page, so
// an interrupted sweep resumes from the last durable page job.
type LibraryArtworkDispatchArgs struct {
	Offset int `json:"offset"`
}

func (LibraryArtworkDispatchArgs) Kind() string { return "library_artwork_dispatch_v1" }

// LibraryArtistArtworkArgs names an exact local artist and the canonical
// MusicBrainz artist identity that was current when the job was enqueued. The
// worker re-reads the artist before doing any work.
type LibraryArtistArtworkArgs struct {
	ArtistID   uuid.UUID `json:"artist_id" river:"unique"`
	ArtistMBID string    `json:"artist_mbid" river:"unique"`
}

func (LibraryArtistArtworkArgs) Kind() string { return "library_artist_artwork_v1" }

// LibraryEditionArtworkArgs names one exact MusicBrainz release edition derived
// from an artist's durable discography. Front and gallery status are re-read at
// delivery rather than frozen into the args.
type LibraryEditionArtworkArgs struct {
	ArtistID    uuid.UUID `json:"artist_id" river:"unique"`
	ArtistMBID  string    `json:"artist_mbid" river:"unique"`
	ReleaseMBID string    `json:"release_mbid" river:"unique"`
}

func (LibraryEditionArtworkArgs) Kind() string { return "library_edition_artwork_v1" }

type libraryArtworkArtistPageReader interface {
	ReadLibraryArtists(context.Context, persistence.LibraryCatalogRequest) (persistence.LibraryCatalogArtistPage, error)
}

type libraryArtworkJobInserter interface {
	Insert(context.Context, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// LibraryArtworkScheduler admits durable artist and edition artwork work and
// paginates the backfill sweep. It never performs provider or filesystem I/O.
// River insertion is wired after the client exists; workers only call the
// scheduler at delivery time.
type LibraryArtworkScheduler struct {
	repository libraryArtworkArtistPageReader
	inserter   libraryArtworkJobInserter
}

func NewLibraryArtworkScheduler(repository libraryArtworkArtistPageReader) *LibraryArtworkScheduler {
	return &LibraryArtworkScheduler{repository: repository}
}

// SetInserter attaches the River client after PrepareWithWorkers has created
// it. The server only starts River after this method has run.
func (scheduler *LibraryArtworkScheduler) SetInserter(inserter libraryArtworkJobInserter) {
	if scheduler == nil {
		return
	}
	scheduler.inserter = inserter
}

// DispatchPage reads one DB page of visible library artists and enqueues only
// exact, unambiguous MusicBrainz identities. The next offset is durable: if the
// process stops after this page, the in-flight page job is retried, and a new
// root sweep restarts the backfill after the dispatch period.
func (scheduler *LibraryArtworkScheduler) DispatchPage(ctx context.Context, offset int) error {
	if scheduler == nil || scheduler.repository == nil {
		return errors.New("library artwork scheduler is unavailable")
	}
	if scheduler.inserter == nil {
		return errors.New("library artwork scheduler has no River inserter")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if offset < 0 {
		offset = 0
	}
	page, err := scheduler.repository.ReadLibraryArtists(ctx, persistence.LibraryCatalogRequest{
		Limit: libraryArtworkDispatchPageSize, Offset: offset,
	})
	if err != nil {
		return fmt.Errorf("read library artwork artist page at offset %d: %w", offset, err)
	}
	problems := make([]error, 0)
	for _, artist := range page.Items {
		if artist.ID == uuid.Nil {
			continue
		}
		mbid := CanonicalMusicBrainzArtistMBIDFromLinks(artist.ProviderLinks)
		if mbid == "" {
			continue // No unambiguous exact MusicBrainz identity: no provider work.
		}
		if err := scheduler.EnqueueArtist(ctx, artist.ID, mbid); err != nil {
			problems = append(problems, fmt.Errorf("enqueue artist %s artwork: %w", artist.ID, err))
		}
	}
	next := offset + len(page.Items)
	if len(page.Items) > 0 && next < page.Total {
		if err := scheduler.enqueue(ctx, LibraryArtworkDispatchArgs{Offset: next}, libraryArtworkDispatchInsertOptions()); err != nil {
			problems = append(problems, fmt.Errorf("enqueue library artwork page at offset %d: %w", next, err))
		}
	}
	return errors.Join(problems...)
}

// EnqueueArtist admits one exact artist. The args remain stable across retries
// so River can collapse duplicate deliveries while the artist job is active or
// still inside its refresh period.
func (scheduler *LibraryArtworkScheduler) EnqueueArtist(ctx context.Context, artistID uuid.UUID, artistMBID string) error {
	if scheduler == nil || scheduler.inserter == nil {
		return errors.New("library artwork scheduler is unavailable")
	}
	if artistID == uuid.Nil {
		return errors.New("library artwork artist ID is required")
	}
	mbid := CanonicalMusicBrainzArtistMBID([]string{artistMBID})
	if mbid == "" {
		return errors.New("library artwork artist MBID is invalid")
	}
	return scheduler.enqueue(ctx, LibraryArtistArtworkArgs{ArtistID: artistID, ArtistMBID: mbid}, libraryArtistArtworkInsertOptions())
}

// EnqueueEdition admits one exact edition derived from an artist discography.
// Eligibility and existing front/gallery state are deliberately not frozen into
// the args; the edition worker reads them again at delivery.
func (scheduler *LibraryArtworkScheduler) EnqueueEdition(ctx context.Context, artistID uuid.UUID, artistMBID, releaseMBID string) error {
	if scheduler == nil || scheduler.inserter == nil {
		return errors.New("library artwork scheduler is unavailable")
	}
	if artistID == uuid.Nil {
		return errors.New("library artwork artist ID is required")
	}
	mbid := CanonicalMusicBrainzArtistMBID([]string{artistMBID})
	if mbid == "" {
		return errors.New("library artwork artist MBID is invalid")
	}
	release, ok := CanonicalMusicBrainzReleaseMBID(releaseMBID)
	if !ok {
		return errors.New("library artwork release MBID is invalid")
	}
	return scheduler.enqueue(ctx, LibraryEditionArtworkArgs{
		ArtistID: artistID, ArtistMBID: mbid, ReleaseMBID: release,
	}, libraryEditionArtworkInsertOptions())
}

func (scheduler *LibraryArtworkScheduler) enqueue(ctx context.Context, args river.JobArgs, options *river.InsertOpts) error {
	_, err := scheduler.inserter.Insert(ctx, args, options)
	return err
}

// LibraryArtworkDispatchInsert is used by the periodic root job. The root is
// unique for the dispatch period, so a minute tick does not restart a sweep that
// is already paging through the library.
func LibraryArtworkDispatchInsert() (river.JobArgs, *river.InsertOpts) {
	return LibraryArtworkDispatchArgs{Offset: 0}, libraryArtworkDispatchInsertOptions()
}

func libraryArtworkDispatchInsertOptions() *river.InsertOpts {
	return &river.InsertOpts{
		Queue: LibraryArtworkQueue, MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true, ByPeriod: libraryArtworkDispatchPeriod,
			ByState: libraryArtworkUniqueStates(),
		},
	}
}

func libraryArtistArtworkInsertOptions() *river.InsertOpts {
	return &river.InsertOpts{
		Queue: LibraryArtworkQueue, MaxAttempts: 5,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true, ByPeriod: libraryArtistArtworkPeriod,
			ByState: libraryArtworkUniqueStates(),
		},
	}
}

func libraryEditionArtworkInsertOptions() *river.InsertOpts {
	return &river.InsertOpts{
		Queue: LibraryArtworkQueue, MaxAttempts: 5,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true, ByPeriod: libraryEditionArtworkPeriod,
			ByState: libraryArtworkUniqueStates(),
		},
	}
}

func libraryArtworkUniqueStates() []rivertype.JobState {
	// Completed is deliberate: the period must suppress re-enqueueing an
	// artist or edition whose previous delivery finished successfully.
	return []rivertype.JobState{
		rivertype.JobStateAvailable,
		rivertype.JobStateCompleted,
		rivertype.JobStatePending,
		rivertype.JobStateRunning,
		rivertype.JobStateRetryable,
		rivertype.JobStateScheduled,
	}
}

// CanonicalMusicBrainzArtistMBID exposes the exact-identity rule used by the
// artist photo resolver: one valid MusicBrainz artist UUID, or nothing.
func CanonicalMusicBrainzArtistMBID(providerKeys []string) string {
	return validatedMusicBrainzArtistMBID(providerKeys)
}

// CanonicalMusicBrainzArtistMBIDFromLinks accepts only MusicBrainz provider
// links and applies the same unambiguous-identity rule.
func CanonicalMusicBrainzArtistMBIDFromLinks(links []persistence.LibraryCatalogProviderLink) string {
	keys := make([]string, 0, len(links))
	for _, link := range links {
		if strings.EqualFold(strings.TrimSpace(link.ProviderCode), "musicbrainz") {
			keys = append(keys, link.ProviderKey)
		}
	}
	return CanonicalMusicBrainzArtistMBID(keys)
}

// CanonicalMusicBrainzReleaseMBID accepts only a canonical MusicBrainz release
// UUID. The edition worker and scheduler use the same rule as provider artwork.
func CanonicalMusicBrainzReleaseMBID(value string) (string, bool) {
	return exactReleaseMBID(value)
}
