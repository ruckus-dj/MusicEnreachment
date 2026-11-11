package publication

import (
	"encoding/json"
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
	keys, ok := normalizedTagKeys(scope, field)
	if !ok {
		return nil
	}
	if field == "artist_credit" {
		if scope == librarymetadata.ScopePosition {
			return append(keys, "ARTISTS", "MUSICBRAINZ_ARTISTID")
		}
		return append(keys, "ALBUMARTISTS", "MUSICBRAINZ_ALBUMARTISTID")
	}
	return keys
}

// PublicationTagKeysForFieldValue includes value-dependent keys emitted by a
// field, such as instrument-qualified performer tags.
func PublicationTagKeysForFieldValue(scope librarymetadata.Scope, field string, value any) ([]string, error) {
	keys := PublicationTagKeysForField(scope, field)
	if field != "contributors" || scope != librarymetadata.ScopePosition {
		return keys, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode publication contributors for tag footprint: %w", err)
	}
	var contributors []librarymetadata.Contributor
	if err := json.Unmarshal(encoded, &contributors); err != nil {
		return nil, fmt.Errorf("decode publication contributors for tag footprint: %w", err)
	}
	seen := make(map[string]struct{}, len(keys)+len(contributors))
	for _, key := range keys {
		seen[key] = struct{}{}
	}
	for _, contributor := range contributors {
		if !strings.EqualFold(strings.TrimSpace(contributor.Role), "performer") {
			continue
		}
		key := performerTagKey(contributor.Instrument)
		if _, exists := seen[key]; exists {
			continue
		}
		keys = append(keys, key)
		seen[key] = struct{}{}
	}
	return keys, nil
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
	return key == "LYRICS" || validProviderTagKey(key) || strings.HasPrefix(key, "PERFORMER:") && len(key) > len("PERFORMER:")
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
