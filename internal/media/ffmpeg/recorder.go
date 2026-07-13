package ffmpeg

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/comavius/kinugasa-recording/internal/media"
)

// RecorderConfig configures one camera's stream-copy MPEG-TS recorder.
type RecorderConfig struct {
	FFmpegPath    string
	InputURL      string
	RecordingRoot string
	SegmentLength int
}

// RecorderCommand builds an ffmpeg command that records closed segments in a
// machine-readable list. The recorder process promotes listed files to ready/.
func RecorderCommand(config RecorderConfig) (media.Command, error) {
	if config.FFmpegPath == "" {
		config.FFmpegPath = "ffmpeg"
	}
	if config.InputURL == "" {
		return media.Command{}, fmt.Errorf("input URL is required")
	}
	if config.RecordingRoot == "" {
		return media.Command{}, fmt.Errorf("recording root is required")
	}
	if config.SegmentLength <= 0 {
		return media.Command{}, fmt.Errorf("segment length must be positive")
	}

	listPath := filepath.Join(config.RecordingRoot, "state", "segments.list")
	outputPattern := filepath.Join(config.RecordingRoot, "staging", "segment-%020d.ts.part")
	args := []string{
		"-nostdin", "-hide_banner", "-loglevel", "warning",
		"-progress", "pipe:1", "-nostats",
		"-i", config.InputURL,
		"-map", "0:v:0",
		"-c:v", "copy",
		"-f", "segment",
		"-segment_format", "mpegts",
		"-segment_time", strconv.Itoa(config.SegmentLength),
		"-reset_timestamps", "1",
		"-segment_list", listPath,
		"-segment_list_type", "flat",
		outputPattern,
	}

	return media.Command{Path: config.FFmpegPath, Args: args}, nil
}
