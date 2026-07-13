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
		log.Printf("video-recorder: %v", err)
		os.Exit(1)
	}
}

func run() error {
	environment := media.Environment{}
	inputURL, err := environment.Required("INPUT_URL")
	if err != nil {
		return err
	}
	segmentLength, err := environment.Int("SEGMENT_LENGTH_SECONDS", 10)
	if err != nil {
		return err
	}
	root := environment.String("RECORDING_ROOT", "/recording")
	command, err := mediaffmpeg.RecorderCommand(mediaffmpeg.RecorderConfig{
		FFmpegPath:    environment.String("FFMPEG_PATH", "ffmpeg"),
		InputURL:      inputURL,
		RecordingRoot: root,
		SegmentLength: segmentLength,
	})
	if err != nil {
		return fmt.Errorf("build ffmpeg command: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return media.NewRecorder(root, command, environment.String("STATUS_ADDRESS", ":8080")).Run(ctx)
}
