package media

import (
	"context"
	"testing"
	"time"
)

func TestSupervisorObservesProgressAndStopsGracefully(t *testing.T) {
	t.Parallel()

	supervisor := NewSupervisor(Command{
		Path: "sh",
		Args: []string{"-c", "printf 'frame=12\\nprogress=continue\\n'; exec sleep 30"},
	}, nil)
	if err := supervisor.Start(); err != nil {
		t.Fatalf("Start() returned %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for supervisor.Snapshot().Frame != 12 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if snapshot := supervisor.Snapshot(); snapshot.Frame != 12 || snapshot.LastProgressAt.IsZero() {
		t.Fatalf("snapshot = %#v, want observed frame 12", snapshot)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := supervisor.Stop(ctx); err != nil {
		t.Fatalf("Stop() returned %v", err)
	}
	if snapshot := supervisor.Snapshot(); snapshot.Phase != ProcessPhaseStopped {
		t.Fatalf("phase = %q, want %q", snapshot.Phase, ProcessPhaseStopped)
	}
}

func TestSupervisorReportsUnexpectedExit(t *testing.T) {
	t.Parallel()

	supervisor := NewSupervisor(Command{Path: "sh", Args: []string{"-c", "exit 7"}}, nil)
	if err := supervisor.Start(); err != nil {
		t.Fatalf("Start() returned %v", err)
	}
	select {
	case <-supervisor.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("process did not exit")
	}

	snapshot := supervisor.Snapshot()
	if snapshot.Phase != ProcessPhaseFailed || snapshot.ExitCode != 7 {
		t.Fatalf("snapshot = %#v, want failed exit code 7", snapshot)
	}
}
