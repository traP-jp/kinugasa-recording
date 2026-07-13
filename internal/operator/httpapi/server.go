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
	livekitapi "github.com/comavius/kinugasa-recording/internal/livekit"
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
	cameras   cameraMutationService
	tokens    previewTokenService
	takes     takeMutationService
}

func (s *Server) WithTakeService(service takeMutationService) *Server {
	s.takes = service
	return s
}

func (s *Server) WithPreviewTokenService(service previewTokenService) *Server {
	s.tokens = service
	return s
}

// WithCameraService configures camera mutation endpoints.
func (s *Server) WithCameraService(service cameraMutationService) *Server {
	s.cameras = service
	return s
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
	mux.HandleFunc("/api/v1/livekit/token", s.liveKitToken)

	return requestIDMiddleware(mux)
}

func (s *Server) liveKitToken(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(response, request, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method is not allowed", nil)
		return
	}
	if s.tokens == nil {
		writeError(response, request, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "LiveKit token service is not configured", nil)
		return
	}
	if request.Body != nil {
		request.Body = http.MaxBytesReader(response, request.Body, maximumRequestBodySize)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body struct{}
		if err := decoder.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(response, request, http.StatusBadRequest, "INVALID_ARGUMENT", "request body must be an empty object", nil)
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeError(response, request, http.StatusBadRequest, "INVALID_ARGUMENT", "request body must contain one JSON object", nil)
			return
		}
	}
	issued, err := s.tokens.Issue()
	if err != nil {
		writeError(response, request, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "LiveKit token generation failed", nil)
		return
	}
	writeJSON(response, http.StatusOK, previewTokenResponse{
		ServerURL: issued.ServerURL, RoomName: issued.RoomName, ParticipantToken: issued.ParticipantToken, ExpiresAt: issued.ExpiresAt,
	})
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
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/sessions/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || operatorvalidation.Name(parts[0]) != nil {
		writeError(response, request, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid session name", map[string]string{"field": "sessionName"})
		return
	}
	if len(parts) == 1 {
		if request.Method != http.MethodGet {
			writeError(response, request, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method is not allowed", nil)
			return
		}
		s.getSession(response, request, parts[0])
		return
	}
	if parts[1] == "takes" {
		if len(parts) == 2 && request.Method == http.MethodPost {
			s.startTake(response, request, parts[0])
			return
		}
		if len(parts) == 4 && parts[3] == "stop" && request.Method == http.MethodPost {
			s.stopTake(response, request, parts[0], parts[2])
			return
		}
		writeError(response, request, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method is not allowed", nil)
		return
	}
	if parts[1] != "cameras" || len(parts) > 3 {
		writeError(response, request, http.StatusNotFound, "NOT_FOUND", "endpoint was not found", nil)
		return
	}
	if len(parts) == 2 && request.Method == http.MethodPost {
		s.addCamera(response, request, parts[0])
		return
	}
	if len(parts) == 3 && request.Method == http.MethodDelete {
		s.deleteCamera(response, request, parts[0], parts[2])
		return
	}
	writeError(response, request, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method is not allowed", nil)
}

func (s *Server) startTake(response http.ResponseWriter, request *http.Request, sessionName string) {
	if s.takes == nil {
		writeError(response, request, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "take mutation is not configured", nil)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumRequestBodySize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var body takeMutationRequest
	if err := decoder.Decode(&body); err != nil {
		writeError(response, request, http.StatusBadRequest, "INVALID_ARGUMENT", "request body must be a valid take creation object", nil)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(response, request, http.StatusBadRequest, "INVALID_ARGUMENT", "request body must contain one JSON object", nil)
		return
	}
	result, err := s.takes.Start(request.Context(), sessionName, body.Name, body.CameraNames, request.Header.Get("Idempotency-Key"))
	if err != nil {
		s.writeTakeError(response, request, err, body.Name)
		return
	}
	excluded := make([]excludedCameraResponse, len(result.ExcludedCameras))
	for index, camera := range result.ExcludedCameras {
		excluded[index] = excludedCameraResponse{Name: camera.Name, Reason: camera.Reason}
	}
	writeJSON(response, http.StatusAccepted, takeMutationResponse{Take: takeSummary{Name: result.Take.Name, Phase: recordingv1alpha1.TakePhasePending, CameraNames: result.Take.CameraNames}, ExcludedCameras: excluded})
}

func (s *Server) stopTake(response http.ResponseWriter, request *http.Request, sessionName, takeName string) {
	if operatorvalidation.Name(takeName) != nil {
		writeError(response, request, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid take name", nil)
		return
	}
	if s.takes == nil {
		writeError(response, request, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "take mutation is not configured", nil)
		return
	}
	take, err := s.takes.Stop(request.Context(), sessionName, takeName, request.Header.Get("Idempotency-Key"))
	if err != nil {
		s.writeTakeError(response, request, err, takeName)
		return
	}
	writeJSON(response, http.StatusAccepted, takeMutationResponse{Take: takeSummary{Name: take.Name, Phase: recordingv1alpha1.TakePhaseStopping, CameraNames: take.CameraNames}})
}

func (s *Server) writeTakeError(response http.ResponseWriter, request *http.Request, err error, takeName string) {
	switch {
	case errors.Is(err, operatorlib.ErrInvalidName):
		writeError(response, request, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid take, camera, or session name", nil)
	case errors.Is(err, operatorlib.ErrSessionNotFound), errors.Is(err, operatorlib.ErrTakeNotFound):
		writeError(response, request, http.StatusNotFound, "NOT_FOUND", "session or take was not found", nil)
	case errors.Is(err, operatorlib.ErrTakeNameReserved):
		writeError(response, request, http.StatusConflict, "NAME_RESERVED", "take name has already been used", map[string]string{"field": "name", "value": takeName})
	case errors.Is(err, operatorlib.ErrTakeRecording):
		writeError(response, request, http.StatusConflict, "TAKE_RECORDING", "another take is recording", nil)
	case errors.Is(err, operatorlib.ErrNoAvailableCamera):
		writeError(response, request, http.StatusConflict, "NO_AVAILABLE_CAMERA", "no requested camera is connected", nil)
	case errors.Is(err, operatorlib.ErrIdempotencyConflict):
		writeError(response, request, http.StatusConflict, "STATE_CONFLICT", "idempotency key was already used for another request", nil)
	case errors.Is(err, operatorlib.ErrDependencyUnavailable):
		writeError(response, request, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "take mutation dependency is unavailable", nil)
	default:
		writeError(response, request, http.StatusInternalServerError, "INTERNAL", "take mutation failed", nil)
	}
}

func (s *Server) getSession(response http.ResponseWriter, request *http.Request, name string) {

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

func (s *Server) addCamera(response http.ResponseWriter, request *http.Request, sessionName string) {
	if s.cameras == nil {
		writeError(response, request, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "camera mutation is not configured", nil)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumRequestBodySize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var body cameraMutationRequest
	if err := decoder.Decode(&body); err != nil {
		writeError(response, request, http.StatusBadRequest, "INVALID_ARGUMENT", "request body must be a valid camera creation object", nil)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(response, request, http.StatusBadRequest, "INVALID_ARGUMENT", "request body must contain one JSON object", nil)
		return
	}
	result, err := s.cameras.Add(request.Context(), sessionName, body.Name, request.Header.Get("Idempotency-Key"))
	if err != nil {
		s.writeCameraError(response, request, err, body.Name)
		return
	}
	writeJSON(response, http.StatusAccepted, cameraMutationResponse{
		Camera:         cameraSummary{Name: result.Camera.Name, Phase: recordingv1alpha1.CameraPhaseProvisioning},
		ConnectionURLs: result.ConnectionURLs,
	})
}

func (s *Server) deleteCamera(response http.ResponseWriter, request *http.Request, sessionName, cameraName string) {
	if operatorvalidation.Name(cameraName) != nil {
		writeError(response, request, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid camera name", map[string]string{"field": "cameraName"})
		return
	}
	if s.cameras == nil {
		writeError(response, request, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "camera mutation is not configured", nil)
		return
	}
	camera, err := s.cameras.Delete(request.Context(), sessionName, cameraName, request.Header.Get("Idempotency-Key"))
	if err != nil {
		s.writeCameraError(response, request, err, cameraName)
		return
	}
	writeJSON(response, http.StatusAccepted, cameraMutationResponse{Camera: cameraSummary{Name: camera.Name, Phase: recordingv1alpha1.CameraPhaseDeleting}})
}

func (s *Server) writeCameraError(response http.ResponseWriter, request *http.Request, err error, cameraName string) {
	switch {
	case errors.Is(err, operatorlib.ErrInvalidName):
		writeError(response, request, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid camera or session name", nil)
	case errors.Is(err, operatorlib.ErrSessionNotFound), errors.Is(err, operatorlib.ErrCameraNotFound):
		writeError(response, request, http.StatusNotFound, "NOT_FOUND", "session or camera was not found", nil)
	case errors.Is(err, operatorlib.ErrCameraNameReserved):
		writeError(response, request, http.StatusConflict, "NAME_RESERVED", "camera name has already been used", map[string]string{"field": "name", "value": cameraName})
	case errors.Is(err, operatorlib.ErrTakeRecording):
		writeError(response, request, http.StatusConflict, "TAKE_RECORDING", "camera mutation is disabled while a take is recording", nil)
	case errors.Is(err, operatorlib.ErrIdempotencyConflict):
		writeError(response, request, http.StatusConflict, "STATE_CONFLICT", "idempotency key was already used for another request", nil)
	case errors.Is(err, operatorlib.ErrMediaPortsExhausted):
		writeError(response, request, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "media node ports are exhausted", nil)
	case errors.Is(err, operatorlib.ErrDependencyUnavailable):
		writeError(response, request, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "camera mutation dependency is unavailable", nil)
	default:
		writeError(response, request, http.StatusInternalServerError, "INTERNAL", "camera mutation failed", nil)
	}
}

type sessionResource struct {
	Name   string                          `json:"name"`
	Spec   recordingv1alpha1.SessionSpec   `json:"spec"`
	Status recordingv1alpha1.SessionStatus `json:"status"`
}

type createSessionRequest struct {
	Name string `json:"name"`
}

type cameraMutationRequest struct {
	Name string `json:"name"`
}
type takeMutationRequest struct {
	Name        string   `json:"name"`
	CameraNames []string `json:"cameraNames,omitempty"`
}
type takeSummary struct {
	Name        string                      `json:"name"`
	Phase       recordingv1alpha1.TakePhase `json:"phase"`
	CameraNames []string                    `json:"cameraNames"`
}
type excludedCameraResponse struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}
type takeMutationResponse struct {
	Take            takeSummary              `json:"take"`
	ExcludedCameras []excludedCameraResponse `json:"excludedCameras,omitempty"`
}
type cameraSummary struct {
	Name  string                        `json:"name"`
	Phase recordingv1alpha1.CameraPhase `json:"phase"`
}
type cameraMutationResponse struct {
	Camera         cameraSummary                     `json:"camera"`
	ConnectionURLs recordingv1alpha1.CameraEndpoints `json:"connectionUrls,omitempty"`
}

type previewTokenResponse struct {
	ServerURL        string    `json:"serverUrl"`
	RoomName         string    `json:"roomName"`
	ParticipantToken string    `json:"participantToken"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

type sessionCreationService interface {
	Create(context.Context, string, string) (*recordingv1alpha1.Session, error)
}

type cameraMutationService interface {
	Add(context.Context, string, string, string) (*operatorlib.CameraMutationResult, error)
	Delete(context.Context, string, string, string) (*recordingv1alpha1.CameraSpec, error)
}

type previewTokenService interface {
	Issue() (*livekitapi.PreviewToken, error)
}

type takeMutationService interface {
	Start(context.Context, string, string, []string, string) (*operatorlib.TakeMutationResult, error)
	Stop(context.Context, string, string, string) (*recordingv1alpha1.TakeSpec, error)
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
