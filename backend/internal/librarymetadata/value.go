package librarymetadata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"time"

	"github.com/google/uuid"
)

const EnvelopeVersion = 1

type ValueState string

const (
	StateAbsent        ValueState = "absent"
	StateValue         ValueState = "value"
	StateExplicitEmpty ValueState = "explicit_empty"
)

type Authority string

const (
	AuthorityAutomatic Authority = "automatic"
	AuthorityManual    Authority = "manual"
)

type ProvenanceKind string

const (
	ProvenanceProvider       ProvenanceKind = "provider"
	ProvenanceSourceMetadata ProvenanceKind = "source_metadata"
	ProvenanceManual         ProvenanceKind = "manual"
)

// Provenance describes where this field value came from, not the audio source
// used for publication. SourceField and Reference are only supplied when real
// evidence exists.
type Provenance struct {
	Kind        ProvenanceKind `json:"kind"`
	SourceField string         `json:"source_field,omitempty"`
	Reference   string         `json:"reference,omitempty"`
}

// FieldValue is one desired local value, including its independently-held
// authority and metadata provenance.
type FieldValue struct {
	State      ValueState `json:"state"`
	Value      any        `json:"value,omitempty"`
	Authority  Authority  `json:"authority"`
	Provenance Provenance `json:"provenance"`

	// valuePresent records whether the decoded document supplied "value". It
	// separates an omitted value from an explicitly supplied null one.
	valuePresent bool
}

