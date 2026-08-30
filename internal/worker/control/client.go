package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
)

const (
	defaultInitialBackoff = 250 * time.Millisecond
	defaultMaximumBackoff = 10 * time.Second
)

type StateStore interface {
	WorkerID() string
	Hello(sessionID, cameraIdentityID string, observedAt time.Time) (*workerv1.WorkerHello, error)
	PendingEvents() ([]*workerv1.WorkerEvent, error)
	AcknowledgeEvents(eventIDs []string) error
	CommandResult(commandID string) (*workerv1.CommandResult, bool, error)
	SaveCommandResult(result *workerv1.CommandResult) error
}

type CommandExecutor interface {
	Execute(context.Context, *workerv1.WorkerCommand) (*workerv1.CommandResult, error)
}

type Config struct {
	SessionID        string
	CameraIdentityID string
	InitialBackoff   time.Duration
	MaximumBackoff   time.Duration
}

type Client struct {
	service  workerv1.ConsoleVideoWorkerServiceClient
	state    StateStore
	executor CommandExecutor
	config   Config
	logger   *slog.Logger
	now      func() time.Time
	wait     func(context.Context, time.Duration) error
	wake     chan struct{}
}

func NewClient(
	service workerv1.ConsoleVideoWorkerServiceClient,
	state StateStore,
	executor CommandExecutor,
	config Config,
	logger *slog.Logger,
) (*Client, error) {
	if service == nil {
		return nil, fmt.Errorf("worker control service must be set")
	}
	if state == nil {
		return nil, fmt.Errorf("worker state store must be set")
	}
	if executor == nil {
		return nil, fmt.Errorf("worker command executor must be set")
	}
	if config.SessionID == "" {
		return nil, fmt.Errorf("session ID must not be empty")
	}
	if config.CameraIdentityID == "" {
		return nil, fmt.Errorf("camera identity ID must not be empty")
	}
	if config.InitialBackoff == 0 {
		config.InitialBackoff = defaultInitialBackoff
	}
	if config.MaximumBackoff == 0 {
		config.MaximumBackoff = defaultMaximumBackoff
	}
	if config.InitialBackoff < 0 || config.MaximumBackoff < config.InitialBackoff {
		return nil, fmt.Errorf("worker control backoff must be positive and ordered")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		service:  service,
		state:    state,
		executor: executor,
		config:   config,
		logger:   logger,
		now:      time.Now,
		wait:     waitForRetry,
		wake:     make(chan struct{}, 1),
	}, nil
}

// NotifyEvents wakes the active stream after another worker subsystem has
// appended an event to the durable outbox. Notifications are coalesced.
func (c *Client) NotifyEvents() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Client) Run(ctx context.Context) error {
	backoff := c.config.InitialBackoff
	for {
		err := c.runStream(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, errShutdownAccepted) {
			return nil
		}
		if isPermanent(err) {
			return err
		}
		c.logger.Warn("worker control stream disconnected", "error", err, "retry_after", backoff)
		if err := c.wait(ctx, backoff); err != nil {
			return nil
		}
		backoff = nextBackoff(backoff, c.config.MaximumBackoff)
	}
}

type permanentError struct{ error }

func permanent(err error) error {
	return permanentError{error: err}
}

func isPermanent(err error) bool {
	var target permanentError
	if errors.As(err, &target) {
		return true
	}
	switch status.Code(err) {
	case codes.InvalidArgument,
		codes.FailedPrecondition,
		codes.PermissionDenied,
		codes.Unauthenticated,
		codes.Aborted:
		return true
	default:
		return false
	}
}

var errShutdownAccepted = errors.New("worker shutdown accepted")
