package cameraconnection

import (
	"strings"
	"testing"
)

func TestConfigValidatesRISTNodePortPublication(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "valid", mutate: func(config *Config) {
			config.RISTPublicHost = "127.0.0.1"
			config.RISTNodePortMin = 32000
			config.RISTNodePortMax = 32099
		}},
		{name: "missing public host", mutate: func(config *Config) {
			config.RISTNodePortMin = 32000
			config.RISTNodePortMax = 32099
		}, want: "public host"},
		{name: "partial range", mutate: func(config *Config) {
			config.RISTPublicHost = "127.0.0.1"
			config.RISTNodePortMin = 32000
		}, want: "configured together"},
		{name: "reversed range", mutate: func(config *Config) {
			config.RISTPublicHost = "127.0.0.1"
			config.RISTNodePortMin = 32099
			config.RISTNodePortMax = 32000
		}, want: "invalid"},
		{name: "missing encryption pepper", mutate: func(config *Config) {
			config.RISTEncryptionPepper = ""
		}, want: "encryption pepper"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := testConfig()
			test.mutate(&config)
			err := config.validate()
			if test.want == "" && err != nil {
				t.Fatalf("validate() error = %v", err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("validate() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestDeriveRISTSecretUsesStableIDs(t *testing.T) {
	connection := testConnection()
	const pepper = "test-pepper-with-at-least-32-bytes"
	if got, want := deriveRISTSecret(connection, pepper), "6Su4bZRzK9axvHoYBLaWTzMCDZRWJolpsCKrDs2wnz8"; got != want {
		t.Fatalf("deriveRISTSecret() = %q, want %q", got, want)
	}

	renamed := connection.DeepCopy()
	renamed.Spec.SessionName = "renamed-session"
	renamed.Spec.CameraName = "renamed-camera"
	if got := deriveRISTSecret(renamed, pepper); got != deriveRISTSecret(connection, pepper) {
		t.Fatalf("deriveRISTSecret() changed after display name change: %q", got)
	}

	differentIdentity := connection.DeepCopy()
	differentIdentity.Spec.CameraIdentityID = "019c240f-3eb4-72d6-a6fa-adfe1df795c8"
	if got := deriveRISTSecret(differentIdentity, pepper); got == deriveRISTSecret(connection, pepper) {
		t.Fatal("deriveRISTSecret() did not change with camera identity ID")
	}
}
