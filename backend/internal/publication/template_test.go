package publication

import (
	"errors"
	"reflect"
	"testing"
)

func TestTemplateFallbackAndQuotedPipe(t *testing.T) {
	template, err := ParseTemplate(`{ALBUM_TYPE | "Type | edition"}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := template.Render(func(token string) TokenValue {
		return TokenValue{Value: "", Present: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Type | edition"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Render() = %#v, want %#v", got, want)
	}
}

func TestTemplateQuotedLiteralMayContainClosers(t *testing.T) {
	template, err := ParseTemplate(`{ALBUM_TYPE | "Edition }"}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := template.Render(func(token string) TokenValue {
		return TokenValue{Value: "", Present: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Edition }"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Render() = %#v, want %#v", got, want)
	}

	// Escaped quotes and a brace inside the literal must not end the expression
	// or split the alternatives.
	template, err = ParseTemplate(`{ALBUM_PRIMARY_ARTIST_MBID_PART | "Deluxe \"Edition\" } | x" | ALBUM_PRIMARY_ARTIST_ID}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err = template.Render(func(token string) TokenValue {
		return TokenValue{Value: "", Present: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{`Deluxe "Edition" } | x`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Render() = %#v, want %#v", got, want)
	}
}

func TestTemplateQuotedLiteralEscapedBackslashes(t *testing.T) {
	// An escaped backslash must not escape the closing quote or the closing
	// brace. The literal is inspected directly because Render rejects backslashes.
	template, err := ParseTemplate(`{ALBUM_TYPE | "Edition } \\"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(template.components) != 1 || len(template.components[0]) != 1 {
		t.Fatalf("components = %#v", template.components)
	}
	choices := template.components[0][0].choices
	if len(choices) != 2 || choices[0].token != "ALBUM_TYPE" || choices[1].text != `Edition } \` {
		t.Fatalf("choices = %#v", choices)
	}
}

func TestTemplateRejectsUnclosedQuotedExpression(t *testing.T) {
	for _, source := range []string{
		`{ALBUM_TYPE | "unterminated}`,
		`{ALBUM_TYPE | "unterminated`,
		`{ALBUM_TYPE | "unterminated\`,
		`{ALBUM_TYPE | "escaped \"}`,
		`{ALBUM_TYPE | "Edition }"`,
	} {
		if _, err := ParseTemplate(source); !errors.Is(err, ErrInvalidTemplate) {
			t.Errorf("ParseTemplate(%q) error = %v", source, err)
		}
	}
}

func TestTemplateUnknownAndResolverErrorsDoNotFallback(t *testing.T) {
	if _, err := ParseTemplate(`{NOT_APPROVED | "fallback"}`); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("ParseTemplate error = %v", err)
	}
	template, err := ParseTemplate(`{ALBUM_PRIMARY_ARTIST | "fallback"}`)
	if err != nil {
		t.Fatal(err)
	}
	primaryErr := errors.New("primary artist is ambiguous")
	_, err = template.Render(func(string) TokenValue { return TokenValue{Err: primaryErr} })
	if !errors.Is(err, primaryErr) {
		t.Fatalf("Render error = %v, want primary error", err)
	}
}

func TestTemplateNumericFormattingAndValidation(t *testing.T) {
	template, err := ParseTemplate(`{DISC_NUMBER:02}-{TRACK_NUMBER:02}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := template.Render(func(token string) TokenValue {
		value := map[string]string{"DISC_NUMBER": "2", "TRACK_NUMBER": "12"}[token]
		return TokenValue{Value: value, Present: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"02-12"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Render() = %#v, want %#v", got, want)
	}
	if _, err := ParseTemplate(`{TRACK_NAME:02}`); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("unsupported format error = %v", err)
	}
	_, err = template.Render(func(string) TokenValue { return TokenValue{Value: "x", Present: true} })
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("invalid numeric error = %v", err)
	}
}

func TestTemplateRejectsMalformedAndUnsafePaths(t *testing.T) {
	for _, source := range []string{"", "/absolute", `a\\b`, "a/", "//x", "{TRACK_NAME", "TRACK_NAME}", `{"unterminated}`, "{TRACK_NAME | }"} {
		if _, err := ParseTemplate(source); !errors.Is(err, ErrInvalidTemplate) {
			t.Errorf("ParseTemplate(%q) error = %v", source, err)
		}
	}
	for _, value := range []string{"../escape", "a/b", `a\\b`, "\x00", ".", ".."} {
		template, err := ParseTemplate(`{TRACK_NAME}`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = template.Render(func(string) TokenValue { return TokenValue{Value: value, Present: true} })
		if !errors.Is(err, ErrInvalidValue) {
			t.Errorf("Render(%q) error = %v", value, err)
		}
	}
}

func TestTemplateUnicodeAndStableOutput(t *testing.T) {
	template, err := ParseTemplate(`{TRACK_NAME} - {EXT}`)
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(token string) TokenValue {
		return TokenValue{Value: map[string]string{"TRACK_NAME": "東京 – Café", "EXT": "m4a"}[token], Present: true}
	}
	one, err := template.Render(resolve)
	if err != nil {
		t.Fatal(err)
	}
	two, err := template.Render(resolve)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(one, two) || !reflect.DeepEqual(one, []string{"東京 – Café - m4a"}) {
		t.Fatalf("unstable/unexpected output: %#v, %#v", one, two)
	}
}

func TestDefaultTemplateExact(t *testing.T) {
	want := `{ALBUM_PRIMARY_ARTIST} [{ALBUM_PRIMARY_ARTIST_MBID_PART | ALBUM_PRIMARY_ARTIST_ID}]/{ALBUM_TYPE | "Unclassified"}/{ALBUM_YEAR | "Unknown year"} - {ALBUM_NAME} [{ALBUM_MBID_PART | ALBUM_ID}]/{DISC_NUMBER:02}-{TRACK_NUMBER:02} - {TRACK_NAME}.{EXT}`
	if DefaultTemplate != want {
		t.Fatalf("DefaultTemplate = %q, want %q", DefaultTemplate, want)
	}
	if _, err := ParseTemplate(DefaultTemplate); err != nil {
		t.Fatal(err)
	}
}
