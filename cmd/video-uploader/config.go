package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	s3store "github.com/traP-jp/kinugasa-recording/internal/uploader/objectstore/s3"
)

type uploaderConfig struct {
	SessionID        string
	CameraIdentityID string
	SharedVolume     string
	ConsoleAddress   string
	S3               s3store.Config
	PollInterval     time.Duration
	MaximumAttempts  int
}

func uploaderConfigFromEnvironment() (uploaderConfig, error) {
	pathStyle, err := strconv.ParseBool(valueOrDefault(os.Getenv("KINUGASA_S3_PATH_STYLE"), "false"))
	if err != nil {
		return uploaderConfig{}, fmt.Errorf("KINUGASA_S3_PATH_STYLE must be a boolean")
	}
	config := uploaderConfig{
		SessionID: os.Getenv("KINUGASA_SESSION_ID"), CameraIdentityID: os.Getenv("KINUGASA_CAMERA_IDENTITY_ID"),
		SharedVolume:   valueOrDefault(os.Getenv("KINUGASA_SHARED_VOLUME"), "/recordings"),
		ConsoleAddress: os.Getenv("KINUGASA_CONSOLE_GRPC_ADDRESS"),
		S3: s3store.Config{Bucket: os.Getenv("KINUGASA_S3_BUCKET"), Region: os.Getenv("KINUGASA_S3_REGION"),
			Endpoint: os.Getenv("KINUGASA_S3_ENDPOINT"), UsePathStyle: pathStyle},
		PollInterval: time.Second, MaximumAttempts: 5,
	}
	if config.SessionID == "" || config.CameraIdentityID == "" || config.ConsoleAddress == "" ||
		config.S3.Bucket == "" || config.S3.Region == "" {
		return uploaderConfig{}, fmt.Errorf("uploader identity, console address, S3 bucket, and S3 region are required")
	}
	if value := os.Getenv("KINUGASA_UPLOAD_POLL_INTERVAL"); value != "" {
		config.PollInterval, err = time.ParseDuration(value)
		if err != nil || config.PollInterval <= 0 {
			return uploaderConfig{}, fmt.Errorf("KINUGASA_UPLOAD_POLL_INTERVAL must be positive")
		}
	}
	if value := os.Getenv("KINUGASA_UPLOAD_MAX_ATTEMPTS"); value != "" {
		config.MaximumAttempts, err = strconv.Atoi(value)
		if err != nil || config.MaximumAttempts <= 0 {
			return uploaderConfig{}, fmt.Errorf("KINUGASA_UPLOAD_MAX_ATTEMPTS must be positive")
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
