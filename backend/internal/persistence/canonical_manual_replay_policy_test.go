//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCanonicalManualLyricsCommitReplayRequiresCurrentPolicyPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _, _, _ := prepareManualLyricsCommitFixture(t)
	if committed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("manual commit = %v, %v", committed, err)
	}
	attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if attempt == nil || attempt.Phase != "committed" || attempt.FrozenInputs != nil {
		t.Fatalf("commit did not consume frozen inputs: %+v", attempt)
	}
	entries, err := decodeJournal(attempt.MutationJournal)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parseCanonicalManualLyricsCommitPrefix(entries, f.guard, attempt); !ok {
		t.Fatal("valid canonical manual commit journal rejected")
	}

	// Simulate a self-consistent historical journal: every recorded policy
	// provenance value is obsolete, while the factual evidence remains intact.
	var obsolete []json.RawMessage
	if err := json.Unmarshal(mustJSON(entries), &obsolete); err != nil {
		t.Fatal(err)
	}
	for i, raw := range obsolete {
		var value any
		decoderErr := json.Unmarshal(raw, &value)
		if decoderErr != nil {
			t.Fatalf("decode journal entry %d: %v", i, decoderErr)
		}
		obsolete[i] = mustJSON(rewritePolicyVersions(value, 2))
	}
	if _, ok := parseCanonicalManualLyricsCommitPrefix(obsolete, f.guard, attempt); ok {
		t.Fatal("self-consistent journal with obsolete policy provenance accepted")
	}
}

func rewritePolicyVersions(value any, version int) any {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "quality_policy_version" {
				value[key] = version
			} else {
				value[key] = rewritePolicyVersions(child, version)
			}
		}
	case []any:
		for i, child := range value {
			value[i] = rewritePolicyVersions(child, version)
		}
	}
	return value
}
