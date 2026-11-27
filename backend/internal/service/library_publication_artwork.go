package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// ErrPublicationArtwork marks first-publication artwork that is pending,
// temporarily unavailable, or failed. It is deliberately distinct from a
// definitive Cover Art Archive absence or an absent saved source cover: callers
// must not turn this error into "publish without a cover".
var ErrPublicationArtwork = errors.New("publication artwork is not ready")

// LibraryPublicationArtworkState is the stable status vocabulary for the
// upcoming UI contract. Ready and absent both permit publication; pending and
// unavailable are retryable; current_transfer is an explicit scope statement,
// not a silent absence claim.
const (
	LibraryPublicationArtworkStateReady           = "ready"
	LibraryPublicationArtworkStateAbsent          = "absent"
	LibraryPublicationArtworkStatePending         = "pending"
	LibraryPublicationArtworkStateUnavailable     = "unavailable"
	LibraryPublicationArtworkStateNotLinked       = "not_linked"
	LibraryPublicationArtworkStateCurrentTransfer = "current_transfer"
)

// LibraryPublicationArtworkStatus is the bounded status the UI may render. A
// retryable status means publication admission remains blocked; it is never a
// no-cover success.
type LibraryPublicationArtworkStatus struct {
	State       string `json:"state"`
	Retryable   bool   `json:"retryable"`
	WarningCode string `json:"warning_code,omitempty"`
}

// PublicationArtworkStatusFromError maps the service error boundary onto the UI
// status vocabulary. A nil error means preparation returned successfully and
// cover presence must be read from the effective cover authority.
func PublicationArtworkStatusFromError(err error) LibraryPublicationArtworkStatus {
	switch {
	case err == nil:
		return LibraryPublicationArtworkStatus{State: LibraryPublicationArtworkStateReady}
	case errors.Is(err, ErrProviderArtworkPending):
		return LibraryPublicationArtworkStatus{State: LibraryPublicationArtworkStatePending, Retryable: true, WarningCode: "artwork_pending"}
	case errors.Is(err, ErrPublicationArtwork):
		return LibraryPublicationArtworkStatus{State: LibraryPublicationArtworkStateUnavailable, Retryable: true, WarningCode: "artwork_unavailable"}
	default:
		return LibraryPublicationArtworkStatus{State: LibraryPublicationArtworkStateUnavailable, Retryable: true, WarningCode: "artwork_failed"}
	}
}

// publicationArtworkPreparer is the narrow primary-service contract used before
// first publication. The primary service resolves the exact source-linked
// MusicBrainz edition, caches gallery originals, and then runs the default
// acquisition from the persisted front or saved local source. It never performs
// remote work inside an admission transaction.
type publicationArtworkPreparer interface {
	PrepareForPublication(context.Context, uuid.UUID) error
}

// WithPublicationArtwork installs the primary artwork preparation service.
// ReleaseID is the local library release identity; the implementation decides
// whether the release is linked to an exact MusicBrainz edition.
func (service *LibraryPublications) WithPublicationArtwork(artwork interface {
	PrepareForPublication(context.Context, uuid.UUID) error
}) *LibraryPublications {
	service.artwork = artwork
	return service
}

// preparePublicationArtwork prepares first-publication artwork and classifies
// every failure as ErrPublicationArtwork. A pending or temporarily unavailable
// provider answer therefore blocks admission instead of silently degrading to
// the no-cover publication path. Definitive absence is reported by the primary
// service as success, not as an error.
func (service *LibraryPublications) preparePublicationArtwork(ctx context.Context, releaseID uuid.UUID) error {
	if service == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if releaseID == uuid.Nil {
		return persistence.ErrPublicationAdmissionConflict
	}
	if service.artwork != nil {
		if err := service.artwork.PrepareForPublication(ctx, releaseID); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("%w: %w", ErrPublicationArtwork, err)
		}
		return nil
	}
	if service.coverAcquisition != nil {
		if err := service.coverAcquisition.Ensure(ctx, releaseID); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("%w: %w", ErrPublicationArtwork, err)
		}
	}
	return nil
}

// firstPublicationArtworkReleaseIdentity returns the exact MusicBrainz release
// MBID represented by a first-publication snapshot. Artwork preparation binds
// provider state to this identity, so admission must reject a relink that
// happened after preparation instead of publishing a cover selected for a
// different edition.
func firstPublicationArtworkReleaseIdentity(snapshot *persistence.LibraryFirstPublicationContext) (uuid.UUID, bool) {
	if snapshot == nil {
		return uuid.Nil, false
	}
	if snapshot.Tags.Release.ID != nil && *snapshot.Tags.Release.ID != uuid.Nil {
		return *snapshot.Tags.Release.ID, true
	}
	if token := snapshot.TemplateTokens["ALBUM_MBID"]; token != "" {
		if parsed, err := uuid.Parse(token); err == nil && parsed != uuid.Nil {
			return parsed, true
		}
	}
	return uuid.Nil, false
}
