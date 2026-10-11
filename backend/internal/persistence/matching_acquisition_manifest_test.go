package persistence

import (
	"encoding/json"
	"fmt"
	"testing"
)

// manifestReleaseIDs returns count canonical lowercase release UUIDs. The
// manifest checks parse each release id as a canonical UUID, so the fixtures
// must use real ids rather than opaque labels.
func manifestReleaseIDs(count int) []string {
	ids := make([]string, count)
	for index := range ids {
		ids[index] = fmt.Sprintf("00000000-0000-4000-8000-%012d", index+1)
	}
	return ids
}

// manifestBrowsePayload is the raw MusicBrainz artist release browse envelope
// with its hyphenated release-count/release-offset keys.
func manifestBrowsePayload(t *testing.T, offset, count int, releaseIDs []string) json.RawMessage {
	t.Helper()
	releases := make([]map[string]string, len(releaseIDs))
	for index, id := range releaseIDs {
		releases[index] = map[string]string{"id": id}
	}
	encoded, err := json.Marshal(map[string]any{"release-offset": offset, "release-count": count, "releases": releases})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// manifestPageOutput is the sealed accumulated catalog manifest for one page.
func manifestPageOutput(t *testing.T, nextOffset, catalogCount int, complete bool, releaseIDs []string) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"next_offset": nextOffset, "complete": complete, "catalog_count": catalogCount,
		"covered_releases": nextOffset, "release_ids": releaseIDs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func manifestPreviousOutput(t *testing.T, releaseIDs []string, catalogCount *int) json.RawMessage {
	t.Helper()
	page := map[string]any{"release_ids": releaseIDs}
	if catalogCount != nil {
		page["catalog_count"] = *catalogCount
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func manifestIntPtr(value int) *int { return &value }

func TestResponseCursorMatchesExactCatalogManifest(t *testing.T) {
	priorCount := manifestIntPtr(3)
	previous := manifestPreviousOutput(t, manifestReleaseIDs(1), priorCount)
	payload := manifestBrowsePayload(t, 1, 3, manifestReleaseIDs(2)[1:])
	input := json.RawMessage(`{"offset":1,"limit":1}`)
	validOutput := manifestPageOutput(t, 2, 3, false, manifestReleaseIDs(2))
	if !responseCursorMatches(payload, input, validOutput, previous) {
		t.Fatal("expected exact accumulated catalog manifest to match")
	}

	rejected := map[string]json.RawMessage{
		"invented id":   manifestPageOutput(t, 2, 3, false, []string{manifestReleaseIDs(2)[0], manifestReleaseIDs(3)[2]}),
		"missing id":    manifestPageOutput(t, 2, 3, false, manifestReleaseIDs(2)[:1]),
		"wrong prefix":  manifestPageOutput(t, 2, 3, false, []string{manifestReleaseIDs(3)[2], manifestReleaseIDs(2)[1]}),
		"padded ids":    manifestPageOutput(t, 2, 3, false, manifestReleaseIDs(3)),
		"changed count": manifestPageOutput(t, 2, 4, false, manifestReleaseIDs(2)),
		"omitted count": json.RawMessage(`{"next_offset":2,"complete":false,"covered_releases":2,"release_ids":["` + manifestReleaseIDs(2)[0] + `","` + manifestReleaseIDs(2)[1] + `"]}`),
		"null count":    json.RawMessage(`{"next_offset":2,"complete":false,"catalog_count":null,"covered_releases":2,"release_ids":["` + manifestReleaseIDs(2)[0] + `","` + manifestReleaseIDs(2)[1] + `"]}`),
	}
	for name, output := range rejected {
		t.Run(name, func(t *testing.T) {
			if responseCursorMatches(payload, input, output, previous) {
				t.Fatal("accepted a catalog manifest that differs from accepted page provenance")
			}
		})
	}
}

func TestResponseCursorMatchesTwoPageChain(t *testing.T) {
	ids := manifestReleaseIDs(3)
	firstPayload := manifestBrowsePayload(t, 0, 3, ids[:2])
	firstInput := json.RawMessage(`{"offset":0,"limit":100}`)
	firstOutput := manifestPageOutput(t, 2, 3, false, ids[:2])
	// The first page follows the artist cursor, whose output has offset zero and
	// no sealed catalog count yet: the absence is legitimate.
	artistCursor := json.RawMessage(`{"artist_mbid":"11111111-1111-4111-8111-111111111111","next_offset":0,"complete":false}`)
	if !responseCursorMatches(firstPayload, firstInput, firstOutput, artistCursor) {
		t.Fatal("expected first catalog page to establish the sealed catalog count")
	}

	secondPayload := manifestBrowsePayload(t, 2, 3, ids[2:])
	secondInput := json.RawMessage(`{"offset":2,"limit":100}`)
	secondOutput := manifestPageOutput(t, 3, 3, true, ids)
	if !responseCursorMatches(secondPayload, secondInput, secondOutput, firstOutput) {
		t.Fatal("expected the second catalog page to continue the same sealed catalog")
	}
}

func TestResponseCursorMatchesEmptyCatalogPage(t *testing.T) {
	input := json.RawMessage(`{"offset":0,"limit":100}`)
	payload := manifestBrowsePayload(t, 0, 0, nil)
	output := manifestPageOutput(t, 0, 0, true, nil)
	artistCursor := json.RawMessage(`{"artist_mbid":"11111111-1111-4111-8111-111111111111","next_offset":0,"complete":false}`)
	if !responseCursorMatches(payload, input, output, artistCursor) {
		t.Fatal("expected an empty first catalog page to seal a zero catalog count")
	}
}

func TestResponseCursorMatchesRejectsContinuationWithoutPriorCount(t *testing.T) {
	ids := manifestReleaseIDs(3)
	previous := manifestPreviousOutput(t, ids[:2], nil)
	payload := manifestBrowsePayload(t, 2, 3, ids[2:])
	input := json.RawMessage(`{"offset":2,"limit":100}`)
	output := manifestPageOutput(t, 3, 3, true, ids)
	if responseCursorMatches(payload, input, output, previous) {
		t.Fatal("accepted a continuation whose prior page dropped the sealed catalog count")
	}
}

func TestResponseCursorMatchesRejectsChangedPriorCount(t *testing.T) {
	ids := manifestReleaseIDs(101)
	previous := manifestPreviousOutput(t, ids, manifestIntPtr(200))
	payload := manifestBrowsePayload(t, 101, 101, nil)
	input := json.RawMessage(`{"offset":101,"limit":100}`)
	output := manifestPageOutput(t, 101, 101, true, ids)
	if responseCursorMatches(payload, input, output, previous) {
		t.Fatal("accepted a completed page whose prior sealed catalog count changed")
	}
}
