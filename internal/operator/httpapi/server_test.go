package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
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

type sessionCreatorStub struct {
	name    string
	session *recordingv1alpha1.Session
	err     error
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
