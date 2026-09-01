package postgres

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/traP-jp/kinugasa-recording/internal/console/api"
	"github.com/traP-jp/kinugasa-recording/internal/console/application"
)

func TestConsoleAPIWithPostgres(t *testing.T) {
	store := New(resetDatabase(t))
	handler := api.NewHandler(
		application.New(store),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	session := performJSONRequest(t, handler, http.MethodPost, "/api/sessions", `{"name":"session-1"}`)
	if session.Code != http.StatusCreated {
		t.Fatalf("create session status = %d, want 201; body = %s", session.Code, session.Body.String())
	}

	camera := performJSONRequest(t, handler, http.MethodPost, "/api/sessions/session-1/cameras", `{"name":"camera-1"}`)
	if camera.Code != http.StatusCreated {
		t.Fatalf("create camera status = %d, want 201; body = %s", camera.Code, camera.Body.String())
	}
	var cameraBody struct {
		Name   string  `json:"name"`
		URL    *string `json:"url"`
		Status string  `json:"status"`
	}
	if err := json.NewDecoder(camera.Body).Decode(&cameraBody); err != nil {
		t.Fatalf("decode camera response: %v", err)
	}
	if cameraBody.Name != "camera-1" || cameraBody.URL != nil || cameraBody.Status != "activating" {
		t.Fatalf("camera response = %+v", cameraBody)
	}

	list := performJSONRequest(t, handler, http.MethodGet, "/api/sessions/session-1/cameras", "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"name":"camera-1"`) {
		t.Fatalf("list cameras status = %d, body = %s", list.Code, list.Body.String())
	}

	duplicate := performJSONRequest(t, handler, http.MethodPost, "/api/sessions/session-1/cameras", `{"name":"camera-1"}`)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate camera status = %d, want 409; body = %s", duplicate.Code, duplicate.Body.String())
	}
}

func performJSONRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
