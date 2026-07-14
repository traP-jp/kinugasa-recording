package media

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunComponentRestartsExitedProcess(t *testing.T) {
	t.Parallel()
	counter := filepath.Join(t.TempDir(), "starts")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunComponent(ctx, map[string]Command{"worker": {
			Path: os.Args[0],
			Args: []string{"-test.run=TestComponentRestartHelper"},
			Env:  []string{"KINUGASA_COMPONENT_HELPER=1", "KINUGASA_COMPONENT_COUNTER=" + counter},
		}}, "127.0.0.1:0")
	}()

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(counter)
		if err == nil && strings.TrimSpace(string(contents)) == "2" {
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("RunComponent() returned %v", err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("component process was not restarted")
}

func TestComponentRestartHelper(t *testing.T) {
	if os.Getenv("KINUGASA_COMPONENT_HELPER") != "1" {
		return
	}
	path := os.Getenv("KINUGASA_COMPONENT_COUNTER")
	contents, _ := os.ReadFile(path)
	count, _ := strconv.Atoi(strings.TrimSpace(string(contents)))
	count++
	if err := os.WriteFile(path, []byte(strconv.Itoa(count)), 0o600); err != nil {
		_, _ = io.WriteString(os.Stderr, err.Error())
		os.Exit(3)
	}
	if count == 1 {
		os.Exit(2)
	}
	time.Sleep(30 * time.Second)
}
