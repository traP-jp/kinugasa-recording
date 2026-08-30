package domain

import "testing"

func TestNewID(t *testing.T) {
	t.Parallel()

	first, err := NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	second, err := NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	if !uuidPattern.MatchString(first) {
		t.Fatalf("NewID() = %q, want canonical UUID", first)
	}
	if first[14] != '4' {
		t.Fatalf("NewID() version = %q, want UUIDv4", first[14])
	}
	if first == second {
		t.Fatal("two generated UUIDs are equal")
	}
}

func TestValidateName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "single letter", value: "a", valid: true},
		{name: "hyphenated", value: "studio-camera-1", valid: true},
		{name: "maximum length", value: "a1234567890123456789012345678901", valid: true},
		{name: "empty", value: "", valid: false},
		{name: "too long", value: "a12345678901234567890123456789012", valid: false},
		{name: "leading digit", value: "1camera", valid: false},
		{name: "trailing hyphen", value: "camera-", valid: false},
		{name: "uppercase", value: "Camera", valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateName("name", test.value)
			if (err == nil) != test.valid {
				t.Fatalf("validateName(%q) error = %v, valid = %v", test.value, err, test.valid)
			}
		})
	}
}
