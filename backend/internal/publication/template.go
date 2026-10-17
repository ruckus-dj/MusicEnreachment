package publication

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// DefaultTemplate is the owner-approved A15 publication path template.
const DefaultTemplate = `{ALBUM_PRIMARY_ARTIST} [{ALBUM_PRIMARY_ARTIST_MBID_PART | ALBUM_PRIMARY_ARTIST_ID}]/{ALBUM_TYPE | "Unclassified"}/{ALBUM_YEAR | "Unknown year"} - {ALBUM_NAME} [{ALBUM_MBID_PART | ALBUM_ID}]/{DISC_NUMBER:02}-{TRACK_NUMBER:02} - {TRACK_NAME}.{EXT}`

var (
	ErrInvalidTemplate = errors.New("invalid publication template")
	ErrInvalidValue    = errors.New("invalid publication template value")
)

type templatePart struct {
	text    string
	token   string
	width   int
	choices []templatePart
}

type templateComponent []templatePart

// Template is a parsed relative-path publication template.
type Template struct{ components []templateComponent }

// TokenValue distinguishes a missing value from an explicitly present empty value.
// Err is reserved for resolution failures and always stops fallback evaluation.
type TokenValue struct {
	Value   string
	Present bool
	Err     error
}

// TokenResolver returns a value for an approved token. A missing resolver/value
// participates in fallback; a resolver error never does.
type TokenResolver func(token string) TokenValue

// ParseTemplate parses a restricted template expression; it does not access the
// filesystem or sanitize metadata.
func ParseTemplate(source string) (Template, error) {
	if source == "" || strings.HasPrefix(source, "/") || strings.HasPrefix(source, `\`) {
		return Template{}, fmt.Errorf("%w: path must be nonempty and relative", ErrInvalidTemplate)
	}
	components := make([]templateComponent, 1)
	var literal strings.Builder
	flush := func() {
		if literal.Len() > 0 {
			components[len(components)-1] = append(components[len(components)-1], templatePart{text: literal.String()})
			literal.Reset()
		}
	}
	for i := 0; i < len(source); {
		switch source[i] {
		case '/':
			flush()
			if len(components[len(components)-1]) == 0 {
				return Template{}, fmt.Errorf("%w: empty path component", ErrInvalidTemplate)
			}
			components = append(components, nil)
			i++
		case '\\':
			return Template{}, fmt.Errorf("%w: backslash path separators are not allowed", ErrInvalidTemplate)
		case '}':
			return Template{}, fmt.Errorf("%w: unmatched closing brace", ErrInvalidTemplate)
		case '{':
			flush()
			end, err := expressionEnd(source, i+1)
			if err != nil {
				return Template{}, err
			}
			alternatives, err := parseAlternatives(source[i+1 : end])
			if err != nil {
				return Template{}, err
			}
			components[len(components)-1] = append(components[len(components)-1], templatePart{choices: alternatives})
			i = end + 1
		default:
			literal.WriteByte(source[i])
			i++
		}
	}
	flush()
	if len(components[len(components)-1]) == 0 {
		return Template{}, fmt.Errorf("%w: empty path component", ErrInvalidTemplate)
	}
	return Template{components: components}, nil
}

// expressionEnd returns the index of the closing brace of the expression that
// starts at start. Quoted literals use the same escape rules as
// parseAlternatives, so pipes, quotes, and braces inside them never terminate
// the expression.
func expressionEnd(source string, start int) (int, error) {
	quoted, escaped := false, false
	for i := start; i < len(source); i++ {
		c := source[i]
		if c == '}' && !quoted {
			return i, nil
		}
		if quoted {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
		}
	}
	if quoted {
		return 0, fmt.Errorf("%w: unterminated quoted literal", ErrInvalidTemplate)
	}
	return 0, fmt.Errorf("%w: unclosed expression", ErrInvalidTemplate)
}

// Render resolves the template into path components, never a filesystem path.
func (t Template) Render(resolve TokenResolver) ([]string, error) {
	if len(t.components) == 0 {
		return nil, fmt.Errorf("%w: template is not parsed", ErrInvalidTemplate)
	}
	result := make([]string, 0, len(t.components))
	for _, component := range t.components {
		var value strings.Builder
		for _, part := range component {
			if part.choices != nil {
				for _, choice := range part.choices {
					text, present, err := renderChoice(choice, resolve)
					if err != nil {
						return nil, err
					}
					if present {
						value.WriteString(text)
						break
					}
				}
				continue
			}
			if part.token == "" {
				value.WriteString(part.text)
				continue
			}
			text, present, err := renderChoice(part, resolve)
			if err != nil {
				return nil, err
			}
			if present {
				value.WriteString(text)
			}
		}
		text := value.String()
		if text == "" || text == "." || text == ".." || strings.ContainsAny(text, "/\\\x00") {
			return nil, fmt.Errorf("%w: rendered path component is empty or unsafe", ErrInvalidValue)
		}
		result = append(result, text)
	}
	return result, nil
}

func renderChoice(part templatePart, resolve TokenResolver) (string, bool, error) {
	if part.token == "" {
		return part.text, part.text != "", nil
	}
	resolved := TokenValue{}
	if resolve != nil {
		resolved = resolve(part.token)
	}
	if resolved.Err != nil {
		return "", false, fmt.Errorf("resolve %s: %w", part.token, resolved.Err)
	}
	if !resolved.Present || resolved.Value == "" {
		return "", false, nil
	}
	if part.width != 0 {
		text, err := padNumber(resolved.Value, part.width)
		if err != nil {
			return "", false, fmt.Errorf("%w: %s requires a nonnegative integer", ErrInvalidValue, part.token)
		}
		return text, true, nil
	}
	return resolved.Value, true, nil
}

func parseAlternatives(source string) ([]templatePart, error) {
	var parts []templatePart
	start, quoted, escaped := 0, false, false
	for i := 0; i <= len(source); i++ {
		if i < len(source) {
			c := source[i]
			if quoted {
				if escaped {
					escaped = false
					continue
				}
				if c == '\\' {
					escaped = true
					continue
				}
				if c == '"' {
					quoted = false
				}
				continue
			}
			if c == '"' {
				quoted = true
				continue
			}
			if c != '|' {
				continue
			}
		}
		term := strings.TrimSpace(source[start:i])
		if term == "" {
			return nil, fmt.Errorf("%w: empty alternative", ErrInvalidTemplate)
		}
		if strings.HasPrefix(term, `"`) {
			value, err := strconv.Unquote(term)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid quoted literal", ErrInvalidTemplate)
			}
			parts = append(parts, templatePart{text: value})
		} else {
			name, width, ok := strings.Cut(term, ":")
			if !validToken(name) {
				return nil, fmt.Errorf("%w: unknown token %q", ErrInvalidTemplate, name)
			}
			modifier := 0
			if ok {
				if width != "02" || !numericToken(name) {
					return nil, fmt.Errorf("%w: unsupported modifier", ErrInvalidTemplate)
				}
				modifier = 2
			}
			parts = append(parts, templatePart{token: name, width: modifier})
		}
		start = i + 1
	}
	if quoted {
		return nil, fmt.Errorf("%w: unterminated quoted literal", ErrInvalidTemplate)
	}
	return parts, nil
}

