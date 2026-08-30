package gateway

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	FFmpegPath    string
	FFprobePath   string
	RISTAddress   string
	VideoRTPURL   string
	AudioRTPURL   string
	StatusAddress string
	RetryInterval time.Duration
}

func (c Config) withDefaults() Config {
	if c.FFmpegPath == "" {
		c.FFmpegPath = "ffmpeg"
	}
	if c.FFprobePath == "" {
		c.FFprobePath = "ffprobe"
	}
	if c.RetryInterval <= 0 {
		c.RetryInterval = 2 * time.Second
	}
	return c
}

func (c Config) validate() error {
	if _, _, err := net.SplitHostPort(c.RISTAddress); err != nil {
		return fmt.Errorf("RIST address must contain a host and port: %w", err)
	}
	if _, _, err := net.SplitHostPort(c.StatusAddress); err != nil {
		return fmt.Errorf("status address must contain a host and port: %w", err)
	}
	for name, value := range map[string]string{"video RTP URL": c.VideoRTPURL, "audio RTP URL": c.AudioRTPURL} {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "rtp" || parsed.Host == "" {
			return fmt.Errorf("%s must be an absolute rtp URL", name)
		}
	}
	if strings.TrimSpace(c.FFmpegPath) == "" || strings.TrimSpace(c.FFprobePath) == "" {
		return fmt.Errorf("FFmpeg and ffprobe paths must be set")
	}
	return nil
}

func (c Config) ristURL() string {
	return "rist://@" + c.RISTAddress
}
