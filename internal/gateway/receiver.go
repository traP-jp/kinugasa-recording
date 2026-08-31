package gateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
)

func startRISTReceiver(ctx context.Context, config Config, logger *slog.Logger) (<-chan error, error) {
	command := exec.CommandContext(ctx, config.RISTReceiverPath,
		"--inputurl", config.ristURL(),
		"--outputurl", config.RISTOutputURL,
		"--profile", "1",
		"--statsinterval", "1000",
	)
	command.Stdout = io.Discard
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("capture ristreceiver logs: %w", err)
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start ristreceiver: %w", err)
	}
	logsDone := make(chan struct{})
	go func() {
		defer close(logsDone)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			logger.Info("ristreceiver", "message", scanner.Text())
		}
	}()
	done := make(chan error, 1)
	go func() {
		err := command.Wait()
		<-logsDone
		done <- err
	}()
	return done, nil
}
