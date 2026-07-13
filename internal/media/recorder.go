package media

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

const recorderPollInterval = 100 * time.Millisecond

var segmentFileName = regexp.MustCompile(`^segment-[0-9]{20}\.ts$`)
var stagingSegmentFileName = regexp.MustCompile(`^segment-[0-9]{20}\.ts\.part$`)

type RecorderState struct {
	Phase     string    `json:"phase"`
	FileCount int       `json:"fileCount"`
	LastFile  string    `json:"lastFile,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
	Error     string    `json:"error,omitempty"`
}

// Recorder supervises ffmpeg and publishes only segments that ffmpeg reports closed.
type Recorder struct {
	root       string
	command    Command
	statusAddr string

	mutex sync.RWMutex
	state RecorderState
}

func NewRecorder(root string, command Command, statusAddress string) *Recorder {
	return &Recorder{
		root:       root,
		command:    command,
		statusAddr: statusAddress,
		state:      RecorderState{Phase: "Starting", UpdatedAt: time.Now().UTC()},
	}
}

// Run records until ctx is cancelled. Only a graceful ffmpeg stop creates recorder.done.
func (recorder *Recorder) Run(ctx context.Context) error {
	if err := recorder.prepare(); err != nil {
		return err
	}

	supervisor := NewSupervisor(recorder.command, os.Stderr)
	if err := supervisor.Start(); err != nil {
		recorder.setFailure(err)
		return err
	}
	recorder.setPhase("Recording")

	var statusServer *http.Server
	var serverErrors chan error
	if recorder.statusAddr != "" {
		statusServer = recorder.newStatusServer(supervisor)
		serverErrors = make(chan error, 1)
		go func() { serverErrors <- statusServer.ListenAndServe() }()
	}

	monitorContext, stopMonitor := context.WithCancel(context.Background())
	monitorErrors := make(chan error, 1)
	go func() { monitorErrors <- recorder.monitor(monitorContext) }()

	var runError error
	graceful := false
	monitorFinished := false
	select {
	case <-ctx.Done():
		recorder.setPhase("Stopping")
		if err := StopWithTimeout(supervisor); err != nil {
			runError = err
		} else {
			graceful = true
		}
	case <-supervisor.Done():
		runError = fmt.Errorf("recorder exited: %s", supervisor.Snapshot().Error)
	case err := <-monitorErrors:
		monitorFinished = true
		runError = fmt.Errorf("finalize segments: %w", err)
		_ = StopWithTimeout(supervisor)
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			runError = fmt.Errorf("status server: %w", err)
			_ = StopWithTimeout(supervisor)
		}
	}

	stopMonitor()
	if !monitorFinished {
		if err := <-monitorErrors; err != nil && runError == nil {
			runError = fmt.Errorf("finalize segments: %w", err)
		}
	}
	if err := recorder.finalizeListedSegments(); err != nil && runError == nil {
		runError = err
	}

	if statusServer != nil {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = statusServer.Shutdown(shutdownContext)
	}

	if graceful && runError == nil {
		recorder.setPhase("Completed")
		if err := recorder.writeDone(); err != nil {
			runError = err
		}
	}
	if runError != nil {
		recorder.setFailure(runError)
	}
	return runError
}

func (recorder *Recorder) prepare() error {
	if recorder.root == "" {
		return fmt.Errorf("recording root is required")
	}
	for _, directory := range []string{"staging", "ready", "state"} {
		if err := os.MkdirAll(filepath.Join(recorder.root, directory), 0o750); err != nil {
			return fmt.Errorf("create %s directory: %w", directory, err)
		}
	}
	if _, err := os.Stat(filepath.Join(recorder.root, "state", "recorder.done")); err == nil {
		return fmt.Errorf("recording is already complete")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect recorder completion: %w", err)
	}
	return recorder.writeState()
}

func (recorder *Recorder) monitor(ctx context.Context) error {
	ticker := time.NewTicker(recorderPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := recorder.finalizeListedSegments(); err != nil {
				return err
			}
		}
	}
}

func (recorder *Recorder) finalizeListedSegments() error {
	list, err := os.Open(filepath.Join(recorder.root, "state", "segments.list"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open segment list: %w", err)
	}

	scanner := bufio.NewScanner(list)
	for scanner.Scan() {
		partName := filepath.Base(scanner.Text())
		if !stagingSegmentFileName.MatchString(partName) {
			return fmt.Errorf("invalid segment list entry %q", scanner.Text())
		}
		name := partName[:len(partName)-len(".part")]
		if !segmentFileName.MatchString(name) {
			return fmt.Errorf("invalid segment file name %q", name)
		}
		readyPath := filepath.Join(recorder.root, "ready", name)
		if _, err := os.Stat(readyPath); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect ready segment: %w", err)
		}
		stagingPath := filepath.Join(recorder.root, "staging", partName)
		if err := os.Rename(stagingPath, readyPath); err != nil {
			return fmt.Errorf("promote %s: %w", name, err)
		}
		recorder.mutex.Lock()
		recorder.state.FileCount++
		recorder.state.LastFile = name
		recorder.state.UpdatedAt = time.Now().UTC()
		recorder.mutex.Unlock()
		if err := recorder.writeState(); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		_ = list.Close()
		return err
	}
	if err := list.Close(); err != nil {
		return fmt.Errorf("close segment list: %w", err)
	}
	return nil
}

func (recorder *Recorder) setPhase(phase string) {
	recorder.mutex.Lock()
	recorder.state.Phase = phase
	recorder.state.UpdatedAt = time.Now().UTC()
	recorder.mutex.Unlock()
	_ = recorder.writeState()
}

func (recorder *Recorder) setFailure(err error) {
	recorder.mutex.Lock()
	recorder.state.Phase = "Failed"
	recorder.state.Error = err.Error()
	recorder.state.UpdatedAt = time.Now().UTC()
	recorder.mutex.Unlock()
	_ = recorder.writeState()
}

func (recorder *Recorder) snapshot() RecorderState {
	recorder.mutex.RLock()
	defer recorder.mutex.RUnlock()
	return recorder.state
}

func (recorder *Recorder) writeState() error {
	return writeJSONAtomically(filepath.Join(recorder.root, "state", "recorder.json"), recorder.snapshot())
}

func (recorder *Recorder) writeDone() error {
	return writeJSONAtomically(filepath.Join(recorder.root, "state", "recorder.done"), recorder.snapshot())
}

func writeJSONAtomically(path string, value any) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".recorder-*")
	if err != nil {
		return fmt.Errorf("create temporary state: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	encoder := json.NewEncoder(temporary)
	if err := encoder.Encode(value); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode recorder state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync recorder state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close recorder state: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("publish recorder state: %w", err)
	}
	return nil
}

func (recorder *Recorder) newStatusServer(supervisor *Supervisor) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, _ *http.Request) {
		if supervisor.Snapshot().Phase != ProcessPhaseRunning {
			writeStatusJSON(response, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy"})
			return
		}
		writeStatusJSON(response, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/status", func(response http.ResponseWriter, _ *http.Request) {
		writeStatusJSON(response, http.StatusOK, map[string]any{"recorder": recorder.snapshot(), "process": supervisor.Snapshot()})
	})
	return &http.Server{Addr: recorder.statusAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
}
