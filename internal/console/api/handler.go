package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/traP-jp/kinugasa-recording/internal/console/application"
	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

type Service interface {
	CreateSession(context.Context, string) (domain.Session, error)
	ListSessions(context.Context, application.PageRequest) (application.SessionPage, error)
	GetSession(context.Context, string) (repository.SessionDetail, error)
	CreateCamera(context.Context, string, string) (repository.Camera, error)
	ListCameras(context.Context, string) ([]repository.Camera, error)
	GetCamera(context.Context, string, string) (repository.Camera, error)
	DeleteCamera(context.Context, string, string, bool) error
	StartTake(context.Context, string, string, []string) (application.OngoingTakeView, error)
	GetOngoingTake(context.Context, string) (*application.OngoingTakeView, error)
	FinishTake(context.Context, string) (domain.FinishedTake, error)
	ListFinishedTakes(context.Context, string, application.PageRequest) (application.FinishedTakePage, error)
	GetFinishedTake(context.Context, string, string) (repository.FinishedTakeDetail, error)
	GetLockfile(context.Context, string) (application.Lockfile, error)
	CreatePreviewAccess(context.Context, string) (application.PreviewAccess, error)
}

type Handler struct {
	service Service
	logger  *slog.Logger
}

func NewHandler(service Service, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	handler := &Handler{service: service, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handler.health)
	mux.HandleFunc("GET /api/sessions", handler.listSessions)
	mux.HandleFunc("POST /api/sessions", handler.createSession)
	mux.HandleFunc("GET /api/sessions/{sessionName}", handler.getSession)
	mux.HandleFunc("GET /api/sessions/{sessionName}/cameras", handler.listCameras)
	mux.HandleFunc("POST /api/sessions/{sessionName}/cameras", handler.createCamera)
	mux.HandleFunc("DELETE /api/sessions/{sessionName}/cameras/{cameraName}", handler.deleteCamera)
	mux.HandleFunc("GET /api/sessions/{sessionName}/cameras/{cameraName}/connection", handler.getCameraConnection)
	mux.HandleFunc("GET /api/sessions/{sessionName}/ongoing-take", handler.getOngoingTake)
	mux.HandleFunc("POST /api/sessions/{sessionName}/ongoing-take/start", handler.startTake)
	mux.HandleFunc("POST /api/sessions/{sessionName}/ongoing-take/finish", handler.finishTake)
	mux.HandleFunc("GET /api/sessions/{sessionName}/takes", handler.listFinishedTakes)
	mux.HandleFunc("GET /api/sessions/{sessionName}/takes/{takeName}", handler.getFinishedTake)
	mux.HandleFunc("GET /api/sessions/{sessionName}/lockfile", handler.getLockfile)
	mux.HandleFunc("POST /api/sessions/{sessionName}/preview-access", handler.createPreviewAccess)
	return mux
}

func (h *Handler) health(response http.ResponseWriter, _ *http.Request) {
	response.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"
	switch {
	case errors.Is(err, application.ErrInvalidArgument):
		status = http.StatusBadRequest
		message = err.Error()
	case errors.Is(err, repository.ErrNotFound):
		status = http.StatusNotFound
		message = "resource not found"
	case errors.Is(err, repository.ErrConflict):
		status = http.StatusConflict
		message = "resource conflicts with the current state"
	default:
		h.logger.ErrorContext(request.Context(), "console API request failed",
			"method", request.Method,
			"path", request.URL.Path,
			"error", err,
		)
	}
	writeJSON(response, status, errorResponse{Error: message})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func decodeJSON(response http.ResponseWriter, request *http.Request, destination any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("decode JSON: request body must contain one object")
	}
	return nil
}

func pagination(request *http.Request) (application.PageRequest, error) {
	page, err := positiveQueryInteger(request, "page", 1)
	if err != nil {
		return application.PageRequest{}, err
	}
	pageSize, err := positiveQueryInteger(request, "pageSize", 20)
	if err != nil {
		return application.PageRequest{}, err
	}
	return application.PageRequest{Page: page, PageSize: pageSize}, nil
}

func positiveQueryInteger(request *http.Request, name string, defaultValue int) (int, error) {
	value := request.URL.Query().Get(name)
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be an integer", application.ErrInvalidArgument, name)
	}
	return parsed, nil
}

func optionalBooleanQuery(request *http.Request, name string) (bool, error) {
	value := request.URL.Query().Get(name)
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%w: %s must be true or false", application.ErrInvalidArgument, name)
	}
	return parsed, nil
}
