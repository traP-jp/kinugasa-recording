package preview

import (
	"log/slog"
	"slices"
	"testing"
	"time"
)

func TestRelayBuildsWHIPCopyCommand(t *testing.T) {
	relay, err := NewRelay(Config{
		FFmpegPath:     "ffmpeg",
		InputArguments: []string{"-rtsp_transport", "tcp", "-i", "rtsp://127.0.0.1:8554/camera"},
		WHIPURL:        "https://ingress.example.com/whip",
		BearerToken:    "stream-key",
		RetryInterval:  time.Second,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewRelay() error = %v", err)
	}
	wantTail := []string{
		"-map", "0:v:0", "-map", "0:a:0?", "-c", "copy",
		"-f", "whip", "-authorization", "stream-key", "https://ingress.example.com/whip",
	}
	arguments := relay.arguments()
	if !slices.Equal(arguments[len(arguments)-len(wantTail):], wantTail) {
		t.Fatalf("arguments() = %q", arguments)
	}
}

func TestRelayRejectsInvalidConfiguration(t *testing.T) {
	tests := []Config{
		{InputArguments: []string{"-i", "input"}, WHIPURL: "ws://livekit.example.com", BearerToken: "token"},
		{InputArguments: []string{"-i", "input"}, WHIPURL: "https://livekit.example.com/whip"},
		{WHIPURL: "https://livekit.example.com/whip", BearerToken: "token"},
	}
	for _, config := range tests {
		if _, err := NewRelay(config, nil); err == nil {
			t.Fatalf("NewRelay(%+v) error = nil", config)
		}
	}
}
