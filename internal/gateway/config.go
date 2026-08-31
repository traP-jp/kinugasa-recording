package gateway

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	FFmpegPath       string
	FFprobePath      string
	RISTReceiverPath string
	RISTAddress      string
	RISTOutputURL    string
	VideoRTPURL      string
	AudioRTPURL      string
	StatusAddress    string
	RetryInterval    time.Duration
}

func (c Config) withDefaults() Config {
	if c.FFmpegPath == "" {
		c.FFmpegPath = "ffmpeg"
	}
	if c.FFprobePath == "" {
		c.FFprobePath = "ffprobe"
	}
	if c.RISTReceiverPath == "" {
		c.RISTReceiverPath = "ristreceiver"
	}
	if c.RISTOutputURL == "" {
		c.RISTOutputURL = "udp://127.0.0.1:10000"
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
	parsedOutput, err := url.Parse(c.RISTOutputURL)
	if err != nil || parsedOutput.Scheme != "udp" || parsedOutput.Host == "" {
		return fmt.Errorf("RIST receiver output URL must be an absolute udp URL")
	}
	if strings.TrimSpace(c.FFmpegPath) == "" || strings.TrimSpace(c.FFprobePath) == "" || strings.TrimSpace(c.RISTReceiverPath) == "" {
		return fmt.Errorf("FFmpeg, ffprobe, and ristreceiver paths must be set")
	}
	return nil
}

func (c Config) ristURL() string {
	return "rist://@" + c.RISTAddress
}
