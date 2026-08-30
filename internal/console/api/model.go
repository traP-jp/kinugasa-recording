package api

import (
	"time"

	"github.com/traP-jp/kinugasa-recording/internal/console/application"
	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

type errorResponse struct {
	Error string `json:"error"`
}

type createResourceRequest struct {
	Name string `json:"name"`
}

type sessionResponse struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	State     domain.SessionState `json:"state"`
	CreatedAt time.Time           `json:"createdAt"`
}

func newSessionResponse(session domain.Session) sessionResponse {
	return sessionResponse{
		ID:        string(session.ID),
		Name:      session.Name,
		State:     session.State,
		CreatedAt: session.CreatedAt,
	}
}

type paginationResponse struct {
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
	Total    int64 `json:"total"`
}

type sessionPageResponse struct {
	Items      []sessionResponse  `json:"items"`
	Pagination paginationResponse `json:"pagination"`
}

func newSessionPageResponse(page application.SessionPage) sessionPageResponse {
	items := make([]sessionResponse, len(page.Items))
	for index, session := range page.Items {
		items[index] = newSessionResponse(session)
	}
	return sessionPageResponse{
		Items: items,
		Pagination: paginationResponse{
			Page:     page.Page,
			PageSize: page.PageSize,
			Total:    page.Total,
		},
	}
}

type sessionDetailResponse struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	State           domain.SessionState `json:"state"`
	OngoingTakeName *string             `json:"ongoingTakeName"`
	CreatedAt       time.Time           `json:"createdAt"`
}

func newSessionDetailResponse(detail repository.SessionDetail) sessionDetailResponse {
	var ongoingTakeName *string
	if detail.OngoingTakeName != "" {
		ongoingTakeName = &detail.OngoingTakeName
	}
	return sessionDetailResponse{
		ID:              string(detail.Session.ID),
		Name:            detail.Session.Name,
		State:           detail.Session.State,
		OngoingTakeName: ongoingTakeName,
		CreatedAt:       detail.Session.CreatedAt,
	}
}

type cameraConnectionResponse struct {
	Name   string                        `json:"name"`
	URL    *string                       `json:"url"`
	Status domain.CameraConnectionStatus `json:"status"`
	Error  *string                       `json:"error"`
}

func newCameraConnectionResponse(camera repository.Camera) cameraConnectionResponse {
	var cameraURL *string
	if camera.Connection.URL != "" {
		cameraURL = &camera.Connection.URL
	}
	var cameraError *string
	if camera.Connection.Error != "" {
		cameraError = &camera.Connection.Error
	}
	return cameraConnectionResponse{
		Name:   camera.Identity.Name,
		URL:    cameraURL,
		Status: camera.Connection.Status,
		Error:  cameraError,
	}
}

type startTakeRequest struct {
	Name        string   `json:"name"`
	CameraNames []string `json:"cameraNames"`
}

type recordingCameraResponse struct {
	Name      string                      `json:"name"`
	State     domain.RecordingCameraState `json:"state"`
	StartedAt time.Time                   `json:"startedAt"`
	Error     *string                     `json:"error"`
}

type ongoingTakeResponse struct {
	ID        string                    `json:"id"`
	Name      string                    `json:"name"`
	StartedAt time.Time                 `json:"startedAt"`
	Cameras   []recordingCameraResponse `json:"cameras"`
}

func newOngoingTakeResponse(view application.OngoingTakeView) ongoingTakeResponse {
	cameras := make([]recordingCameraResponse, len(view.Take.Cameras))
	for index, camera := range view.Take.Cameras {
		var cameraError *string
		if camera.Error != "" {
			cameraError = &camera.Error
		}
		cameras[index] = recordingCameraResponse{
			Name: view.CameraNames[camera.CameraIdentityID], State: camera.State,
			StartedAt: camera.StartedAt, Error: cameraError,
		}
	}
	return ongoingTakeResponse{ID: string(view.Take.ID), Name: view.Take.Name, StartedAt: view.Take.StartedAt, Cameras: cameras}
}

type ongoingTakeResultResponse struct {
	Type        string               `json:"type"`
	OngoingTake *ongoingTakeResponse `json:"ongoingTake,omitempty"`
}

type finishedTakeResponse struct {
	ID         string                   `json:"id"`
	Name       string                   `json:"name"`
	State      domain.FinishedTakeState `json:"state"`
	StartedAt  time.Time                `json:"startedAt"`
	FinishedAt time.Time                `json:"finishedAt"`
	Error      *string                  `json:"error"`
}

func newFinishedTakeResponse(take domain.FinishedTake) finishedTakeResponse {
	var takeError *string
	if take.Error != "" {
		takeError = &take.Error
	}
	return finishedTakeResponse{
		ID: string(take.ID), Name: take.Name, State: take.State,
		StartedAt: take.StartedAt, FinishedAt: take.FinishedAt, Error: takeError,
	}
}
