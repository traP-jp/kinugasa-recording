package preview

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

const defaultRetryInterval = 2 * time.Second

type Config struct {
	FFmpegPath     string
	InputArguments []string
	WHIPURL        string
	BearerToken    string
	RetryInterval  time.Duration
}

type Relay struct {
	config Config
	logger *slog.Logger
}

func NewRelay(config Config, logger *slog.Logger) (*Relay, error) {
	if strings.TrimSpace(config.FFmpegPath) == "" {
		config.FFmpegPath = "ffmpeg"
	}
	if len(config.InputArguments) == 0 {
		return nil, fmt.Errorf("FFmpeg input arguments must be set")
	}
	endpoint, err := url.Parse(config.WHIPURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return nil, fmt.Errorf("LiveKit WHIP URL must be an absolute http or https URL")
	}
	if strings.TrimSpace(config.BearerToken) == "" {
		return nil, fmt.Errorf("LiveKit WHIP bearer token must be set")
	}
	if config.RetryInterval == 0 {
		config.RetryInterval = defaultRetryInterval
	}
	if config.RetryInterval < 0 {
		return nil, fmt.Errorf("preview retry interval must be positive")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Relay{config: config, logger: logger}, nil
}

func (r *Relay) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		if err := r.runOnce(ctx); err != nil && ctx.Err() == nil {
			r.logger.Warn("LiveKit preview relay stopped; retrying", "error", err)
		}
		if !waitRetry(ctx, r.config.RetryInterval) {
			break
		}
	}
	return nil
}

func (r *Relay) runOnce(ctx context.Context) error {
	command := exec.CommandContext(ctx, r.config.FFmpegPath, r.arguments()...)
	stderr, err := command.StderrPipe()
	if err != nil {
		return fmt.Errorf("open preview FFmpeg log pipe: %w", err)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start preview FFmpeg: %w", err)
	}
	logDone := make(chan struct{})
	go func() {
		defer close(logDone)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			r.logger.Warn("preview FFmpeg", "message", scanner.Text())
		}
	}()
	waitError := command.Wait()
	<-logDone
	if ctx.Err() != nil {
		return nil
	}
	if waitError == nil {
		return fmt.Errorf("preview FFmpeg exited unexpectedly")
	}
	return fmt.Errorf("run preview FFmpeg: %w", waitError)
}

func (r *Relay) arguments() []string {
	arguments := []string{"-nostdin", "-hide_banner", "-loglevel", "warning"}
	arguments = append(arguments, r.config.InputArguments...)
	return append(arguments,
		"-map", "0:v:0", "-map", "0:a:0?", "-c", "copy",
		"-f", "whip", "-authorization", r.config.BearerToken, r.config.WHIPURL,
	)
}

func waitRetry(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
