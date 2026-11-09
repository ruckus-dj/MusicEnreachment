package service

import (
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func TestSourceRepublishDowngradeQualityIsWarning(t *testing.T) {
	if got := sourceRepublishQualityWarningCode(publication.QualityWorse); got != "output_quality_would_downgrade" {
		t.Fatalf("downgrade warning code = %q", got)
	}
}

func TestMatchSourceRepublishPlanningSettingsRejectsNamespaceChanges(t *testing.T) {
	capturedValues := map[string]string{
		settings.OutputCaseSensitiveKey:        "true",
		settings.OutputUnicodeNormalizationKey: "none",
	}
	captured, err := settings.ParsePublicationPlanningValues(capturedValues)
	if err != nil {
		t.Fatalf("parse captured planning settings: %v", err)
	}

	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "output case sensitivity", key: settings.OutputCaseSensitiveKey, value: "false"},
		{name: "output Unicode normalization", key: settings.OutputUnicodeNormalizationKey, value: "nfc"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changedValues := map[string]string{
				settings.OutputCaseSensitiveKey:        capturedValues[settings.OutputCaseSensitiveKey],
				settings.OutputUnicodeNormalizationKey: capturedValues[settings.OutputUnicodeNormalizationKey],
			}
			changedValues[test.key] = test.value

			_, matches, err := matchSourceRepublishPlanningSettings(captured, changedValues)
			if err != nil {
				t.Fatalf("compare changed planning settings: %v", err)
			}
			if matches {
				t.Fatal("changed namespace settings matched the captured planning settings")
			}
		})
	}
}
