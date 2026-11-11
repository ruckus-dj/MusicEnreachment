package librarymetadata

import (
	"encoding/json"
	"fmt"
)

// CreditOrigin identifies the authority that supplied effective credits.
type CreditOrigin string

type CreditResolutionStatus string

const (
	CreditResolutionResolved     CreditResolutionStatus = "resolved"
	CreditResolutionUnavailable  CreditResolutionStatus = "unavailable"
	CreditResolutionUnclassified CreditResolutionStatus = "unclassified"
)

const (
	CreditOriginPosition         CreditOrigin = "position"
	CreditOriginRelease          CreditOrigin = "release"
	CreditOriginManualMatching   CreditOrigin = "manual_matching"
	CreditOriginProviderPosition CreditOrigin = "provider_position"
	CreditOriginRecording        CreditOrigin = "recording"
)

// CreditCandidate keeps source presence separate from its rows. In particular,
// a present empty slice is authoritative and must not fall through.
type CreditCandidate struct {
	Present bool
	Credits []ArtistCredit
	Origin  CreditOrigin
}

// ResolvedArtistCredits is an effective ordered credit list and its authority.
type ResolvedArtistCredits struct {
	Credits []ArtistCredit
	Origin  CreditOrigin
	State   ValueState
	Status  CreditResolutionStatus
}

// ResolveArtistCredits applies the position credit precedence. Desired fields
// are terminal whenever the key exists, including absent and explicit-empty
// states. Provider credits are supplied only when an explicit provider list
// has been proven; copied recording fallback credits are not an argument here.
func ResolveArtistCredits(
	position *Envelope,
	manualMatching CreditCandidate,
	providerPosition CreditCandidate,
	recording *Envelope,
	recordingCredits CreditCandidate,
) (ResolvedArtistCredits, error) {
	if credits, exists, err := desiredArtistCredits(position, CreditOriginPosition); exists || err != nil {
		if err != nil {
			return ResolvedArtistCredits{}, fmt.Errorf("resolve position artist credits: %w", err)
		}
		return credits, nil
	}
	if manualMatching.Present {
		manualMatching.Origin = CreditOriginManualMatching
		return resolvedCandidate(manualMatching), nil
	}
	if providerPosition.Present {
		providerPosition.Origin = CreditOriginProviderPosition
		return resolvedCandidate(providerPosition), nil
	}
	if credits, exists, err := desiredArtistCredits(recording, CreditOriginRecording); exists || err != nil {
		if err != nil {
			return ResolvedArtistCredits{}, fmt.Errorf("resolve recording artist credits: %w", err)
		}
		return credits, nil
	}
	if recordingCredits.Present {
		recordingCredits.Origin = CreditOriginRecording
		return resolvedCandidate(recordingCredits), nil
	}
	return ResolvedArtistCredits{Status: CreditResolutionUnavailable}, nil
}

// ResolveReleaseArtistCredits resolves release credits independently of
// recording and position ownership.
func ResolveReleaseArtistCredits(envelope *Envelope, relationalCredits []ArtistCredit) (ResolvedArtistCredits, error) {
	if credits, exists, err := desiredArtistCredits(envelope, CreditOriginRelease); exists || err != nil {
		return credits, err
	}
	return resolvedCandidate(CreditCandidate{Present: true, Credits: relationalCredits, Origin: CreditOriginRelease}), nil
}

// ResolveRecordingArtistCredits resolves recording-owned desired credits over
// the recording's relational credit baseline without inheriting other scopes.
func ResolveRecordingArtistCredits(envelope *Envelope, relationalCredits []ArtistCredit) (ResolvedArtistCredits, error) {
	if credits, exists, err := desiredArtistCredits(envelope, CreditOriginRecording); exists || err != nil {
		return credits, err
	}
	return resolvedCandidate(CreditCandidate{Present: true, Credits: relationalCredits, Origin: CreditOriginRecording}), nil
}

func desiredArtistCredits(envelope *Envelope, origin CreditOrigin) (ResolvedArtistCredits, bool, error) {
	if envelope == nil {
		return ResolvedArtistCredits{}, false, nil
	}
	field, exists := envelope.Desired["artist_credit"]
	if !exists {
		return ResolvedArtistCredits{}, false, nil
	}
	if field.State == StateAbsent || field.State == StateExplicitEmpty {
		return ResolvedArtistCredits{Credits: []ArtistCredit{}, Origin: origin, State: field.State, Status: CreditResolutionResolved}, true, nil
	}
	if field.State != StateValue {
		return ResolvedArtistCredits{}, true, fmt.Errorf("unknown artist credit state %q", field.State)
	}
	credits, err := decodeArtistCredits(field.Value)
	if err != nil {
		return ResolvedArtistCredits{}, true, err
	}
	return ResolvedArtistCredits{Credits: credits, Origin: origin, State: StateValue, Status: CreditResolutionResolved}, true, nil
}

func resolvedCandidate(candidate CreditCandidate) ResolvedArtistCredits {
	credits := append([]ArtistCredit(nil), candidate.Credits...)
	if credits == nil {
		credits = []ArtistCredit{}
	}
	return ResolvedArtistCredits{Credits: credits, Origin: candidate.Origin, State: StateValue, Status: CreditResolutionResolved}
}

func decodeArtistCredits(value any) ([]ArtistCredit, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode artist credits: %w", err)
	}
	var credits []ArtistCredit
	if err := json.Unmarshal(encoded, &credits); err != nil {
		return nil, fmt.Errorf("decode artist credits: %w", err)
	}
	if credits == nil {
		credits = []ArtistCredit{}
	}
	return credits, nil
}
