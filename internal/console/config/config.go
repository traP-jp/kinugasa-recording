package config

import (
	"fmt"
	"net/url"
	"os"
	"time"
)

const (
	defaultListenAddress   = ":8080"
	defaultGRPCAddress     = ":9090"
	defaultShutdownTimeout = 10 * time.Second
	defaultPreviewTokenTTL = 5 * time.Minute
)

type Config struct {
	DatabaseURL   string
	ObjectBucket  string
	LiveKitURL    string
	LiveKitAPIKey string
	LiveKitSecret string
	PreviewTTL    time.Duration
	ListenAddress string
	GRPCAddress   string
	ShutdownWait  time.Duration
}

func FromEnvironment() (Config, error) {
	config := Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		ObjectBucket:  os.Getenv("KINUGASA_S3_BUCKET"),
		LiveKitURL:    os.Getenv("LIVEKIT_URL"),
		LiveKitAPIKey: os.Getenv("LIVEKIT_API_KEY"),
		LiveKitSecret: os.Getenv("LIVEKIT_API_SECRET"),
		PreviewTTL:    defaultPreviewTokenTTL,
		ListenAddress: valueOrDefault(os.Getenv("LISTEN_ADDRESS"), defaultListenAddress),
		GRPCAddress:   valueOrDefault(os.Getenv("GRPC_LISTEN_ADDRESS"), defaultGRPCAddress),
		ShutdownWait:  defaultShutdownTimeout,
	}
	if config.DatabaseURL == "" || config.ObjectBucket == "" || config.LiveKitURL == "" ||
		config.LiveKitAPIKey == "" || config.LiveKitSecret == "" {
		return Config{}, fmt.Errorf("database, object bucket, and LiveKit configuration are required")
	}
	liveKitURL, err := url.Parse(config.LiveKitURL)
	if err != nil || liveKitURL.Host == "" || (liveKitURL.Scheme != "ws" && liveKitURL.Scheme != "wss") {
		return Config{}, fmt.Errorf("LIVEKIT_URL must be an absolute ws or wss URL")
	}
	if value := os.Getenv("SHUTDOWN_TIMEOUT"); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT must be a positive duration")
		}
		config.ShutdownWait = duration
	}
	if value := os.Getenv("LIVEKIT_TOKEN_TTL"); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return Config{}, fmt.Errorf("LIVEKIT_TOKEN_TTL must be a positive duration")
		}
		config.PreviewTTL = duration
	}
	return config, nil
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
