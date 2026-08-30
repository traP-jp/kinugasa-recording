package domain

import (
	"net/url"
	"time"
)

type CameraConnectionStatus string

const (
	CameraConnectionStatusActivating CameraConnectionStatus = "activating"
	CameraConnectionStatusWaiting    CameraConnectionStatus = "waiting"
	CameraConnectionStatusConnected  CameraConnectionStatus = "connected"
	CameraConnectionStatusError      CameraConnectionStatus = "error"
)

type CameraIdentity struct {
	ID        CameraIdentityID
	SessionID SessionID
	Name      string
	CreatedAt time.Time
}

func (c CameraIdentity) Validate() error {
	if err := validateID("id", string(c.ID)); err != nil {
		return err
	}
	if err := validateID("sessionId", string(c.SessionID)); err != nil {
		return err
	}
	if err := validateName("name", c.Name); err != nil {
		return err
	}
	return validateTime("createdAt", c.CreatedAt)
}

type CameraConnection struct {
	CameraIdentityID CameraIdentityID
	URL              string
	Status           CameraConnectionStatus
	Error            string
	VideoWorkerID    VideoWorkerID
}

func (c CameraConnection) Validate() error {
	if err := validateID("cameraIdentityId", string(c.CameraIdentityID)); err != nil {
		return err
	}
	switch c.Status {
	case CameraConnectionStatusActivating, CameraConnectionStatusWaiting,
		CameraConnectionStatusConnected, CameraConnectionStatusError:
	default:
		return invalid("status", "must be activating, waiting, connected, or error")
	}

	if c.Status != CameraConnectionStatusActivating && c.URL == "" {
		return invalid("url", "must be set unless status is activating")
	}
	if c.URL != "" {
		parsed, err := url.Parse(c.URL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return invalid("url", "must be an absolute URL")
		}
	}
	if c.Status == CameraConnectionStatusError {
		if err := validateErrorReason("error", c.Error); err != nil {
			return err
		}
	} else if c.Error != "" {
		return invalid("error", "must only be set when status is error")
	}
	if c.VideoWorkerID != "" {
		return validateID("videoWorkerId", string(c.VideoWorkerID))
	}
	return nil
}
