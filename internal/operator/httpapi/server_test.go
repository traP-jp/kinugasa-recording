package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	livekitapi "github.com/comavius/kinugasa-recording/internal/livekit"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	operatorlib "github.com/comavius/kinugasa-recording/internal/operator"
)

func TestServerListsAndGetsSessionsByUserName(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add recording API to scheme: %v", err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&recordingv1alpha1.Session{
			ObjectMeta: metav1.ObjectMeta{Name: "session-b", Namespace: "recording"},
			Spec:       recordingv1alpha1.SessionSpec{Name: "B"},
		},
		&recordingv1alpha1.Session{
			ObjectMeta: metav1.ObjectMeta{Name: "session-a", Namespace: "recording"},
			Spec:       recordingv1alpha1.SessionSpec{Name: "A"},
		},
	).Build()
	server := NewServer(reader, "recording")

	listResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil))
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d: %s", listResponse.Code, http.StatusOK, listResponse.Body.String())
	}
	var listed sessionsResponse
	if err := json.NewDecoder(listResponse.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed.Sessions) != 2 || listed.Sessions[0].Name != "A" || listed.Sessions[1].Name != "B" {
		t.Fatalf("sessions = %#v, want A then B", listed.Sessions)
	}

	getResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/B", nil))
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d: %s", getResponse.Code, http.StatusOK, getResponse.Body.String())
	}
	var got sessionResponse
	if err := json.NewDecoder(getResponse.Body).Decode(&got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if got.Session.Name != "B" {
		t.Fatalf("session name = %q, want B", got.Session.Name)
	}
}

func TestServerUsesCommonErrorResponse(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add recording API to scheme: %v", err)
	}
	server := NewServer(fake.NewClientBuilder().WithScheme(scheme).Build(), "recording")

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/not_valid", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	var body errorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Error.Code != "INVALID_ARGUMENT" || body.Error.RequestID == "" {
		t.Fatalf("error = %#v, want INVALID_ARGUMENT with request ID", body.Error)
	}
}

func TestServerCreatesSession(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add recording API to scheme: %v", err)
	}
	creator := &sessionCreatorStub{session: &recordingv1alpha1.Session{Spec: recordingv1alpha1.SessionSpec{Name: "Session-1"}}}
	server := NewServer(fake.NewClientBuilder().WithScheme(scheme).Build(), "recording", creator)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewBufferString(`{"name":"Session-1"}`))

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if creator.name != "Session-1" {
		t.Fatalf("created name = %q, want Session-1", creator.name)
	}
}

func TestServerReportsReservedSessionName(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add recording API to scheme: %v", err)
	}
	creator := &sessionCreatorStub{err: operatorlib.ErrSessionNameReserved}
	server := NewServer(fake.NewClientBuilder().WithScheme(scheme).Build(), "recording", creator)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewBufferString(`{"name":"Session-1"}`))

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusConflict, response.Body.String())
	}
	var body errorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "NAME_RESERVED" {
		t.Fatalf("error code = %q, want NAME_RESERVED", body.Error.Code)
	}
}

func TestServerAddsAndDeletesCamera(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cameras := &cameraServiceStub{addResult: &operatorlib.CameraMutationResult{
		Camera:         recordingv1alpha1.CameraSpec{Name: "front"},
		ConnectionURLs: recordingv1alpha1.CameraEndpoints{RIST: "rist://host:31000?rist_profile=main", SRT: "srt://host:31001?mode=caller&transtype=live"},
	}}
	server := NewServer(fake.NewClientBuilder().WithScheme(scheme).Build(), "recording").WithCameraService(cameras)
	addResponse := httptest.NewRecorder()
	addRequest := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/Session-1/cameras", bytes.NewBufferString(`{"name":"front"}`))
	addRequest.Header.Set("Idempotency-Key", "add-front")
	server.Handler().ServeHTTP(addResponse, addRequest)
	if addResponse.Code != http.StatusAccepted {
		t.Fatalf("add status = %d: %s", addResponse.Code, addResponse.Body.String())
	}
	var added cameraMutationResponse
	if err := json.NewDecoder(addResponse.Body).Decode(&added); err != nil {
		t.Fatal(err)
	}
	if added.Camera.Phase != recordingv1alpha1.CameraPhaseProvisioning || added.ConnectionURLs.RIST == "" {
		t.Fatalf("added = %#v", added)
	}
	if cameras.sessionName != "Session-1" || cameras.cameraName != "front" || cameras.key != "add-front" {
		t.Fatalf("add arguments = %#v", cameras)
	}

	cameras.deleteResult = &recordingv1alpha1.CameraSpec{Name: "front", DesiredState: recordingv1alpha1.DesiredStateAbsent}
	deleteResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(deleteResponse, httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/Session-1/cameras/front", nil))
	if deleteResponse.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d: %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	var deleted cameraMutationResponse
	if err := json.NewDecoder(deleteResponse.Body).Decode(&deleted); err != nil {
		t.Fatal(err)
	}
	if deleted.Camera.Phase != recordingv1alpha1.CameraPhaseDeleting {
		t.Fatalf("deleted = %#v", deleted)
	}
}

func TestServerMapsCameraMutationConflicts(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	server := NewServer(fake.NewClientBuilder().WithScheme(scheme).Build(), "recording").WithCameraService(&cameraServiceStub{err: operatorlib.ErrTakeRecording})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/sessions/Session-1/cameras", bytes.NewBufferString(`{"name":"front"}`)))
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var body errorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "TAKE_RECORDING" {
		t.Fatalf("error = %#v", body.Error)
	}
}

func TestServerIssuesPreviewToken(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	expires := time.Date(2026, 7, 14, 1, 5, 0, 0, time.UTC)
	server := NewServer(fake.NewClientBuilder().WithScheme(scheme).Build(), "recording").WithPreviewTokenService(&tokenServiceStub{token: &livekitapi.PreviewToken{
		ServerURL: "wss://livekit.example", RoomName: "kinugasa-preview", ParticipantToken: "signed-token", ExpiresAt: expires,
	}})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/livekit/token", bytes.NewBufferString(`{}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var body previewTokenResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ParticipantToken != "signed-token" || body.ExpiresAt != expires {
		t.Fatalf("response = %#v", body)
	}
}

