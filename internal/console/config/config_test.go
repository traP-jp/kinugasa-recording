package config

import (
	"testing"
	"time"
)

func TestFromEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://console@database/recording")
	t.Setenv("KINUGASA_S3_BUCKET", "recordings")
	t.Setenv("LIVEKIT_URL", "wss://livekit.example.com")
	t.Setenv("LIVEKIT_API_KEY", "api-key")
	t.Setenv("LIVEKIT_API_SECRET", "api-secret")
	t.Setenv("LISTEN_ADDRESS", "")
	t.Setenv("SHUTDOWN_TIMEOUT", "5s")

	config, err := FromEnvironment()
	if err != nil {
		t.Fatalf("FromEnvironment() error = %v", err)
	}
	if config.ObjectBucket != "recordings" || config.LiveKitURL != "wss://livekit.example.com" ||
		config.LiveKitPublicURL != "wss://livekit.example.com" || config.PreviewTTL != 5*time.Minute ||
		config.ListenAddress != ":8080" || config.GRPCAddress != ":9090" || config.ShutdownWait != 5*time.Second {
		t.Fatalf("FromEnvironment() = %+v", config)
	}
}

func TestFromEnvironmentAllowsPublicLiveKitURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://console@database/recording")
	t.Setenv("KINUGASA_S3_BUCKET", "recordings")
	t.Setenv("LIVEKIT_URL", "ws://livekit:7880")
	t.Setenv("LIVEKIT_PUBLIC_URL", "ws://127.0.0.1:7880")
	t.Setenv("LIVEKIT_API_KEY", "api-key")
	t.Setenv("LIVEKIT_API_SECRET", "api-secret")

	config, err := FromEnvironment()
	if err != nil {
		t.Fatalf("FromEnvironment() error = %v", err)
	}
	if config.LiveKitURL != "ws://livekit:7880" || config.LiveKitPublicURL != "ws://127.0.0.1:7880" {
		t.Fatalf("FromEnvironment() = %+v", config)
	}
}

func TestFromEnvironmentRequiresDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("KINUGASA_S3_BUCKET", "recordings")
	t.Setenv("LIVEKIT_URL", "wss://livekit.example.com")
	t.Setenv("LIVEKIT_API_KEY", "api-key")
	t.Setenv("LIVEKIT_API_SECRET", "api-secret")

	if _, err := FromEnvironment(); err == nil {
		t.Fatal("FromEnvironment() error = nil, want missing DATABASE_URL error")
	}
}

func TestFromEnvironmentRejectsInvalidLiveKitURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://console@database/recording")
	t.Setenv("KINUGASA_S3_BUCKET", "recordings")
	t.Setenv("LIVEKIT_URL", "https://livekit.example.com")
	t.Setenv("LIVEKIT_API_KEY", "api-key")
	t.Setenv("LIVEKIT_API_SECRET", "api-secret")
	if _, err := FromEnvironment(); err == nil {
		t.Fatal("FromEnvironment() error = nil")
	}
}

func TestFromEnvironmentRejectsInvalidPublicLiveKitURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://console@database/recording")
	t.Setenv("KINUGASA_S3_BUCKET", "recordings")
	t.Setenv("LIVEKIT_URL", "wss://livekit.example.com")
	t.Setenv("LIVEKIT_PUBLIC_URL", "http://livekit.example.com")
	t.Setenv("LIVEKIT_API_KEY", "api-key")
	t.Setenv("LIVEKIT_API_SECRET", "api-secret")
	if _, err := FromEnvironment(); err == nil {
		t.Fatal("FromEnvironment() error = nil")
	}
}
