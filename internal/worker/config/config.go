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
	FFmpegBinary      string
	RTPAddress        string
	RTSPAddress       string
	MediaAPIAddress   string
	MediaPath         string
	GatewayStatusURL  string
	LiveKitWHIPURL    string
	LiveKitWHIPToken  string
	RTPSDP            string
	InputPollInterval time.Duration
}

func FromEnvironment() (Config, error) {
	config := Config{
		SessionID:         os.Getenv("KINUGASA_SESSION_ID"),
		CameraIdentityID:  os.Getenv("KINUGASA_CAMERA_IDENTITY_ID"),
		SharedVolume:      valueOrDefault(os.Getenv("KINUGASA_SHARED_VOLUME"), "/recordings"),
		ConsoleAddress:    os.Getenv("KINUGASA_CONSOLE_GRPC_ADDRESS"),
		MediaMTXBinary:    valueOrDefault(os.Getenv("KINUGASA_MEDIAMTX_BINARY"), "mediamtx"),
		FFmpegBinary:      valueOrDefault(os.Getenv("KINUGASA_FFMPEG_BINARY"), "ffmpeg"),
		RTPAddress:        valueOrDefault(os.Getenv("KINUGASA_RTP_ADDRESS"), "0.0.0.0:8000"),
		RTSPAddress:       valueOrDefault(os.Getenv("KINUGASA_RTSP_ADDRESS"), "127.0.0.1:8554"),
		MediaAPIAddress:   valueOrDefault(os.Getenv("KINUGASA_MEDIA_API_ADDRESS"), "127.0.0.1:9997"),
		MediaPath:         valueOrDefault(os.Getenv("KINUGASA_MEDIA_PATH"), "camera"),
		GatewayStatusURL:  valueOrDefault(os.Getenv("KINUGASA_GATEWAY_STATUS_URL"), "http://127.0.0.1:9080/status"),
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
	config.RTPSDP = valueOrDefault(os.Getenv("KINUGASA_RTP_SDP"), defaultRTPSDP)
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

const defaultRTPSDP = `v=0
o=- 0 0 IN IP4 0.0.0.0
s=Kinugasa H264 Camera
c=IN IP4 0.0.0.0
t=0 0
m=video 8000 RTP/AVP 96
a=rtpmap:96 H264/90000
a=rtcp:8001
m=audio 8000 RTP/AVP 97
a=rtpmap:97 opus/48000/2
a=rtcp:8001
a=recvonly`
