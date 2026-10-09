package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestProviderProjectionFieldAuthorityAndOrder(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewProviderCacheRepository(db)
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name, setting_value) VALUES('musicbrainz_config_identity', 'field-authority') ON CONFLICT (setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	provider := persistence.Provider{ID: uuid.New(), Code: "field-authority"}
	if err := repository.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	source := persistence.ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: "field-authority", Endpoint: "https://example.invalid", ConfigurationIdentity: "field-authority"}
	if err := repository.CreateProviderSource(ctx, &source); err != nil {
		t.Fatal(err)
	}
	const key = "evidence-entity"
	apply := func(cacheKey string, authority int, fields string) persistence.ProviderRefreshTicket {
		t.Helper()
		ticket, err := repository.BeginRefresh(ctx, source.ID, cacheKey, "field-authority")
		if err != nil {
			t.Fatal(err)
		}
		graph := persistence.ProviderGraph{Entities: []persistence.ProviderSourceEntity{{
			Kind: "release", ProviderKey: key, Name: "Release", Fields: json.RawMessage(fields), RawSource: json.RawMessage(`{"from":"response"}`), Authority: authority,
		}}}
		if err := repository.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{}`), Graph: graph}); err != nil {
			t.Fatal(err)
		}
		return ticket
	}
	readCountry := func() string {
		t.Helper()
		var fields string
		if err := db.NewRaw(`SELECT fields FROM provider_source_entity WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, source.ID, key).Scan(ctx, &fields); err != nil {
			t.Fatal(err)
		}
		var values map[string]string
		if err := json.Unmarshal([]byte(fields), &values); err != nil {
			t.Fatal(err)
		}
		return values["country"]
	}

	apply("search-us", 2, `{"country":"US","title":"Search title"}`)
	apply("lookup-omit-country", 3, `{"title":"Lookup title","status":"official"}`)
	apply("search-gb", 2, `{"country":"GB","title":"Older search title"}`)
	if got := readCountry(); got != "GB" {
		t.Fatalf("search evidence did not update country after lookup omitted it: got %q", got)
	}
	var fields string
	if err := db.NewRaw(`SELECT fields FROM provider_source_entity WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, source.ID, key).Scan(ctx, &fields); err != nil {
		t.Fatal(err)
	}
	var lookupFields map[string]string
	if err := json.Unmarshal([]byte(fields), &lookupFields); err != nil {
		t.Fatal(err)
	}
	if lookupFields["title"] != "Lookup title" || lookupFields["status"] != "official" {
		t.Fatalf("lower-authority search overwrote lookup fields: %s", fields)
	}

	// The lookup ticket is deliberately older. A newer search applies first, but
	// cannot advance the authority fence and therefore cannot block the lookup.
	olderLookup, err := repository.BeginRefresh(ctx, source.ID, "pending-lookup", "field-authority")
	if err != nil {
		t.Fatal(err)
	}
	newerSearch, err := repository.BeginRefresh(ctx, source.ID, "newer-search", "field-authority")
	if err != nil {
		t.Fatal(err)
	}
	applyTicket := func(ticket persistence.ProviderRefreshTicket, authority int, raw string) {
		t.Helper()
		graph := persistence.ProviderGraph{Entities: []persistence.ProviderSourceEntity{{Kind: "release", ProviderKey: key, Name: "Release", Fields: json.RawMessage(raw), RawSource: json.RawMessage(`{}`), Authority: authority}}}
		if err := repository.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{}`), Graph: graph}); err != nil {
			t.Fatal(err)
		}
	}
	applyTicket(newerSearch, 2, `{"artist-credit":["search"],"country":"CA"}`)
	applyTicket(olderLookup, 3, `{"artist-credit":["lookup"]}`)
	if err := db.NewRaw(`SELECT fields FROM provider_source_entity WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, source.ID, key).Scan(ctx, &fields); err != nil {
		t.Fatal(err)
	}
	var final map[string]json.RawMessage
	if err := json.Unmarshal([]byte(fields), &final); err != nil {
		t.Fatal(err)
	}
	if string(final["artist-credit"]) != `["lookup"]` || string(final["country"]) != `"CA"` {
		t.Fatalf("field-level authority/fetch order merge = %s", fields)
	}

	// A later lookup advances only fields it supplies; omitted evidence remains.
	apply("newest-lookup", 3, `{"title":"Newest lookup"}`)
	if err := db.NewRaw(`SELECT fields FROM provider_source_entity WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, source.ID, key).Scan(ctx, &fields); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(fields), &final); err != nil {
		t.Fatal(err)
	}
	if string(final["artist-credit"]) != `["lookup"]` || string(final["country"]) != `"CA"` || string(final["title"]) != `"Newest lookup"` {
		t.Fatalf("newer lookup erased omitted evidence: %s", fields)
	}
}
