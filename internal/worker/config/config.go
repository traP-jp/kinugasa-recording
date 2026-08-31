package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	s3store "github.com/traP-jp/kinugasa-recording/internal/worker/upload/objectstore/s3"
)

type Config struct {
	SessionID          string
	CameraIdentityID   string
	SharedVolume       string
	ConsoleAddress     string
	MediaMTXBinary     string
	FFprobeBinary      string
	RTPAddress         string
	MPEGTSAddress      string
	RTSPAddress        string
	MediaAPIAddress    string
	MediaPath          string
	LiveKitWHIPURL     string
	LiveKitWHIPToken   string
	InputPollInterval  time.Duration
	S3                 s3store.Config
	UploadPollInterval time.Duration
	UploadMaxAttempts  int
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
		S3: s3store.Config{
			Bucket:   os.Getenv("KINUGASA_S3_BUCKET"),
			Region:   os.Getenv("KINUGASA_S3_REGION"),
			Endpoint: os.Getenv("KINUGASA_S3_ENDPOINT"),
		},
		UploadPollInterval: time.Second,
		UploadMaxAttempts:  5,
	}
	pathStyle, err := strconv.ParseBool(valueOrDefault(os.Getenv("KINUGASA_S3_PATH_STYLE"), "false"))
	if err != nil {
		return Config{}, fmt.Errorf("KINUGASA_S3_PATH_STYLE must be a boolean")
	}
	config.S3.UsePathStyle = pathStyle
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
	if config.S3.Bucket == "" || config.S3.Region == "" {
		return Config{}, fmt.Errorf("KINUGASA_S3_BUCKET and KINUGASA_S3_REGION are required")
	}
	if value := os.Getenv("KINUGASA_INPUT_POLL_INTERVAL"); value != "" {
		interval, err := time.ParseDuration(value)
		if err != nil || interval <= 0 {
			return Config{}, fmt.Errorf("KINUGASA_INPUT_POLL_INTERVAL must be a positive duration")
		}
		config.InputPollInterval = interval
	}
	if value := os.Getenv("KINUGASA_UPLOAD_POLL_INTERVAL"); value != "" {
		config.UploadPollInterval, err = time.ParseDuration(value)
		if err != nil || config.UploadPollInterval <= 0 {
			return Config{}, fmt.Errorf("KINUGASA_UPLOAD_POLL_INTERVAL must be a positive duration")
		}
	}
	if value := os.Getenv("KINUGASA_UPLOAD_MAX_ATTEMPTS"); value != "" {
		config.UploadMaxAttempts, err = strconv.Atoi(value)
		if err != nil || config.UploadMaxAttempts <= 0 {
			return Config{}, fmt.Errorf("KINUGASA_UPLOAD_MAX_ATTEMPTS must be positive")
		}
	}
	return config, nil
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