// UnmarshalJSON records whether the wire document supplied a value so an
// explicit null cannot be mistaken for an omitted value. Unknown properties
// remain rejected even though the envelope decoder cannot see inside this
// custom unmarshaller.
func (fieldValue *FieldValue) UnmarshalJSON(data []byte) error {
	type wire struct {
		State      ValueState      `json:"state"`
		Value      json.RawMessage `json:"value"`
		Authority  Authority       `json:"authority"`
		Provenance Provenance      `json:"provenance"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded wire
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := ensureEOF(decoder); err != nil {
		return err
	}
	result := FieldValue{State: decoded.State, Authority: decoded.Authority, Provenance: decoded.Provenance}
	if decoded.Value != nil {
		valueDecoder := json.NewDecoder(bytes.NewReader(decoded.Value))
		valueDecoder.UseNumber()
		var value any
		if err := valueDecoder.Decode(&value); err != nil {
			return err
		}
		result.Value = value
		result.valuePresent = true
	}
	*fieldValue = result
	return nil
}

// Envelope is the versioned local_metadata desired-values representation.
type Envelope struct {
	Version int                   `json:"version"`
	Scope   Scope                 `json:"scope"`
	Desired map[string]FieldValue `json:"desired"`
}

type ArtistCredit struct {
	ArtistID   string  `json:"artist_id"`
	ArtistName string  `json:"artist_name"`
	CreditName string  `json:"credit_name"`
	JoinPhrase string  `json:"join_phrase"`
	MBID       *string `json:"mbid,omitempty"`
}

type Contributor struct {
	Name     string  `json:"name"`
	Role     string  `json:"role"`
	ArtistID *string `json:"artist_id,omitempty"`
	MBID     *string `json:"mbid,omitempty"`
}

type Work struct {
	Name string  `json:"name"`
	MBID *string `json:"mbid,omitempty"`
}

type Movement struct {
	Name   string `json:"name"`
	Number int    `json:"number"`
	Count  int    `json:"count"`
}

var partialDatePattern = regexp.MustCompile(`^\d{4}(?:-\d{2}(?:-\d{2})?)?$`)

// DecodeEnvelope strictly decodes and validates a local_metadata envelope.
func DecodeEnvelope(data []byte) (Envelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var envelope Envelope
	if err := decoder.Decode(&envelope); err != nil {
		return Envelope{}, fmt.Errorf("decode local metadata: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return Envelope{}, fmt.Errorf("decode local metadata: %w", err)
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return nil
}

func (envelope Envelope) Validate() error {
	if envelope.Version != EnvelopeVersion {
		return fmt.Errorf("unsupported local metadata version %d", envelope.Version)
	}
	if err := validateScope(envelope.Scope); err != nil {
		return err
	}
	if envelope.Desired == nil {
		return fmt.Errorf("local metadata desired fields are required")
	}
	for name, fieldValue := range envelope.Desired {
		definition, exists := fieldByName[name]
		if !exists {
			return fmt.Errorf("unknown metadata field %q", name)
		}
		if !containsScope(definition.Scopes, envelope.Scope) {
			return fmt.Errorf("metadata field %q is not valid for scope %q", name, envelope.Scope)
		}
		if err := fieldValue.validate(definition); err != nil {
			return fmt.Errorf("metadata field %q: %w", name, err)
		}
	}
	return nil
}

func containsScope(scopes []Scope, target Scope) bool {
	for _, scope := range scopes {
		if scope == target {
			return true
		}
	}
	return false
}

func (fieldValue FieldValue) validate(definition FieldDefinition) error {
	switch fieldValue.State {
	case StateAbsent, StateExplicitEmpty:
		if fieldValue.valuePresent || fieldValue.Value != nil {
			return fmt.Errorf("state %q must not include a value", fieldValue.State)
		}
	case StateValue:
		if fieldValue.Value == nil {
			return fmt.Errorf("state %q requires a value", fieldValue.State)
		}
		if err := validateTypedValue(definition.Type, fieldValue.Value); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown metadata value state %q", fieldValue.State)
	}
	if fieldValue.Authority != AuthorityAutomatic && fieldValue.Authority != AuthorityManual {
		return fmt.Errorf("unknown metadata authority %q", fieldValue.Authority)
	}
	switch fieldValue.Provenance.Kind {
	case ProvenanceProvider, ProvenanceSourceMetadata:
		if fieldValue.Authority != AuthorityAutomatic {
			return fmt.Errorf("%s provenance requires automatic authority", fieldValue.Provenance.Kind)
		}
	case ProvenanceManual:
		if fieldValue.Authority != AuthorityManual {
			return fmt.Errorf("manual provenance requires manual authority")
		}
	default:
		return fmt.Errorf("unknown metadata provenance kind %q", fieldValue.Provenance.Kind)
	}
	return nil
}

func validateTypedValue(valueType ValueType, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode typed value: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return fmt.Errorf("decode typed value: %w", err)
	}
	value = normalized
	switch valueType {
	case TypeString:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("value must be a string")
		}
	case TypeStringList:
		if err := validateStringList(value); err != nil {
			return err
		}
	case TypeUUID:
		if err := validateUUIDString(value, "value"); err != nil {
			return err
		}
	case TypeUUIDList:
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("value must be a UUID list")
		}
		for _, item := range items {
			if err := validateUUIDString(item, "list entry"); err != nil {
				return err
			}
		}
	case TypeArtistCredit:
		return validateObjectList(value, map[string]bool{"artist_id": true, "artist_name": true, "credit_name": true, "join_phrase": true, "mbid": true}, func(item map[string]any) error {
			if err := requiredStringFields(item, "artist_id", "artist_name", "credit_name", "join_phrase"); err != nil {
				return err
			}
			if err := validateUUIDString(item["artist_id"], "artist_id"); err != nil {
				return err
			}
			return optionalUUID(item, "mbid")
		})
	case TypeContributors:
		return validateObjectList(value, map[string]bool{"name": true, "role": true, "artist_id": true, "mbid": true}, func(item map[string]any) error {
			if err := requiredStringFields(item, "name", "role"); err != nil {
				return err
			}
			if err := optionalUUID(item, "artist_id"); err != nil {
				return err
			}
			return optionalUUID(item, "mbid")
		})
	case TypeWork:
		return validateObjectList(value, map[string]bool{"name": true, "mbid": true}, func(item map[string]any) error {
			if err := requiredStringFields(item, "name"); err != nil {
				return err
			}
			return optionalUUID(item, "mbid")
		})
	case TypeMovement:
		item, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("value must be a movement object")
		}
		if err := rejectUnknownKeys(item, map[string]bool{"name": true, "number": true, "count": true}); err != nil {
			return err
		}
		if name, ok := item["name"].(string); !ok || name == "" {
			return fmt.Errorf("movement name must be a non-empty string")
		}
		for _, key := range []string{"number", "count"} {
			number, ok := positiveInteger(item[key])
			if !ok || number <= 0 {
				return fmt.Errorf("movement %s must be a positive integer", key)
			}
		}
	case TypeInteger:
		if number, ok := positiveInteger(value); !ok || number <= 0 {
			return fmt.Errorf("value must be a positive integer")
		}
	case TypeNumber:
		number, ok := numericValue(value)
		if !ok || number <= 0 || math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("value must be a positive finite number")
		}
	case TypeBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("value must be a boolean")
		}
	case TypeDate:
		date, ok := value.(string)
		if !ok || !validPartialDate(date) {
			return fmt.Errorf("value must be a valid partial date in YYYY, YYYY-MM, or YYYY-MM-DD form")
		}
	case TypeAdvisory:
		advisory, ok := value.(string)
		if !ok || (advisory != "explicit" && advisory != "clean") {
			return fmt.Errorf("value must be explicit or clean")
		}
	default:
		return fmt.Errorf("unsupported metadata value type %q", valueType)
	}
	return nil
}

func validateStringList(value any) error {
	items, ok := value.([]any)
	if !ok {
		if _, stringOK := value.([]string); stringOK {
			return nil
		}
		return fmt.Errorf("value must be a string list")
	}
	for _, item := range items {
		if _, ok := item.(string); !ok {
			return fmt.Errorf("list entries must be strings")
		}
	}
	return nil
}

func validateObjectList(value any, allowed map[string]bool, validate func(map[string]any) error) error {
	items, ok := value.([]any)
	if !ok {
		return fmt.Errorf("value must be a list of objects")
	}
	for index, entry := range items {
		item, ok := entry.(map[string]any)
		if !ok {
			return fmt.Errorf("list entry %d must be an object", index)
		}
		if err := rejectUnknownKeys(item, allowed); err != nil {
			return fmt.Errorf("list entry %d: %w", index, err)
		}
		if err := validate(item); err != nil {
			return fmt.Errorf("list entry %d: %w", index, err)
		}
	}
	return nil
}

func requiredStringFields(item map[string]any, keys ...string) error {
	for _, key := range keys {
		if _, ok := item[key].(string); !ok {
			return fmt.Errorf("%s must be a string", key)
		}
	}
	return nil
}

func rejectUnknownKeys(item map[string]any, allowed map[string]bool) error {
	for key := range item {
		if !allowed[key] {
			return fmt.Errorf("unknown value field %q", key)
		}
	}
	return nil
}

func optionalUUID(item map[string]any, key string) error {
	value, exists := item[key]
	if !exists || value == nil {
		return nil
	}
	return validateUUIDString(value, key)
}

func validateUUIDString(value any, field string) error {
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("%s must be a uuid string", field)
	}
	if _, err := uuid.Parse(text); err != nil {
		return fmt.Errorf("%s must be a uuid string", field)
	}
	return nil
}

func positiveInteger(value any) (int64, bool) {
	switch number := value.(type) {
	case int:
		return int64(number), true
	case int64:
		return number, true
	case json.Number:
		parsed, err := number.Int64()
		return parsed, err == nil
	case float64:
		if number == math.Trunc(number) && number <= math.MaxInt64 && number >= math.MinInt64 {
			return int64(number), true
		}
	}
	return 0, false
}

func numericValue(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func validPartialDate(value string) bool {
	if !partialDatePattern.MatchString(value) {
		return false
	}
	if value[:4] == "0000" {
		return false
	}
	switch len(value) {
	case 4:
		return true
	case 7:
		month := value[5:7]
		parsed, err := time.Parse("2006-01", value)
		return err == nil && parsed.Format("2006-01") == value && month != "00"
	case 10:
		parsed, err := time.Parse("2006-01-02", value)
		return err == nil && parsed.Format("2006-01-02") == value
	default:
		return false
	}
}
