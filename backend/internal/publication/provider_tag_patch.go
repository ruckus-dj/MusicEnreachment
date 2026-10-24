package publication

import (
	"fmt"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
)

// ApplyProviderTagPatch projects only explicit provider values onto current
// normalized tags. It does not establish provider authority; callers must pass
// only accepted provider fields and explicitly selected protected keys.
func ApplyProviderTagPatch(current map[string][]string, patch map[string][]string, protectedKeys []string) (map[string][]string, error) {
	if current == nil {
		return nil, fmt.Errorf("current publication tags are required")
	}

	protected := make(map[string]struct{}, len(protectedKeys))
	for _, key := range protectedKeys {
		if !validProtectedTagKey(key) {
			return nil, fmt.Errorf("invalid protected normalized tag key %q", key)
		}
		protected[key] = struct{}{}
	}

	result := cloneTagMap(current)
	for key, values := range patch {
		if !validProviderTagKey(key) {
			return nil, fmt.Errorf("invalid provider normalized tag key %q", key)
		}
		if values == nil {
			return nil, fmt.Errorf("provider normalized tag %q has nil values", key)
		}
		if _, keepCurrent := protected[key]; keepCurrent {
			continue
		}
		result[key] = cloneTagValues(values)
	}
	return result, nil
}

// PublicationTagKeysForField returns the approved normalized tag keys a
// publication field can produce. It describes mapping footprints only; it
// does not enable provider updates or confer authority to apply them.
func PublicationTagKeysForField(scope librarymetadata.Scope, field string) []string {
	if scope != librarymetadata.ScopeRelease && scope != librarymetadata.ScopePosition {
		return nil
	}
	key, ok := normalizedTagKey(scope, field)
	if !ok {
		return nil
	}
	if field == "artist_credit" {
		if scope == librarymetadata.ScopePosition {
			return []string{key, "ARTISTS", "MUSICBRAINZ_ARTISTID"}
		}
		return []string{key, "ALBUMARTISTS", "MUSICBRAINZ_ALBUMARTISTID"}
	}
	return []string{key}
}

func validProviderTagKey(key string) bool {
	if key == "" || strings.TrimSpace(key) != key || key == "LYRICS" {
		return false
	}
	_, supported := providerTagKeys[key]
	return supported
}

var providerTagKeys = func() map[string]struct{} {
	keys := make(map[string]struct{})
	fields := librarymetadata.Fields()
	for _, scope := range []librarymetadata.Scope{librarymetadata.ScopeRelease, librarymetadata.ScopePosition} {
		for _, field := range fields {
			for _, mapped := range PublicationTagKeysForField(scope, field.Name) {
				keys[mapped] = struct{}{}
			}
		}
	}
	return keys
}()

func validProtectedTagKey(key string) bool {
	return key == "LYRICS" || validProviderTagKey(key)
}

func cloneTagMap(tags map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(tags))
	for key, values := range tags {
		cloned[key] = cloneTagValues(values)
	}
	return cloned
}

func cloneTagValues(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}
