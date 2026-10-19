package librarymetadata

import (
	"fmt"
	"reflect"
)

// ResolvedField is the winning optional metadata value and the scope that
// supplied it. Present is false when no scope contains a nonempty value.
type ResolvedField struct {
	Field   FieldValue
	Scope   Scope
	Present bool
}

// ResolveOptionalField resolves only fields intended to be inherited as
// optional metadata. Envelopes may be nil when an owner does not exist.
// Precedence is position, track, recording, then release.
func ResolveOptionalField(fieldName string, position, track, recording, release *Envelope) (ResolvedField, error) {
	definition, ok := fieldByName[fieldName]
	if !ok {
		return ResolvedField{}, fmt.Errorf("unknown metadata field %q", fieldName)
	}
	if definition.Canonical || definition.Type == TypeUUID || definition.Type == TypeUUIDList || definition.Type == TypeInteger {
		return ResolvedField{}, fmt.Errorf("metadata field %q is not optional inheritable metadata", fieldName)
	}

	owners := []struct {
		envelope *Envelope
		scope    Scope
	}{
		{position, ScopePosition},
		{track, ScopeTrack},
		{recording, ScopeRecording},
		{release, ScopeRelease},
	}
	for _, owner := range owners {
		if owner.envelope == nil {
			continue
		}
		if err := owner.envelope.Validate(); err != nil {
			return ResolvedField{}, fmt.Errorf("validate %s metadata: %w", owner.scope, err)
		}
		if owner.envelope.Scope != owner.scope {
			return ResolvedField{}, fmt.Errorf("metadata envelope scope %q does not match owner scope %q", owner.envelope.Scope, owner.scope)
		}
	}

	for _, owner := range owners {
		if owner.envelope == nil {
			continue
		}
		field, exists := owner.envelope.Desired[fieldName]
		if !exists || field.State == StateAbsent {
			continue
		}
		if field.State == StateExplicitEmpty {
			return ResolvedField{Field: field, Scope: owner.scope, Present: true}, nil
		}
		if !nonemptyOptionalValue(field.Value) {
			continue
		}
		return ResolvedField{Field: field, Scope: owner.scope, Present: true}, nil
	}
	return ResolvedField{}, nil
}

func nonemptyOptionalValue(value any) bool {
	if text, ok := value.(string); ok {
		return text != ""
	}
	// Envelope.Validate accepts any JSON-normalizable value, so emptiness must be
	// decided for every accepted slice representation (native typed slices,
	// []any decoded from JSON, arrays) through reflection rather than a fixed
	// type switch. Non-slice values keep their existing non-nil semantics, so an
	// explicit false boolean stays present.
	switch reflected := reflect.ValueOf(value); reflected.Kind() {
	case reflect.Invalid:
		return false
	case reflect.Slice, reflect.Array:
		return reflected.Len() > 0
	default:
		return true
	}
}
