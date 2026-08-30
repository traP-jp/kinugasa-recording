package config

import (
	"strings"
	"testing"
	"time"
)

func TestFromEnvironment(t *testing.T) {
	t.Setenv("KINUGASA_SESSION_ID", "session-id")
	t.Setenv("KINUGASA_CAMERA_IDENTITY_ID", "camera-id")
	t.Setenv("KINUGASA_CONSOLE_GRPC_ADDRESS", "console:9090")
	t.Setenv("KINUGASA_LIVEKIT_WHIP_URL", "https://ingress.example.com/whip")
	t.Setenv("KINUGASA_LIVEKIT_WHIP_TOKEN", "stream-key")
	t.Setenv("KINUGASA_INPUT_POLL_INTERVAL", "100ms")
	config, err := FromEnvironment()
	if err != nil {
		t.Fatalf("FromEnvironment() error = %v", err)
	}
	if config.SharedVolume != "/recordings" || config.RTPAddress != "0.0.0.0:8000" ||
		config.ConsoleAddress != "console:9090" || config.InputPollInterval != 100*time.Millisecond {
		t.Fatalf("FromEnvironment() = %+v", config)
	}
	if !strings.Contains(config.RTPSDP, "H264/90000") {
		t.Fatalf("default RTP SDP = %q", config.RTPSDP)
	}
}

func TestFromEnvironmentRequiresIdentityAndConsole(t *testing.T) {
	if _, err := FromEnvironment(); err == nil {
		t.Fatal("FromEnvironment() error = nil without required identity")
	}
}
