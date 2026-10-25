package persistence

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// PublicationProviderTagProtection reports the normalized tag keys with local
// manual protection, separately from manual-absent facts requiring a decision.
type PublicationProviderTagProtection struct {
	Keys               []string
	ManualAbsentFields []string
}

// BuildPublicationProviderTagProtection collects local facts only. It does
// not decide whether manual-absent fields should protect provider tags.
func BuildPublicationProviderTagProtection(context *PublicationProviderTagContext) (PublicationProviderTagProtection, error) {
	result := PublicationProviderTagProtection{Keys: []string{}, ManualAbsentFields: []string{}}
	if context == nil {
		return result, fmt.Errorf("publication provider tag context is required")
	}

	keys := make(map[string]struct{})
	absent := make(map[string]struct{})
	addEnvelope := func(owner PublicationProviderTagOwner, scope librarymetadata.Scope) error {
		if len(owner.Metadata) == 0 {
			if owner.ID == uuid.Nil {
				return nil
			}
			return fmt.Errorf("local metadata owner has no metadata document")
		}
		envelope, err := decodeFirstPublicationEnvelopeValue(owner.Metadata, scope)
		if err != nil {
			return err
		}
		for field, value := range envelope.Desired {
			if value.Authority != librarymetadata.AuthorityManual {
				continue
			}
			if value.State == librarymetadata.StateAbsent {
				absent[field] = struct{}{}
				continue
			}
			for _, key := range publication.PublicationTagKeysForField(tagProtectionScope(scope), field) {
				keys[key] = struct{}{}
			}
		}
		return nil
	}
	if err := addEnvelope(context.ReleaseOwner, librarymetadata.ScopeRelease); err != nil {
		return PublicationProviderTagProtection{}, fmt.Errorf("read release manual tag protection: %w", err)
	}
	if err := addEnvelope(context.TrackOwner, librarymetadata.ScopeTrack); err != nil {
		return PublicationProviderTagProtection{}, fmt.Errorf("read track manual tag protection: %w", err)
	}
	if err := addEnvelope(context.PositionOwner.Owner, librarymetadata.ScopePosition); err != nil {
		return PublicationProviderTagProtection{}, fmt.Errorf("read position manual tag protection: %w", err)
	}
	if err := addEnvelope(context.RecordingOwner, librarymetadata.ScopeRecording); err != nil {
		return PublicationProviderTagProtection{}, fmt.Errorf("read recording manual tag protection: %w", err)
	}

	for _, override := range []struct {
		owner PublicationProviderTagOwner
		scope librarymetadata.Scope
	}{{context.PositionOwner.Owner, librarymetadata.ScopePosition}, {context.TrackOwner, librarymetadata.ScopePosition}, {context.RecordingOwner, librarymetadata.ScopePosition}} {
		if !override.owner.ManualOverridePresent {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(override.owner.ManualOverride, &fields); err != nil || fields == nil {
			return PublicationProviderTagProtection{}, fmt.Errorf("decode manual matching override: expected JSON object")
		}
		if _, ok := fields["title_override"]; ok {
			for _, key := range publication.PublicationTagKeysForField(override.scope, "title") {
				keys[key] = struct{}{}
			}
		}
		if _, ok := fields["artist_overrides"]; ok {
			for _, key := range publication.PublicationTagKeysForField(override.scope, "artist_credit") {
				keys[key] = struct{}{}
			}
		}
	}

	for key := range keys {
		result.Keys = append(result.Keys, key)
	}
	for field := range absent {
		result.ManualAbsentFields = append(result.ManualAbsentFields, field)
	}
	sort.Strings(result.Keys)
	sort.Strings(result.ManualAbsentFields)
	return result, nil
}

func tagProtectionScope(scope librarymetadata.Scope) librarymetadata.Scope {
	if scope == librarymetadata.ScopeRelease {
		return librarymetadata.ScopeRelease
	}
	return librarymetadata.ScopePosition
}
