// Package httpapi exposes the operator state to the Web UI.
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	operatorlib "github.com/comavius/kinugasa-recording/internal/operator"
	operatorvalidation "github.com/comavius/kinugasa-recording/internal/operator/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const shutdownTimeout = 10 * time.Second
const maximumRequestBodySize = 1 << 20

type requestIDContextKey struct{}

// Server serves the Web UI HTTP API.
type Server struct {
	reader    client.Reader
	namespace string
	creator   sessionCreationService
}

// NewServer constructs the HTTP API using a Kubernetes cache-backed reader.
func NewServer(reader client.Reader, namespace string, creators ...sessionCreationService) *Server {
	server := &Server{reader: reader, namespace: namespace}
	if len(creators) > 0 {
		server.creator = creators[0]
	}

	return server
}

// Handler returns the complete API handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/api/v1/sessions", s.sessions)
	mux.HandleFunc("/api/v1/sessions/", s.session)

	return requestIDMiddleware(mux)
}

func (s *Server) health(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) sessions(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		s.listSessions(response, request)
	case http.MethodPost:
		s.createSession(response, request)
	default:
		writeError(response, request, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method is not allowed", nil)
	}
}

func (s *Server) listSessions(response http.ResponseWriter, request *http.Request) {
	var sessions recordingv1alpha1.SessionList
	options := []client.ListOption{}
	if s.namespace != "" {
		options = append(options, client.InNamespace(s.namespace))
	}
	if err := s.reader.List(request.Context(), &sessions, options...); err != nil {
		writeError(response, request, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "Kubernetes API is unavailable", nil)
		return
	}

	resources := make([]sessionResource, 0, len(sessions.Items))
	for index := range sessions.Items {
		resources = append(resources, newSessionResource(&sessions.Items[index]))
	}
	sort.Slice(resources, func(left, right int) bool { return resources[left].Name < resources[right].Name })

	writeJSON(response, http.StatusOK, sessionsResponse{Sessions: resources})
}

func (s *Server) createSession(response http.ResponseWriter, request *http.Request) {
	if s.creator == nil {
		writeError(response, request, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "session creation is not configured", nil)
		return
	}

	request.Body = http.MaxBytesReader(response, request.Body, maximumRequestBodySize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var body createSessionRequest
	if err := decoder.Decode(&body); err != nil {
		writeError(response, request, http.StatusBadRequest, "INVALID_ARGUMENT", "request body must be a valid session creation object", nil)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(response, request, http.StatusBadRequest, "INVALID_ARGUMENT", "request body must contain one JSON object", nil)
		return
	}

	session, err := s.creator.Create(request.Context(), body.Name, request.Header.Get("Idempotency-Key"))
	switch {
	case err == nil:
		writeJSON(response, http.StatusCreated, sessionResponse{Session: newSessionResource(session)})
	case errors.Is(err, operatorlib.ErrInvalidName):
		writeError(response, request, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid session name", map[string]string{"field": "name"})
	case errors.Is(err, operatorlib.ErrSessionNameReserved):
		writeError(response, request, http.StatusConflict, "NAME_RESERVED", "session name has already been used", map[string]string{"field": "name", "value": body.Name})
	case errors.Is(err, operatorlib.ErrIdempotencyConflict):
		writeError(response, request, http.StatusConflict, "STATE_CONFLICT", "idempotency key was already used for another request", nil)
	case errors.Is(err, operatorlib.ErrDependencyUnavailable):
		writeError(response, request, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "session creation dependency is unavailable", nil)
	default:
		writeError(response, request, http.StatusInternalServerError, "INTERNAL", "session creation failed", nil)
	}
}

func (s *Server) session(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(response, request, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method is not allowed", nil)
		return
	}

	name := strings.TrimPrefix(request.URL.Path, "/api/v1/sessions/")
	if strings.Contains(name, "/") || operatorvalidation.Name(name) != nil {
		writeError(response, request, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid session name", map[string]string{"field": "sessionName"})
		return
	}

	var sessions recordingv1alpha1.SessionList
	options := []client.ListOption{}
	if s.namespace != "" {
		options = append(options, client.InNamespace(s.namespace))
	}
	if err := s.reader.List(request.Context(), &sessions, options...); err != nil {
		writeError(response, request, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "Kubernetes API is unavailable", nil)
		return
	}

	for index := range sessions.Items {
		if sessions.Items[index].Spec.Name == name {
			writeJSON(response, http.StatusOK, sessionResponse{Session: newSessionResource(&sessions.Items[index])})
			return
		}
	}

	writeError(response, request, http.StatusNotFound, "NOT_FOUND", "session was not found", map[string]string{"sessionName": name})
}

type sessionResource struct {
	Name   string                          `json:"name"`
	Spec   recordingv1alpha1.SessionSpec   `json:"spec"`
	Status recordingv1alpha1.SessionStatus `json:"status"`
}

type createSessionRequest struct {
	Name string `json:"name"`
}

type sessionCreationService interface {
	Create(context.Context, string, string) (*recordingv1alpha1.Session, error)
}

type sessionsResponse struct {
	Sessions []sessionResource `json:"sessions"`
}

type sessionResponse struct {
	Session sessionResource `json:"session"`
}

func newSessionResource(session *recordingv1alpha1.Session) sessionResource {
	return sessionResource{Name: session.Spec.Name, Spec: session.Spec, Status: session.Status}
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Details   any    `json:"details,omitempty"`
	RequestID string `json:"requestId"`
}

func writeError(response http.ResponseWriter, request *http.Request, status int, code, message string, details any) {
	requestID, _ := request.Context().Value(requestIDContextKey{}).(string)
	writeJSON(response, status, errorResponse{Error: errorBody{
		Code:      code,
		Message:   message,
		Details:   details,
		RequestID: requestID,
	}})
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(body)
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestID := newRequestID()
		response.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(response, request.WithContext(context.WithValue(request.Context(), requestIDContextKey{}, requestID)))
	})
}

func newRequestID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "request-id-unavailable"
	}

	return hex.EncodeToString(buffer)
}

// Runnable hosts an HTTP server as a controller-runtime manager runnable.
type Runnable struct {
	HTTPServer *http.Server
}

// Start starts serving and gracefully shuts down when the manager context ends.
func (r *Runnable) Start(ctx context.Context) error {
	errorChannel := make(chan error, 1)
	go func() {
		errorChannel <- r.HTTPServer.ListenAndServe()
	}()

	select {
	case err := <-errorChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		return r.HTTPServer.Shutdown(shutdownContext)
	}
}

// NeedLeaderElection ensures that session name reservations have a single writer.
func (r *Runnable) NeedLeaderElection() bool {
	return true
}
