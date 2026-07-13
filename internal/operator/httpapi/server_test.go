package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
