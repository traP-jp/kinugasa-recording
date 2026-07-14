package media

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"path/filepath"
	"testing"
	"time"
)

func TestRecorderFinalizesOnlyListedSegments(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	recorder := NewRecorder(root, Command{}, "")
	if err := recorder.prepare(); err != nil {
		t.Fatalf("prepare() returned %v", err)
	}
	closedName := "segment-00000000000000000000.ts"
	unclosedName := "segment-00000000000000000001.ts"
	if err := os.WriteFile(filepath.Join(root, "staging", closedName+".part"), []byte("closed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "staging", unclosedName+".part"), []byte("open"), 0o600); err != nil {
		t.Fatal(err)
	}
	listEntry := filepath.Join(root, "staging", closedName+".part") + "\n"
	if err := os.WriteFile(filepath.Join(root, "state", "segments.list"), []byte(listEntry), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := recorder.finalizeListedSegments(); err != nil {
		t.Fatalf("finalizeListedSegments() returned %v", err)
	}
	if contents, err := os.ReadFile(filepath.Join(root, "ready", closedName)); err != nil || string(contents) != "closed" {
		t.Fatalf("ready segment = %q, %v", contents, err)
	}
	if _, err := os.Stat(filepath.Join(root, "staging", unclosedName+".part")); err != nil {
		t.Fatalf("unlisted part should remain: %v", err)
	}

	stateContents, err := os.ReadFile(filepath.Join(root, "state", "recorder.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state RecorderState
	if err := json.Unmarshal(stateContents, &state); err != nil {
		t.Fatal(err)
	}
	if state.FileCount != 1 || state.LastFile != closedName {
		t.Fatalf("state = %#v", state)
	}
}

func TestRecorderDoneIsCreatedAtomicallyOnlyOnCompletion(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	recorder := NewRecorder(root, Command{}, "")
	if err := recorder.prepare(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "recorder.done")); !os.IsNotExist(err) {
		t.Fatalf("recorder.done exists before completion: %v", err)
	}
	recorder.setPhase("Completed")
	if err := recorder.writeDone(); err != nil {
		t.Fatal(err)
	}
	var state RecorderState
	contents, err := os.ReadFile(filepath.Join(root, "state", "recorder.done"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, &state); err != nil {
		t.Fatal(err)
	}
	if state.Phase != "Completed" {
		t.Fatalf("done phase = %q", state.Phase)
	}
}

func TestRecorderRunFinalizesLastSegmentBeforeDone(t *testing.T) {
	root := t.TempDir()
	recorder := NewRecorder(root, Command{
		Path: os.Args[0],
		Args: []string{"-test.run=TestRecorderHelperProcess"},
		Env:  []string{"KINUGASA_RECORDER_HELPER=1", "KINUGASA_RECORDER_ROOT=" + root},
	}, "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- recorder.Run(ctx) }()

	firstReady := filepath.Join(root, "ready", "segment-00000000000000000000.ts")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(firstReady); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("recorder exited before first segment was finalized: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("first segment was not finalized")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("recorder did not stop")
	}

	for _, name := range []string{
		"segment-00000000000000000000.ts",
		"segment-00000000000000000001.ts",
	} {
		if _, err := os.Stat(filepath.Join(root, "ready", name)); err != nil {
			t.Errorf("ready segment %s: %v", name, err)
		}
	}
	contents, err := os.ReadFile(filepath.Join(root, "state", "recorder.done"))
	if err != nil {
		t.Fatal(err)
	}
	var state RecorderState
	if err := json.Unmarshal(contents, &state); err != nil {
		t.Fatal(err)
	}
	if state.Phase != "Completed" || state.FileCount != 2 {
		t.Fatalf("done state = %#v", state)
	}
}

func TestRecorderHelperProcess(t *testing.T) {
	if os.Getenv("KINUGASA_RECORDER_HELPER") != "1" {
		return
	}
	root := os.Getenv("KINUGASA_RECORDER_ROOT")
	writeSegment := func(index string, contents string) {
		name := "segment-" + index + ".ts.part"
		if err := os.WriteFile(filepath.Join(root, "staging", name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		listPath := filepath.Join(root, "state", "segments.list")
		list, err := os.OpenFile(listPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := list.WriteString(name + "\n"); err != nil {
			t.Fatal(err)
		}
		if err := list.Close(); err != nil {
			t.Fatal(err)
		}
	}

	writeSegment("00000000000000000000", "first")
	_, _ = os.Stdout.WriteString("frame=1\nprogress=continue\n")
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	<-interrupts
	writeSegment("00000000000000000001", "last")
}
