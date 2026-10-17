package librarymetadata

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var (
	ErrPrimaryArtistRequired = errors.New("primary artist selection required")
	ErrPrimaryArtistInvalid  = errors.New("selected primary artist is not in the current credits")
	ErrAmbiguousArtistMBID   = errors.New("artist mbid identifies multiple local artists")
)

// CreditedArtist is a local artist identity as represented in an ordered credit.
type CreditedArtist struct {
	ArtistID   uuid.UUID
	ArtistName string
	MBID       *uuid.UUID
	CreditName string
	JoinPhrase string
}

// PrimaryArtist is the resolved identity. ArtistID, name, and MBID always come
// from the same current credit identity.
type PrimaryArtist struct {
	ArtistID uuid.UUID
	Name     string
	MBID     *uuid.UUID
}

type PrimaryArtistResolution struct {
	Artist            *PrimaryArtist
	RequiresSelection bool
}

// ResolvePrimaryArtist resolves an explicit local artist ID, or automatically
// resolves the only distinct local artist in the ordered credits. Repeated
// credits for the same local ID remain in the caller's ordered list but count as
// one candidate here.
func ResolvePrimaryArtist(credits []CreditedArtist, selectedArtistID *uuid.UUID) (PrimaryArtistResolution, error) {
	artists := make(map[uuid.UUID]PrimaryArtist)
	mbidOwner := make(map[uuid.UUID]uuid.UUID)
	ambiguousMBID := make(map[uuid.UUID]bool)
	orderedIDs := make([]uuid.UUID, 0, len(credits))
	for _, credit := range credits {
		if credit.ArtistID == uuid.Nil {
			return PrimaryArtistResolution{}, fmt.Errorf("credit has a missing local artist ID")
		}
		if credit.ArtistName == "" {
			return PrimaryArtistResolution{}, fmt.Errorf("credit for artist %s has a missing artist name", credit.ArtistID)
		}
		if existing, exists := artists[credit.ArtistID]; exists {
			if existing.Name != credit.ArtistName || !sameUUID(existing.MBID, credit.MBID) {
				return PrimaryArtistResolution{}, fmt.Errorf("credits for artist %s disagree on identity", credit.ArtistID)
			}
			continue
		}
		var mbid *uuid.UUID
		if credit.MBID != nil && *credit.MBID != uuid.Nil {
			value := *credit.MBID
			mbid = &value
			if owner, exists := mbidOwner[value]; exists && owner != credit.ArtistID {
				ambiguousMBID[value] = true
			} else {
				mbidOwner[value] = credit.ArtistID
			}
		}
		artists[credit.ArtistID] = PrimaryArtist{ArtistID: credit.ArtistID, Name: credit.ArtistName, MBID: mbid}
		orderedIDs = append(orderedIDs, credit.ArtistID)
	}

	var chosen uuid.UUID
	if selectedArtistID != nil {
		if *selectedArtistID == uuid.Nil {
			return PrimaryArtistResolution{}, fmt.Errorf("%w: local artist ID is missing", ErrPrimaryArtistInvalid)
		}
		if _, exists := artists[*selectedArtistID]; !exists {
			return PrimaryArtistResolution{}, fmt.Errorf("%w: %s", ErrPrimaryArtistInvalid, *selectedArtistID)
		}
		chosen = *selectedArtistID
	} else {
		if len(orderedIDs) == 0 || len(orderedIDs) > 1 {
			return PrimaryArtistResolution{RequiresSelection: true}, ErrPrimaryArtistRequired
		}
		chosen = orderedIDs[0]
	}
	result := artists[chosen]
	if result.MBID != nil && ambiguousMBID[*result.MBID] {
		return PrimaryArtistResolution{}, fmt.Errorf("%w: %s", ErrAmbiguousArtistMBID, *result.MBID)
	}
	return PrimaryArtistResolution{Artist: &result}, nil
}

func sameUUID(left, right *uuid.UUID) bool {
	if left == nil || *left == uuid.Nil {
		return right == nil || *right == uuid.Nil
	}
	return right != nil && *left == *right
}
