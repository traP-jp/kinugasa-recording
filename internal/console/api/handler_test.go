package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/traP-jp/kinugasa-recording/internal/console/application"
	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

func TestCreateSessionEndpoint(t *testing.T) {
	createdAt := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	service := &serviceStub{
		createSession: func(_ context.Context, name string) (domain.Session, error) {
			if name != "studio-a" {
				t.Fatalf("CreateSession name = %q", name)
			}
			return domain.Session{
				ID:        "019c240d-a6de-7de0-a826-0f26e8803fc0",
				Name:      name,
				State:     domain.SessionStateActive,
				CreatedAt: createdAt,
			}, nil
		},
	}
	response := request(t, NewHandler(service, discardLogger()), http.MethodPost, "/api/sessions", `{"name":"studio-a"}`)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", response.Code, response.Body.String())
	}
	var body sessionResponse
	decodeResponse(t, response, &body)
	if body.Name != "studio-a" || body.State != domain.SessionStateActive || !body.CreatedAt.Equal(createdAt) {
		t.Fatalf("body = %+v", body)
	}
}

func TestCreateSessionRejectsUnknownFields(t *testing.T) {
	response := request(t, NewHandler(&serviceStub{}, discardLogger()), http.MethodPost, "/api/sessions", `{"name":"studio-a","extra":true}`)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", response.Code, response.Body.String())
	}
}

func TestListSessionsEndpoint(t *testing.T) {
	service := &serviceStub{
		listSessions: func(_ context.Context, page application.PageRequest) (application.SessionPage, error) {
			if page.Page != 2 || page.PageSize != 5 {
				t.Fatalf("page = %+v, want page 2 size 5", page)
			}
			return application.SessionPage{
				Items:    []domain.Session{},
				Page:     page.Page,
				PageSize: page.PageSize,
				Total:    6,
			}, nil
		},
	}
	response := request(t, NewHandler(service, discardLogger()), http.MethodGet, "/api/sessions?page=2&pageSize=5", "")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	var body sessionPageResponse
	decodeResponse(t, response, &body)
	if body.Items == nil || body.Pagination.Total != 6 {
		t.Fatalf("body = %+v", body)
	}
}

func TestCameraEndpointsUseNullableFields(t *testing.T) {
	camera := repository.Camera{
		Identity: domain.CameraIdentity{Name: "camera-1"},
		Connection: domain.CameraConnection{
			Status: domain.CameraConnectionStatusActivating,
		},
	}
	service := &serviceStub{
		createCamera: func(_ context.Context, sessionName, cameraName string) (repository.Camera, error) {
			if sessionName != "session-1" || cameraName != "camera-1" {
				t.Fatalf("CreateCamera(%q, %q)", sessionName, cameraName)
			}
			return camera, nil
		},
		getCamera: func(context.Context, string, string) (repository.Camera, error) {
			return camera, nil
		},
	}
	handler := NewHandler(service, discardLogger())

	for _, test := range []struct {
		method string
		path   string
		body   string
		status int
	}{
		{method: http.MethodPost, path: "/api/sessions/session-1/cameras", body: `{"name":"camera-1"}`, status: http.StatusCreated},
		{method: http.MethodGet, path: "/api/sessions/session-1/cameras/camera-1/connection", status: http.StatusOK},
	} {
		response := request(t, handler, test.method, test.path, test.body)
		if response.Code != test.status {
			t.Fatalf("%s %s status = %d, want %d", test.method, test.path, response.Code, test.status)
		}
		var body map[string]any
		decodeResponse(t, response, &body)
		if body["url"] != nil || body["error"] != nil || body["status"] != "activating" {
			t.Fatalf("camera body = %#v", body)
		}
	}
}

