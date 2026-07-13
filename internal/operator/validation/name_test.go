package validation

import (
	"strings"
	"testing"
)

func TestName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		name      string
		wantError bool
	}{
		"letters digits and hyphens": {name: "Camera-01"},
		"leading hyphen":             {name: "-camera"},
		"empty":                      {name: "", wantError: true},
		"underscore":                 {name: "camera_01", wantError: true},
		"non ASCII":                  {name: "カメラ", wantError: true},
		"too long":                   {name: strings.Repeat("a", 256), wantError: true},
	}

	for testName, test := range tests {
		t.Run(testName, func(t *testing.T) {
			t.Parallel()

			errorValue := Name(test.name)
			if test.wantError && errorValue == nil {
				t.Fatal("Name() returned nil, expected an error")
			}
			if !test.wantError && errorValue != nil {
				t.Fatalf("Name() returned %v, expected nil", errorValue)
			}
		})
	}
}
