package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"
)

// RunComponent supervises a set of processes and exposes their state over HTTP.
func RunComponent(ctx context.Context, commands map[string]Command, statusAddress string) error {
	names := make([]string, 0, len(commands))
	processes := make(map[string]*Supervisor, len(commands))
	for name, command := range commands {
		names = append(names, name)
		processes[name] = NewSupervisor(command, os.Stderr)
	}
	sort.Strings(names)

	for _, name := range names {
		if err := processes[name].Start(); err != nil {
			return errors.Join(fmt.Errorf("start %s: %w", name, err), stopProcesses(processes))
		}
	}

	statusServer := newStatusServer(statusAddress, processes)
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- statusServer.ListenAndServe()
	}()

	processExited := make(chan string, len(processes))
	observeProcessExits(processes, processExited)

	var runError error
	running := true
	for running {
		select {
		case <-ctx.Done():
			running = false
		case name := <-processExited:
			restartTimer := time.NewTimer(500 * time.Millisecond)
			select {
			case <-ctx.Done():
				restartTimer.Stop()
				running = false
			case <-restartTimer.C:
				if err := processes[name].Start(); err != nil {
					runError = fmt.Errorf("restart %s: %w", name, err)
					running = false
					break
				}
				observeProcessExit(name, processes[name], processExited)
			}
		case err := <-serverErrors:
			if !errors.Is(err, http.ErrServerClosed) {
				runError = fmt.Errorf("status server: %w", err)
			}
			running = false
		}
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = statusServer.Shutdown(shutdownContext)
	if err := stopProcesses(processes); err != nil && runError == nil {
		runError = err
	}

	return runError
}

func observeProcessExits(processes map[string]*Supervisor, exited chan<- string) {
	for name, process := range processes {
		observeProcessExit(name, process, exited)
	}
}

func observeProcessExit(name string, process *Supervisor, exited chan<- string) {
	go func() {
		<-process.Done()
		exited <- name
	}()
}

func newStatusServer(address string, processes map[string]*Supervisor) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, _ *http.Request) {
		for _, process := range processes {
			if process.Snapshot().Phase != ProcessPhaseRunning {
				writeStatusJSON(response, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy"})
				return
			}
		}
		writeStatusJSON(response, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/status", func(response http.ResponseWriter, _ *http.Request) {
		result := make(map[string]ProcessSnapshot, len(processes))
		for name, process := range processes {
			result[name] = process.Snapshot()
		}
		writeStatusJSON(response, http.StatusOK, map[string]any{"processes": result})
	})

	return &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func stopProcesses(processes map[string]*Supervisor) error {
	var stopError error
	for name, process := range processes {
		if err := StopWithTimeout(process); err != nil {
			stopError = errors.Join(stopError, fmt.Errorf("stop %s: %w", name, err))
		}
	}

	return stopError
}

func writeStatusJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
