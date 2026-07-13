package ffmpeg

import (
	"fmt"

	"github.com/comavius/kinugasa-recording/internal/media"
)

// IngressConfig configures the RTMP-to-WHIP bridge for one camera.
type IngressConfig struct {
	FFmpegPath    string
	RTMPListenURL string
	WHIPURL       string
	WHIPToken     string
	Transcode     bool
}

// IngressCommand builds an RTMP listener and WHIP publisher command.
func IngressCommand(config IngressConfig) (media.Command, error) {
	if config.FFmpegPath == "" {
		config.FFmpegPath = "ffmpeg"
	}
	if config.RTMPListenURL == "" {
		return media.Command{}, fmt.Errorf("RTMP listen URL is required")
	}
	if config.WHIPURL == "" {
		return media.Command{}, fmt.Errorf("WHIP URL is required")
	}

	arguments := []string{
		"-nostdin", "-hide_banner", "-loglevel", "warning",
		"-progress", "pipe:1", "-nostats",
		"-listen", "1", "-i", config.RTMPListenURL,
		"-map", "0:v:0",
	}
	if config.Transcode {
		arguments = append(arguments,
			"-c:v", "libx264",
			"-profile:v", "baseline",
			"-tune", "zerolatency",
			"-bf", "0",
		)
	} else {
		arguments = append(arguments, "-c:v", "copy")
	}
	arguments = append(arguments, "-strict", "experimental", "-f", "whip")
	if config.WHIPToken != "" {
		arguments = append(arguments, "-authorization", config.WHIPToken)
	}
	arguments = append(arguments, config.WHIPURL)

	return media.Command{Path: config.FFmpegPath, Args: arguments}, nil
}
