package main

import (
	"os"
	"time"

	"github.com/traP-jp/kinugasa-recording/internal/gateway"
)

func configFromEnvironment() gateway.Config {
	return gateway.Config{
		FFmpegPath:       valueOrDefault(os.Getenv("KINUGASA_FFMPEG_BINARY"), "ffmpeg"),
		FFprobePath:      valueOrDefault(os.Getenv("KINUGASA_FFPROBE_BINARY"), "ffprobe"),
		RISTReceiverPath: valueOrDefault(os.Getenv("KINUGASA_RISTRECEIVER_BINARY"), "ristreceiver"),
		RISTAddress:      valueOrDefault(os.Getenv("KINUGASA_RIST_ADDRESS"), "0.0.0.0:9000"),
		RISTOutputURL:    valueOrDefault(os.Getenv("KINUGASA_RIST_OUTPUT_URL"), "udp://127.0.0.1:10000"),
		VideoRTPURL:      valueOrDefault(os.Getenv("KINUGASA_VIDEO_RTP_URL"), "rtp://127.0.0.1:8000?rtcpport=8001"),
		AudioRTPURL:      valueOrDefault(os.Getenv("KINUGASA_AUDIO_RTP_URL"), "rtp://127.0.0.1:8000?rtcpport=8001"),
		StatusAddress:    valueOrDefault(os.Getenv("KINUGASA_GATEWAY_STATUS_ADDRESS"), "127.0.0.1:9080"),
		RetryInterval:    2 * time.Second,
	}
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
