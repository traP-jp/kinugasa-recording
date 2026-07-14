package media

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const processStopTimeout = 10 * time.Second

type ProcessPhase string

const (
	ProcessPhaseStarting ProcessPhase = "Starting"
	ProcessPhaseRunning  ProcessPhase = "Running"
	ProcessPhaseStopped  ProcessPhase = "Stopped"
	ProcessPhaseFailed   ProcessPhase = "Failed"
)

// Command describes one supervised child process.
type Command struct {
	Path string
	Args []string
	Env  []string
}

// ProcessSnapshot is a concurrency-safe view of a supervised process.
type ProcessSnapshot struct {
	Phase          ProcessPhase `json:"phase"`
	PID            int          `json:"pid,omitempty"`
	StartedAt      time.Time    `json:"startedAt,omitempty"`
	ExitedAt       time.Time    `json:"exitedAt,omitempty"`
	LastProgressAt time.Time    `json:"lastProgressAt,omitempty"`
	Frame          int64        `json:"frame,omitempty"`
	ExitCode       int          `json:"exitCode,omitempty"`
	Error          string       `json:"error,omitempty"`
}

// Supervisor starts, observes, and gracefully stops one child process.
type Supervisor struct {
	command Command
	stderr  io.Writer

	mutex    sync.RWMutex
	process  *exec.Cmd
	snapshot ProcessSnapshot
	done     chan struct{}
	stopping bool
}

// NewSupervisor creates a stopped process supervisor.
func NewSupervisor(command Command, stderr io.Writer) *Supervisor {
	if stderr == nil {
		stderr = io.Discard
	}

	return &Supervisor{command: command, stderr: stderr, snapshot: ProcessSnapshot{Phase: ProcessPhaseStopped}}
}

// Start starts the configured process and its progress observer.
func (supervisor *Supervisor) Start() error {
	supervisor.mutex.Lock()
	defer supervisor.mutex.Unlock()

	if supervisor.process != nil {
		return fmt.Errorf("process is already running")
	}

	command := exec.Command(supervisor.command.Path, supervisor.command.Args...)
	command.Env = append(os.Environ(), supervisor.command.Env...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open progress pipe: %w", err)
	}
	command.Stderr = supervisor.stderr
	supervisor.snapshot = ProcessSnapshot{Phase: ProcessPhaseStarting}
	if err := command.Start(); err != nil {
		supervisor.snapshot = ProcessSnapshot{Phase: ProcessPhaseFailed, Error: err.Error(), ExitedAt: time.Now().UTC()}
		return fmt.Errorf("start process: %w", err)
	}

	supervisor.process = command
	supervisor.done = make(chan struct{})
	supervisor.stopping = false
	supervisor.snapshot = ProcessSnapshot{
		Phase:     ProcessPhaseRunning,
		PID:       command.Process.Pid,
		StartedAt: time.Now().UTC(),
	}
	go supervisor.observeProgress(stdout)
	go supervisor.wait(command, supervisor.done)

	return nil
}

// Stop sends SIGINT, then kills the process if the context expires.
func (supervisor *Supervisor) Stop(ctx context.Context) error {
	supervisor.mutex.Lock()
	command := supervisor.process
	done := supervisor.done
	if command == nil {
		supervisor.mutex.Unlock()
		return nil
	}
	supervisor.stopping = true
	supervisor.mutex.Unlock()

	if err := command.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("interrupt process: %w", err)
	}

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill process after timeout: %w", err)
		}
		<-done
		return fmt.Errorf("stop process: %w", ctx.Err())
	}
}

// Snapshot returns the most recent process state.
func (supervisor *Supervisor) Snapshot() ProcessSnapshot {
	supervisor.mutex.RLock()
	defer supervisor.mutex.RUnlock()

	return supervisor.snapshot
}

// Done is closed when the current child process exits.
func (supervisor *Supervisor) Done() <-chan struct{} {
	supervisor.mutex.RLock()
	defer supervisor.mutex.RUnlock()

	return supervisor.done
}

func (supervisor *Supervisor) observeProgress(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), "=")
		if !found {
			continue
		}

		supervisor.mutex.Lock()
		switch key {
		case "frame":
			if frame, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				if frame > supervisor.snapshot.Frame {
					supervisor.snapshot.Frame = frame
					supervisor.snapshot.LastProgressAt = time.Now().UTC()
				}
			}
		}
		supervisor.mutex.Unlock()
	}
}

func (supervisor *Supervisor) wait(command *exec.Cmd, done chan struct{}) {
	err := command.Wait()
	exitedAt := time.Now().UTC()

	supervisor.mutex.Lock()
	defer supervisor.mutex.Unlock()
	defer close(done)

	supervisor.snapshot.ExitedAt = exitedAt
	supervisor.snapshot.PID = 0
	supervisor.snapshot.ExitCode = command.ProcessState.ExitCode()
	if supervisor.stopping {
		supervisor.snapshot.Phase = ProcessPhaseStopped
	} else {
		supervisor.snapshot.Phase = ProcessPhaseFailed
		if err == nil {
			supervisor.snapshot.Error = "process exited unexpectedly"
		} else {
			supervisor.snapshot.Error = err.Error()
		}
	}
	supervisor.process = nil
}

// StopWithTimeout stops a process with the component's standard grace period.
func StopWithTimeout(supervisor *Supervisor) error {
	ctx, cancel := context.WithTimeout(context.Background(), processStopTimeout)
	defer cancel()

	return supervisor.Stop(ctx)
}
