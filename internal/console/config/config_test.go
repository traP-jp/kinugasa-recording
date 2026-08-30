package config

import (
	"testing"
	"time"
)

func TestFromEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://console@database/recording")
	t.Setenv("LISTEN_ADDRESS", "")
	t.Setenv("SHUTDOWN_TIMEOUT", "5s")

	config, err := FromEnvironment()
	if err != nil {
		t.Fatalf("FromEnvironment() error = %v", err)
	}
	if config.ListenAddress != ":8080" || config.GRPCAddress != ":9090" || config.ShutdownWait != 5*time.Second {
		t.Fatalf("FromEnvironment() = %+v", config)
	}
}

func TestFromEnvironmentRequiresDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	if _, err := FromEnvironment(); err == nil {
		t.Fatal("FromEnvironment() error = nil, want missing DATABASE_URL error")
	}
}
