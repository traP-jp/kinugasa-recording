package gateway

import (
	"encoding/json"
	"net/http"
	"sync"
)

type State string

const (
	StateWaiting   State = "waiting"
	StateConnected State = "connected"
	StateError     State = "error"
)

type ErrorCode string

const (
	ErrorCodeInputUnavailable ErrorCode = "input_unavailable"
	ErrorCodeUnsupportedCodec ErrorCode = "unsupported_video_codec"
	ErrorCodeUnsupportedFPS   ErrorCode = "unsupported_frame_rate"
	ErrorCodeMediaPipeline    ErrorCode = "media_pipeline_failure"
)

type Status struct {
	State State     `json:"state"`
	Code  ErrorCode `json:"code,omitempty"`
	Error string    `json:"error,omitempty"`
}

type statusStore struct {
	mu     sync.RWMutex
	status Status
}

func newStatusStore() *statusStore {
	return &statusStore{status: Status{State: StateWaiting}}
}

func (s *statusStore) set(status Status) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
}

func (s *statusStore) handler(response http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	status := s.status
	s.mu.RUnlock()
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(status)
}
