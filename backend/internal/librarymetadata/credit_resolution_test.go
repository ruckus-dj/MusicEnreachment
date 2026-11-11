package librarymetadata

import "testing"

func TestResolveArtistCreditsAuthorityAndTerminalStates(t *testing.T) {
	credit := ArtistCredit{ArtistID: "11111111-1111-4111-8111-111111111111", ArtistName: "Artist", CreditName: "Alias", JoinPhrase: " & "}
	value := func(scope Scope, state ValueState, credits []ArtistCredit) *Envelope {
		field := FieldValue{State: state, Authority: AuthorityManual, Provenance: Provenance{Kind: ProvenanceManual}}
		if state == StateValue {
			field.Value = credits
			field.valuePresent = true
		}
		return &Envelope{Version: EnvelopeVersion, Scope: scope, Desired: map[string]FieldValue{"artist_credit": field}}
	}
	tests := []struct {
		name      string
		position  *Envelope
		manual    CreditCandidate
		provider  CreditCandidate
		recording *Envelope
		recBase   CreditCandidate
		origin    CreditOrigin
		state     ValueState
		count     int
	}{
		{
			name:     "explicit provider empty is not recording fallback",
			provider: CreditCandidate{Present: true, Credits: []ArtistCredit{}},
			recBase:  CreditCandidate{Present: true, Credits: []ArtistCredit{credit}},
			origin:   CreditOriginProviderPosition, state: StateValue, count: 0,
		},
		{
			name:     "position absent is terminal",
			position: value(ScopePosition, StateAbsent, nil),
			provider: CreditCandidate{Present: true, Credits: []ArtistCredit{credit}},
			recBase:  CreditCandidate{Present: true, Credits: []ArtistCredit{credit}},
			origin:   CreditOriginPosition, state: StateAbsent, count: 0,
		},
		{
			name:     "position explicit empty is terminal",
			position: value(ScopePosition, StateExplicitEmpty, nil),
			recBase:  CreditCandidate{Present: true, Credits: []ArtistCredit{credit}},
			origin:   CreditOriginPosition, state: StateExplicitEmpty, count: 0,
		},
		{
			name:     "position value empty is terminal",
			position: value(ScopePosition, StateValue, []ArtistCredit{}),
			recBase:  CreditCandidate{Present: true, Credits: []ArtistCredit{credit}},
			origin:   CreditOriginPosition, state: StateValue, count: 0,
		},
		{
			name:     "manual empty outranks provider",
			manual:   CreditCandidate{Present: true, Credits: []ArtistCredit{}},
			provider: CreditCandidate{Present: true, Credits: []ArtistCredit{credit}},
			recBase:  CreditCandidate{Present: true, Credits: []ArtistCredit{credit}},
			origin:   CreditOriginManualMatching, state: StateValue, count: 0,
		},
		{
			name:      "recording desired empty is terminal",
			recording: value(ScopeRecording, StateExplicitEmpty, nil),
			recBase:   CreditCandidate{Present: true, Credits: []ArtistCredit{credit}},
			origin:    CreditOriginRecording, state: StateExplicitEmpty, count: 0,
		},
		{
			name:    "unclassified provider absence falls back to recording",
			recBase: CreditCandidate{Present: true, Credits: []ArtistCredit{credit}},
			origin:  CreditOriginRecording, state: StateValue, count: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolved, err := ResolveArtistCredits(test.position, test.manual, test.provider, test.recording, test.recBase)
			if err != nil {
				t.Fatalf("ResolveArtistCredits() error = %v", err)
			}
			if resolved.Origin != test.origin || resolved.State != test.state || len(resolved.Credits) != test.count {
				t.Fatalf("resolved = %#v, want origin %q, state %q, count %d", resolved, test.origin, test.state, test.count)
			}
		})
	}
}
