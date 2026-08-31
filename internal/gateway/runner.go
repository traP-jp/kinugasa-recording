package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	config = config.withDefaults()
	if err := config.validate(); err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	statuses := newStatusStore()
	listener, err := net.Listen("tcp", config.StatusAddress)
	if err != nil {
		return fmt.Errorf("listen for gateway status: %w", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(statuses.handler), ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()
	runContext, cancelRun := context.WithCancel(ctx)
	receiverDone, err := startRISTReceiver(runContext, config, logger)
	if err != nil {
		cancelRun()
		return err
	}
	monitoredReceiverDone := make(chan error, 1)
	go func() {
		receiverError := <-receiverDone
		monitoredReceiverDone <- receiverError
		cancelRun()
	}()

	for runContext.Err() == nil {
		statuses.set(Status{State: StateWaiting})
		result, failure := probe(runContext, config)
		if failure != nil {
			statuses.set(*failure)
			logger.Warn("camera input probe failed", "code", failure.Code, "error", failure.Error)
			if !waitRetry(runContext, config.RetryInterval) {
				break
			}
			continue
		}
		if runContext.Err() != nil {
			break
		}
		if err := relay(runContext, config, result, statuses, logger); err != nil && runContext.Err() == nil {
			statuses.set(Status{State: StateError, Code: ErrorCodeMediaPipeline, Error: "camera RTP relay failed"})
			logger.Warn("camera RTP relay stopped", "error", err)
		}
		if !waitRetry(runContext, config.RetryInterval) {
			break
		}
	}
	cancelRun()
	receiverError := <-monitoredReceiverDone
	if ctx.Err() != nil {
		return nil
	}
	if receiverError != nil {
		return fmt.Errorf("ristreceiver stopped: %w", receiverError)
	}
	return fmt.Errorf("ristreceiver stopped unexpectedly")
}

func relay(
	ctx context.Context,
	config Config,
	probe probeResult,
	statuses *statusStore,
	logger *slog.Logger,
) error {
	arguments := []string{
		"-nostdin", "-hide_banner", "-loglevel", "warning", "-progress", "pipe:1",
		"-i", config.RISTOutputURL,
		"-map", "0:v:0", "-c:v", "copy", "-an", "-f", "rtp", config.VideoRTPURL,
	}
	if probe.HasAudio {
		arguments = append(arguments,
			"-map", "0:a:0", "-vn", "-c:a", "libopus", "-ar", "48000", "-ac", "2", "-f", "rtp", config.AudioRTPURL,
		)
	}
	command := exec.CommandContext(ctx, config.FFmpegPath, arguments...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "progress=") {
				statuses.set(Status{State: StateConnected})
			}
		}
	}()
	errorDone := make(chan struct{})
	go func() {
		defer close(errorDone)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			logger.Warn("FFmpeg gateway", "message", scanner.Text())
		}
	}()
	err = command.Wait()
	<-progressDone
	<-errorDone
	statuses.set(Status{State: StateWaiting})
	if ctx.Err() != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return nil
		}
	}
	return err
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
