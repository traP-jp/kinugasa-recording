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
		log.Printf("video-fanout: %v", err)
		os.Exit(1)
	}
}

func run() error {
	environment := media.Environment{}
	ristPort, err := environment.Int("RIST_PORT", 10000)
	if err != nil {
		return err
	}
	srtPort, err := environment.Int("SRT_PORT", 10001)
	if err != nil {
		return err
	}
	recordingPort, err := environment.Int("RECORDING_PORT", 12000)
	if err != nil {
		return err
	}
	previewURL, err := environment.Required("PREVIEW_RTMP_URL")
	if err != nil {
		return err
	}
	commands, err := mediaffmpeg.FanoutCommands(mediaffmpeg.FanoutConfig{
		FFmpegPath:          environment.String("FFMPEG_PATH", "ffmpeg"),
		RISTListenPort:      ristPort,
		SRTListenPort:       srtPort,
		RecordingListenPort: recordingPort,
		PreviewRTMPURL:      previewURL,
	})
	if err != nil {
		return fmt.Errorf("build ffmpeg commands: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return media.RunComponent(ctx, commands, environment.String("STATUS_ADDRESS", ":8080"))
}
