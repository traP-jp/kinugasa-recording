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
