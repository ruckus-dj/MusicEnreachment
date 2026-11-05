//go:build integration

package persistence

import (
	"encoding/json"
	"testing"
)

func TestLibraryCatalogEditionUsesTypedLocalMetadataAndBlocksProviderFallback(t *testing.T) {
	metadata := json.RawMessage(`{"local_metadata":{"version":1,"scope":"release","desired":{"release_date":{"state":"value","value":"2001","authority":"manual","provenance":{"kind":"manual"}},"label":{"state":"explicit_empty","authority":"manual","provenance":{"kind":"manual"}},"catalog_number":{"state":"absent","authority":"manual","provenance":{"kind":"manual"}}}}}`)
	edition := libraryCatalogLocalEdition(metadata)
	provider := LibraryCatalogEdition{
		ReleaseDate: catalogProviderStringPointer("2020"), Labels: []string{"Provider Label"},
		CatalogNumbers: []string{"Provider Catalog"}, SecondaryTypes: []string{"EP"},
	}
	mergeLibraryCatalogEdition(&edition, provider, metadata)
	if edition.ReleaseDate == nil || *edition.ReleaseDate != "2001" {
		t.Fatalf("local release date = %#v", edition.ReleaseDate)
	}
	if len(edition.Labels) != 0 || len(edition.CatalogNumbers) != 0 {
		t.Fatalf("explicit empty/manual absence resurrected provider values: %#v", edition)
	}
	if len(edition.SecondaryTypes) != 1 || edition.SecondaryTypes[0] != "EP" {
		t.Fatalf("unmanaged provider field did not fall back: %#v", edition.SecondaryTypes)
	}
}

func TestLibraryCatalogEditionMalformedLocalMetadataDoesNotGuessProviderValues(t *testing.T) {
	metadata := json.RawMessage(`{"local_metadata":{"version":99,"scope":"release","desired":{}}}`)
	edition := libraryCatalogLocalEdition(metadata)
	mergeLibraryCatalogEdition(&edition, LibraryCatalogEdition{ReleaseDate: catalogProviderStringPointer("2020"), Labels: []string{}}, metadata)
	if edition.ReleaseDate != nil {
		t.Fatalf("malformed local metadata fell back to guessed provider value: %#v", edition.ReleaseDate)
	}
}

func catalogProviderStringPointer(value string) *string { return &value }