func TestServerStartsAndStopsTake(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	takes := &takeServiceStub{startResult: &operatorlib.TakeMutationResult{
		Take:            recordingv1alpha1.TakeSpec{Name: "take-1", CameraNames: []string{"front"}},
		ExcludedCameras: []operatorlib.ExcludedCamera{{Name: "side", Reason: "CAMERA_DISCONNECTED"}},
	}}
	server := NewServer(fake.NewClientBuilder().WithScheme(scheme).Build(), "recording").WithTakeService(takes)
	startResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(startResponse, httptest.NewRequest(http.MethodPost, "/api/v1/sessions/Session-1/takes", bytes.NewBufferString(`{"name":"take-1","cameraNames":["front","side"]}`)))
	if startResponse.Code != http.StatusAccepted {
		t.Fatalf("start status = %d: %s", startResponse.Code, startResponse.Body.String())
	}
	var started takeMutationResponse
	if err := json.NewDecoder(startResponse.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	if started.Take.Phase != recordingv1alpha1.TakePhasePending || len(started.ExcludedCameras) != 1 {
		t.Fatalf("started = %#v", started)
	}

	takes.stopResult = &recordingv1alpha1.TakeSpec{Name: "take-1", CameraNames: []string{"front"}}
	stopResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(stopResponse, httptest.NewRequest(http.MethodPost, "/api/v1/sessions/Session-1/takes/take-1/stop", bytes.NewBufferString(`{}`)))
	if stopResponse.Code != http.StatusAccepted {
		t.Fatalf("stop status = %d: %s", stopResponse.Code, stopResponse.Body.String())
	}
}

type sessionCreatorStub struct {
	name    string
	session *recordingv1alpha1.Session
	err     error
}

type cameraServiceStub struct {
	sessionName, cameraName, key string
	addResult                    *operatorlib.CameraMutationResult
	deleteResult                 *recordingv1alpha1.CameraSpec
	err                          error
}

type tokenServiceStub struct {
	token *livekitapi.PreviewToken
	err   error
}

type takeServiceStub struct {
	startResult *operatorlib.TakeMutationResult
	stopResult  *recordingv1alpha1.TakeSpec
	err         error
}

func (stub *takeServiceStub) Start(context.Context, string, string, []string, string) (*operatorlib.TakeMutationResult, error) {
	return stub.startResult, stub.err
}
func (stub *takeServiceStub) Stop(context.Context, string, string, string) (*recordingv1alpha1.TakeSpec, error) {
	return stub.stopResult, stub.err
}

func (stub *tokenServiceStub) Issue() (*livekitapi.PreviewToken, error) { return stub.token, stub.err }

func (stub *cameraServiceStub) Add(_ context.Context, sessionName, cameraName, key string) (*operatorlib.CameraMutationResult, error) {
	stub.sessionName, stub.cameraName, stub.key = sessionName, cameraName, key
	return stub.addResult, stub.err
}

func (stub *cameraServiceStub) Delete(_ context.Context, sessionName, cameraName, key string) (*recordingv1alpha1.CameraSpec, error) {
	stub.sessionName, stub.cameraName, stub.key = sessionName, cameraName, key
	return stub.deleteResult, stub.err
}

func (stub *sessionCreatorStub) Create(_ context.Context, name, _ string) (*recordingv1alpha1.Session, error) {
	stub.name = name
	if stub.err != nil {
		return nil, stub.err
	}
	if stub.session == nil {
		return nil, errors.New("session is not configured")
	}

	return stub.session, nil
}
