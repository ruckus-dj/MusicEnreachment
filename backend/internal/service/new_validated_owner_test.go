//go:build integration

package service

import "testing"

func TestValidatedPreparedManualLyricsFLACPostgreSQL(t *testing.T) {
	for _, test := range []struct {
		name    string
		cover   bool
		removal bool
	}{
		{name: "no_cover_value"},
		{name: "cover_value", cover: true},
		{name: "cover_removal", cover: true, removal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			testPreparedManualLyricsFLACOwner(t, manualLyricsScenario{withCover: test.cover, removal: test.removal, continuous: true})
		})
	}
}
