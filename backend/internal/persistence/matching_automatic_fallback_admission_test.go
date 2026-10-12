package persistence

import (
	"encoding/json"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
)

func TestValidateFallbackProviderInputUsesStageProviderKind(t *testing.T) {
	stage := policy.TypedFallbackQueryStage{Kind: "album_track", Query: `release:"Album" AND recording:"Track"`, Limit: 10}
	input := json.RawMessage(`{"schema_version":1,"query":"release:\"Album\" AND recording:\"Track\"","offset":0,"limit":10}`)

	for _, test := range []struct {
		name        string
		stageKind   string
		kind        string
		cachePrefix string
		wantErr     bool
	}{
		{name: "recording search accepted", stageKind: "album_track", kind: "provider_recording_search", cachePrefix: "recording-search"},
		{name: "release search rejected", stageKind: "album_track", kind: "provider_release_search", cachePrefix: "recording-search", wantErr: true},
		{name: "unknown stage rejected", stageKind: "unknown", kind: "provider_recording_search", cachePrefix: "recording-search", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			requestStage := stage
			requestStage.Kind = test.stageKind
			key := test.cachePrefix + ":0:10:" + requestStage.Query
			err := validateFallbackProviderInput(requestStage, &Operation{
				Kind: test.kind, InputSnapshot: input, ProviderCacheKey: &key,
			})
			if (err != nil) != test.wantErr {
				t.Fatalf("validateFallbackProviderInput() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