func TestRepositoryErrorsMapToContractStatuses(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{name: "not found", err: repository.ErrNotFound, status: http.StatusNotFound},
		{name: "conflict", err: repository.ErrConflict, status: http.StatusConflict},
		{name: "internal", err: errors.New("database unavailable"), status: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &serviceStub{
				getSession: func(context.Context, string) (repository.SessionDetail, error) {
					return repository.SessionDetail{}, test.err
				},
			}
			response := request(t, NewHandler(service, discardLogger()), http.MethodGet, "/api/sessions/missing", "")
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestTakeEndpoints(t *testing.T) {
	now := time.Date(2026, 8, 31, 13, 0, 0, 0, time.UTC)
	view := application.OngoingTakeView{
		Take: domain.OngoingTake{
			ID: "019c2949-b039-739f-953b-3e4236cf44c5", Name: "take-1", StartedAt: now,
			Cameras: []domain.RecordingCamera{{
				CameraIdentityID: "019c2949-b9c5-748a-8477-0a0af012b26c",
				State:            domain.RecordingCameraStateRecording, StartedAt: now,
			}},
		},
		CameraNames: map[domain.CameraIdentityID]string{"019c2949-b9c5-748a-8477-0a0af012b26c": "camera-1"},
	}
	service := &serviceStub{
		startTake: func(_ context.Context, sessionName, takeName string, cameraNames []string) (application.OngoingTakeView, error) {
			if sessionName != "session-1" || takeName != "take-1" || len(cameraNames) != 1 {
				t.Fatalf("StartTake(%q, %q, %v)", sessionName, takeName, cameraNames)
			}
			return view, nil
		},
		getOngoingTake: func(context.Context, string) (*application.OngoingTakeView, error) { return &view, nil },
		finishTake: func(context.Context, string) (domain.FinishedTake, error) {
			return domain.FinishedTake{ID: view.Take.ID, Name: view.Take.Name, State: domain.FinishedTakeStateUploading,
				StartedAt: now, FinishedAt: now.Add(time.Minute)}, nil
		},
		listFinishedTakes: func(context.Context, string, application.PageRequest) (application.FinishedTakePage, error) {
			return application.FinishedTakePage{Items: []domain.FinishedTake{{
				ID: view.Take.ID, Name: view.Take.Name, State: domain.FinishedTakeStateCompleted,
				StartedAt: now, FinishedAt: now.Add(time.Minute),
			}}, Page: 1, PageSize: 20, Total: 1}, nil
		},
		getFinishedTake: func(context.Context, string, string) (repository.FinishedTakeDetail, error) {
			hash := domain.ContentHash{}
			size := int64(42)
			return repository.FinishedTakeDetail{
				Take: domain.FinishedTake{
					ID: view.Take.ID, Name: view.Take.Name, State: domain.FinishedTakeStateCompleted,
					StartedAt: now, FinishedAt: now.Add(time.Minute),
					VideoFiles: []domain.VideoFile{{
						FinishedTakeID: view.Take.ID, CameraIdentityID: "019c2949-b9c5-748a-8477-0a0af012b26c",
						State: domain.VideoFileStateCompleted, StartedAt: now, FinishedAt: now.Add(time.Minute),
						ObjectKey: "objects/video.mp4", Hash: &hash, Size: &size,
					}},
				},
				CameraNames: map[domain.CameraIdentityID]string{"019c2949-b9c5-748a-8477-0a0af012b26c": "camera-1"},
			}, nil
		},
	}
	handler := NewHandler(service, discardLogger())
	startResponse := request(t, handler, http.MethodPost, "/api/sessions/session-1/ongoing-take/start",
		`{"name":"take-1","cameraNames":["camera-1"]}`)
	if startResponse.Code != http.StatusCreated {
		t.Fatalf("start status = %d, body = %s", startResponse.Code, startResponse.Body.String())
	}
	getResponse := request(t, handler, http.MethodGet, "/api/sessions/session-1/ongoing-take", "")
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"type":"present"`) {
		t.Fatalf("get ongoing response = %d %s", getResponse.Code, getResponse.Body.String())
	}
	finishResponse := request(t, handler, http.MethodPost, "/api/sessions/session-1/ongoing-take/finish", "")
	if finishResponse.Code != http.StatusAccepted || !strings.Contains(finishResponse.Body.String(), `"state":"uploading"`) {
		t.Fatalf("finish response = %d %s", finishResponse.Code, finishResponse.Body.String())
	}
	listResponse := request(t, handler, http.MethodGet, "/api/sessions/session-1/takes", "")
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"total":1`) {
		t.Fatalf("list takes response = %d %s", listResponse.Code, listResponse.Body.String())
	}
	detailResponse := request(t, handler, http.MethodGet, "/api/sessions/session-1/takes/take-1", "")
	if detailResponse.Code != http.StatusOK || !strings.Contains(detailResponse.Body.String(), `"cameraName":"camera-1"`) ||
		!strings.Contains(detailResponse.Body.String(), `"hash":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="`) {
		t.Fatalf("get take response = %d %s", detailResponse.Code, detailResponse.Body.String())
	}
}

func request(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type serviceStub struct {
	createSession     func(context.Context, string) (domain.Session, error)
	listSessions      func(context.Context, application.PageRequest) (application.SessionPage, error)
	getSession        func(context.Context, string) (repository.SessionDetail, error)
	createCamera      func(context.Context, string, string) (repository.Camera, error)
	listCameras       func(context.Context, string) ([]repository.Camera, error)
	getCamera         func(context.Context, string, string) (repository.Camera, error)
	deleteCamera      func(context.Context, string, string) error
	startTake         func(context.Context, string, string, []string) (application.OngoingTakeView, error)
	getOngoingTake    func(context.Context, string) (*application.OngoingTakeView, error)
	finishTake        func(context.Context, string) (domain.FinishedTake, error)
	listFinishedTakes func(context.Context, string, application.PageRequest) (application.FinishedTakePage, error)
	getFinishedTake   func(context.Context, string, string) (repository.FinishedTakeDetail, error)
}

func (s *serviceStub) CreateSession(ctx context.Context, name string) (domain.Session, error) {
	return s.createSession(ctx, name)
}

func (s *serviceStub) ListSessions(ctx context.Context, page application.PageRequest) (application.SessionPage, error) {
	return s.listSessions(ctx, page)
}

func (s *serviceStub) GetSession(ctx context.Context, name string) (repository.SessionDetail, error) {
	return s.getSession(ctx, name)
}

func (s *serviceStub) CreateCamera(ctx context.Context, sessionName, cameraName string) (repository.Camera, error) {
	return s.createCamera(ctx, sessionName, cameraName)
}

func (s *serviceStub) ListCameras(ctx context.Context, sessionName string) ([]repository.Camera, error) {
	return s.listCameras(ctx, sessionName)
}

func (s *serviceStub) GetCamera(ctx context.Context, sessionName, cameraName string) (repository.Camera, error) {
	return s.getCamera(ctx, sessionName, cameraName)
}

func (s *serviceStub) DeleteCamera(ctx context.Context, sessionName, cameraName string) error {
	return s.deleteCamera(ctx, sessionName, cameraName)
}

func (s *serviceStub) StartTake(ctx context.Context, sessionName, takeName string, cameraNames []string) (application.OngoingTakeView, error) {
	return s.startTake(ctx, sessionName, takeName, cameraNames)
}

func (s *serviceStub) GetOngoingTake(ctx context.Context, sessionName string) (*application.OngoingTakeView, error) {
	return s.getOngoingTake(ctx, sessionName)
}

func (s *serviceStub) FinishTake(ctx context.Context, sessionName string) (domain.FinishedTake, error) {
	return s.finishTake(ctx, sessionName)
}

func (s *serviceStub) ListFinishedTakes(ctx context.Context, sessionName string, request application.PageRequest) (application.FinishedTakePage, error) {
	return s.listFinishedTakes(ctx, sessionName, request)
}

func (s *serviceStub) GetFinishedTake(ctx context.Context, sessionName, takeName string) (repository.FinishedTakeDetail, error) {
	return s.getFinishedTake(ctx, sessionName, takeName)
}