func validToken(token string) bool {
	if token == "ALBUM_ARTIST" || token == "TRACK_ARTIST" || token == "ALBUM_ARTIST_ID" || token == "TRACK_ARTIST_ID" ||
		token == "ALBUM_ARTIST_MBID" || token == "TRACK_ARTIST_MBID" || token == "ALBUM_ARTIST_MBID_PART" || token == "TRACK_ARTIST_MBID_PART" ||
		token == "ALBUM_PRIMARY_ARTIST" || token == "TRACK_PRIMARY_ARTIST" ||
		token == "ALBUM_PRIMARY_ARTIST_ID" || token == "TRACK_PRIMARY_ARTIST_ID" || token == "ALBUM_PRIMARY_ARTIST_MBID" || token == "TRACK_PRIMARY_ARTIST_MBID" ||
		token == "ALBUM_PRIMARY_ARTIST_MBID_PART" || token == "TRACK_PRIMARY_ARTIST_MBID_PART" || token == "ALBUM_NAME" || token == "TRACK_NAME" ||
		token == "ALBUM_YEAR" || token == "DISC_NUMBER" || token == "TRACK_NUMBER" || token == "ALBUM_TYPE" || token == "ALBUM_PRIMARY_TYPE" || token == "ALBUM_SECONDARY_TYPES" ||
		token == "ARTIST_ID" || token == "ALBUM_ID" || token == "ALBUM_MBID" || token == "ALBUM_MBID_PART" || token == "POSITION_ID" || token == "EXT" {
		return true
	}
	return false
}

func numericToken(token string) bool {
	return token == "DISC_NUMBER" || token == "TRACK_NUMBER" || token == "ALBUM_YEAR"
}

func padNumber(value string, width int) (string, error) {
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", width, n), nil
}
