// Package ffmpeg builds commands for the media processing components.
package ffmpeg

import (
	"fmt"
	"strconv"

	"github.com/comavius/kinugasa-recording/internal/media"
)

type InputProtocol string

const (
	InputProtocolRIST InputProtocol = "rist"
	InputProtocolSRT  InputProtocol = "srt"
)

// FanoutConfig configures one camera's fanout process group.
type FanoutConfig struct {
	FFmpegPath            string
	Protocol              InputProtocol
	ListenPort            int
	RecordingListenPort   int
	PreviewRTMPURL        string
	PreviewLoopbackPort   int
	RecordingLoopbackPort int
}

// FanoutCommands receives H.264 once and splits stream-copy outputs to preview and recording paths.
func FanoutCommands(config FanoutConfig) (map[string]media.Command, error) {
	if config.FFmpegPath == "" {
		config.FFmpegPath = "ffmpeg"
	}
	if config.ListenPort < 1 || config.ListenPort > 65535 {
		return nil, fmt.Errorf("invalid input listen port %d", config.ListenPort)
	}
	if config.RecordingListenPort < 1 || config.RecordingListenPort > 65535 {
		return nil, fmt.Errorf("invalid recording listen port %d", config.RecordingListenPort)
	}
	if config.PreviewLoopbackPort == 0 {
		config.PreviewLoopbackPort = 10000
	}
	if config.RecordingLoopbackPort == 0 {
		config.RecordingLoopbackPort = 10001
	}
	if config.PreviewRTMPURL == "" {
		return nil, fmt.Errorf("preview RTMP URL is required")
	}

	var inputURL string
	switch config.Protocol {
	case InputProtocolRIST:
		inputURL = fmt.Sprintf("rist://0.0.0.0:%d?rist_profile=main", config.ListenPort)
	case InputProtocolSRT:
		inputURL = fmt.Sprintf("srt://0.0.0.0:%d?mode=listener&transtype=live", config.ListenPort)
	default:
		return nil, fmt.Errorf("unsupported input protocol %q", config.Protocol)
	}

	previewUDP := fmt.Sprintf("udp://127.0.0.1:%d?pkt_size=1316", config.PreviewLoopbackPort)
	recordingUDP := fmt.Sprintf("udp://127.0.0.1:%d?pkt_size=1316", config.RecordingLoopbackPort)
	common := []string{"-nostdin", "-hide_banner", "-loglevel", "warning", "-progress", "pipe:1", "-nostats"}
	ingestArgs := append([]string{}, common...)
	ingestArgs = append(ingestArgs,
		"-fflags", "+genpts",
		"-i", inputURL,
		"-map", "0:v:0",
		"-c:v", "copy",
		"-f", "tee",
		fmt.Sprintf("[f=mpegts:onfail=ignore]%s|[f=mpegts:onfail=ignore]%s", previewUDP, recordingUDP),
	)
	previewArgs := append([]string{}, common...)
	previewArgs = append(previewArgs,
		"-i", fmt.Sprintf("udp://127.0.0.1:%d?fifo_size=65536&overrun_nonfatal=1", config.PreviewLoopbackPort),
		"-map", "0:v:0",
		"-c:v", "copy",
		"-f", "flv",
		config.PreviewRTMPURL,
	)
	recordingArgs := append([]string{}, common...)
	recordingArgs = append(recordingArgs,
		"-i", fmt.Sprintf("udp://127.0.0.1:%d?fifo_size=65536&overrun_nonfatal=1", config.RecordingLoopbackPort),
		"-map", "0:v:0",
		"-c:v", "copy",
		"-f", "mpegts",
		"srt://0.0.0.0:"+strconv.Itoa(config.RecordingListenPort)+"?mode=listener&transtype=live",
	)

	return map[string]media.Command{
		"ingest":    {Path: config.FFmpegPath, Args: ingestArgs},
		"preview":   {Path: config.FFmpegPath, Args: previewArgs},
		"recording": {Path: config.FFmpegPath, Args: recordingArgs},
	}, nil
}
