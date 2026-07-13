package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/comavius/kinugasa-recording/internal/media"
	mediaffmpeg "github.com/comavius/kinugasa-recording/internal/media/ffmpeg"
)

func main() {
	if err := run(); err != nil {
		log.Printf("livekit-ingress: %v", err)
		os.Exit(1)
	}
}

func run() error {
	environment := media.Environment{}
	whipURL, err := environment.Required("WHIP_URL")
	if err != nil {
		return err
	}
	transcode, err := environment.Bool("WHIP_TRANSCODE", false)
	if err != nil {
		return err
	}
	command, err := mediaffmpeg.IngressCommand(mediaffmpeg.IngressConfig{
		FFmpegPath:    environment.String("FFMPEG_PATH", "ffmpeg"),
		RTMPListenURL: environment.String("RTMP_LISTEN_URL", "rtmp://0.0.0.0:1935/live/camera"),
		WHIPURL:       whipURL,
		WHIPToken:     os.Getenv("WHIP_TOKEN"),
		Transcode:     transcode,
	})
	if err != nil {
		return fmt.Errorf("build ffmpeg command: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return media.RunComponent(ctx, map[string]media.Command{"ingress": command}, environment.String("STATUS_ADDRESS", ":8080"))
}
