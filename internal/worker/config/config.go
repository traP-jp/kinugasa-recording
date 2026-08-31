package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	SessionID         string
	CameraIdentityID  string
	SharedVolume      string
	ConsoleAddress    string
	MediaMTXBinary    string
	FFprobeBinary     string
	RTPAddress        string
	MPEGTSAddress     string
	RTSPAddress       string
	MediaAPIAddress   string
	MediaPath         string
	LiveKitWHIPURL    string
	LiveKitWHIPToken  string
	InputPollInterval time.Duration
}

func FromEnvironment() (Config, error) {
	config := Config{
		SessionID:         os.Getenv("KINUGASA_SESSION_ID"),
		CameraIdentityID:  os.Getenv("KINUGASA_CAMERA_IDENTITY_ID"),
		SharedVolume:      valueOrDefault(os.Getenv("KINUGASA_SHARED_VOLUME"), "/recordings"),
		ConsoleAddress:    os.Getenv("KINUGASA_CONSOLE_GRPC_ADDRESS"),
		MediaMTXBinary:    valueOrDefault(os.Getenv("KINUGASA_MEDIAMTX_BINARY"), "mediamtx"),
		FFprobeBinary:     valueOrDefault(os.Getenv("KINUGASA_FFPROBE_BINARY"), "ffprobe"),
		RTPAddress:        valueOrDefault(os.Getenv("KINUGASA_RTP_ADDRESS"), "0.0.0.0:8000"),
		MPEGTSAddress:     valueOrDefault(os.Getenv("KINUGASA_MPEGTS_ADDRESS"), "127.0.0.1:10000"),
		RTSPAddress:       valueOrDefault(os.Getenv("KINUGASA_RTSP_ADDRESS"), "127.0.0.1:8554"),
		MediaAPIAddress:   valueOrDefault(os.Getenv("KINUGASA_MEDIA_API_ADDRESS"), "127.0.0.1:9997"),
		MediaPath:         valueOrDefault(os.Getenv("KINUGASA_MEDIA_PATH"), "camera"),
		LiveKitWHIPURL:    os.Getenv("KINUGASA_LIVEKIT_WHIP_URL"),
		LiveKitWHIPToken:  os.Getenv("KINUGASA_LIVEKIT_WHIP_TOKEN"),
		InputPollInterval: 250 * time.Millisecond,
	}
	if config.SessionID == "" {
		return Config{}, fmt.Errorf("KINUGASA_SESSION_ID is required")
	}
	if config.CameraIdentityID == "" {
		return Config{}, fmt.Errorf("KINUGASA_CAMERA_IDENTITY_ID is required")
	}
	if config.ConsoleAddress == "" {
		return Config{}, fmt.Errorf("KINUGASA_CONSOLE_GRPC_ADDRESS is required")
	}
	if config.LiveKitWHIPURL == "" || config.LiveKitWHIPToken == "" {
		return Config{}, fmt.Errorf("KINUGASA_LIVEKIT_WHIP_URL and KINUGASA_LIVEKIT_WHIP_TOKEN are required")
	}
	if value := os.Getenv("KINUGASA_INPUT_POLL_INTERVAL"); value != "" {
		interval, err := time.ParseDuration(value)
		if err != nil || interval <= 0 {
			return Config{}, fmt.Errorf("KINUGASA_INPUT_POLL_INTERVAL must be a positive duration")
		}
		config.InputPollInterval = interval
	}
	return config, nil
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
