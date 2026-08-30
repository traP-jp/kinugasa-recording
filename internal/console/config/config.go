package config

import (
	"fmt"
	"os"
	"time"
)

const (
	defaultListenAddress   = ":8080"
	defaultGRPCAddress     = ":9090"
	defaultShutdownTimeout = 10 * time.Second
)

type Config struct {
	DatabaseURL   string
	ListenAddress string
	GRPCAddress   string
	ShutdownWait  time.Duration
}

func FromEnvironment() (Config, error) {
	config := Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		ListenAddress: valueOrDefault(os.Getenv("LISTEN_ADDRESS"), defaultListenAddress),
		GRPCAddress:   valueOrDefault(os.Getenv("GRPC_LISTEN_ADDRESS"), defaultGRPCAddress),
		ShutdownWait:  defaultShutdownTimeout,
	}
	if config.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if value := os.Getenv("SHUTDOWN_TIMEOUT"); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT must be a positive duration")
		}
		config.ShutdownWait = duration
	}
	return config, nil
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
