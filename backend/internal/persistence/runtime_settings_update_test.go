package persistence

import "testing"

func TestValidateRuntimeSettingValuesSourceFileConcurrency(t *testing.T) {
	for _, test := range []struct {
		name      string
		value     string
		wantError bool
	}{
		{name: "positive", value: "4"},
		{name: "zero", value: "0", wantError: true},
		{name: "negative", value: "-1", wantError: true},
		{name: "not an integer", value: "many", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateRuntimeSettingValues(map[string]string{
				"publication_format": "mka", "source_file_concurrency": test.value,
			})
			if (err != nil) != test.wantError {
				t.Fatalf("validation error = %v, wantError=%v", err, test.wantError)
			}
		})
	}
}
