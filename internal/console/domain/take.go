package domain

import "time"

type RecordingCameraState string

const (
	RecordingCameraStateRecording RecordingCameraState = "recording"
	RecordingCameraStateErrored   RecordingCameraState = "errored"
)

type OngoingTake struct {
	ID        TakeID
	SessionID SessionID
	Name      string
	StartedAt time.Time
	Cameras   []RecordingCamera
}

func (t OngoingTake) Validate() error {
	if err := validateID("id", string(t.ID)); err != nil {
		return err
	}
	if err := validateID("sessionId", string(t.SessionID)); err != nil {
		return err
	}
	if err := validateName("name", t.Name); err != nil {
		return err
	}
	if err := validateTime("startedAt", t.StartedAt); err != nil {
		return err
	}
	if len(t.Cameras) == 0 {
		return invalid("cameras", "must contain at least one camera")
	}

	seen := make(map[CameraIdentityID]struct{}, len(t.Cameras))
	for _, camera := range t.Cameras {
		if err := camera.Validate(); err != nil {
			return err
		}
		if camera.OngoingTakeID != t.ID {
			return invalid("cameras.ongoingTakeId", "must match the ongoing take")
		}
		if _, exists := seen[camera.CameraIdentityID]; exists {
			return invalid("cameras.cameraIdentityId", "must be unique within the take")
		}
		seen[camera.CameraIdentityID] = struct{}{}
	}
	return nil
}

type RecordingCamera struct {
	OngoingTakeID    TakeID
	CameraIdentityID CameraIdentityID
	State            RecordingCameraState
	StartedAt        time.Time
	Error            string
}

func (c RecordingCamera) Validate() error {
	if err := validateID("ongoingTakeId", string(c.OngoingTakeID)); err != nil {
		return err
	}
	if err := validateID("cameraIdentityId", string(c.CameraIdentityID)); err != nil {
		return err
	}
	if c.State != RecordingCameraStateRecording && c.State != RecordingCameraStateErrored {
		return invalid("state", "must be recording or errored")
	}
	if err := validateTime("startedAt", c.StartedAt); err != nil {
		return err
	}
	if c.State == RecordingCameraStateErrored {
		return validateErrorReason("error", c.Error)
	}
	if c.Error != "" {
		return invalid("error", "must only be set when state is errored")
	}
	return nil
}

type FinishedTakeState string

const (
	FinishedTakeStateUploading FinishedTakeState = "uploading"
	FinishedTakeStateCompleted FinishedTakeState = "completed"
	FinishedTakeStateErrored   FinishedTakeState = "errored"
)

type FinishedTake struct {
	ID         TakeID
	SessionID  SessionID
	Name       string
	State      FinishedTakeState
	StartedAt  time.Time
	FinishedAt time.Time
	Error      string
	VideoFiles []VideoFile
}

func (t FinishedTake) Validate() error {
	if err := validateID("id", string(t.ID)); err != nil {
		return err
	}
	if err := validateID("sessionId", string(t.SessionID)); err != nil {
		return err
	}
	if err := validateName("name", t.Name); err != nil {
		return err
	}
	if err := validateTime("startedAt", t.StartedAt); err != nil {
		return err
	}
	if err := validateTime("finishedAt", t.FinishedAt); err != nil {
		return err
	}
	if t.FinishedAt.Before(t.StartedAt) {
		return invalid("finishedAt", "must not precede startedAt")
	}
	if t.State != FinishedTakeStateUploading && t.State != FinishedTakeStateCompleted && t.State != FinishedTakeStateErrored {
		return invalid("state", "must be uploading, completed, or errored")
	}
	if t.State == FinishedTakeStateErrored {
		if err := validateErrorReason("error", t.Error); err != nil {
			return err
		}
	} else if t.Error != "" {
		return invalid("error", "must only be set when state is errored")
	}

	seen := make(map[CameraIdentityID]struct{}, len(t.VideoFiles))
	uploading := 0
	errored := 0
	for _, file := range t.VideoFiles {
		if err := file.Validate(); err != nil {
			return err
		}
		if file.FinishedTakeID != t.ID {
			return invalid("videoFiles.finishedTakeId", "must match the finished take")
		}
		if _, exists := seen[file.CameraIdentityID]; exists {
			return invalid("videoFiles.cameraIdentityId", "must be unique within the take")
		}
		seen[file.CameraIdentityID] = struct{}{}
		if file.State == VideoFileStateUploading {
			uploading++
		}
		if file.State == VideoFileStateErrored {
			errored++
		}
	}

	switch t.State {
	case FinishedTakeStateUploading:
		if uploading == 0 {
			return invalid("videoFiles", "an uploading take must contain an uploading file")
		}
	case FinishedTakeStateCompleted:
		if len(t.VideoFiles) == 0 || uploading != 0 || errored != 0 {
			return invalid("videoFiles", "a completed take must contain only completed files")
		}
	case FinishedTakeStateErrored:
		if uploading != 0 {
			return invalid("videoFiles", "an errored take cannot contain uploading files")
		}
	}
	return nil
}

type VideoFileState string

const (
	VideoFileStateUploading VideoFileState = "uploading"
	VideoFileStateCompleted VideoFileState = "completed"
	VideoFileStateErrored   VideoFileState = "errored"
)

type VideoFile struct {
	FinishedTakeID   TakeID
	CameraIdentityID CameraIdentityID
	State            VideoFileState
	StartedAt        time.Time
	FinishedAt       time.Time
	ObjectKey        string
	Hash             *ContentHash
	Size             *int64
	Error            string
}

func (f VideoFile) Validate() error {
	if err := validateID("finishedTakeId", string(f.FinishedTakeID)); err != nil {
		return err
	}
	if err := validateID("cameraIdentityId", string(f.CameraIdentityID)); err != nil {
		return err
	}
	if f.State != VideoFileStateUploading && f.State != VideoFileStateCompleted && f.State != VideoFileStateErrored {
		return invalid("state", "must be uploading, completed, or errored")
	}
	if err := validateTime("startedAt", f.StartedAt); err != nil {
		return err
	}
	if err := validateTime("finishedAt", f.FinishedAt); err != nil {
		return err
	}
	if f.FinishedAt.Before(f.StartedAt) {
		return invalid("finishedAt", "must not precede startedAt")
	}
	if f.Size != nil && *f.Size < 0 {
		return invalid("size", "must not be negative")
	}
	if f.State == VideoFileStateCompleted {
		if f.ObjectKey == "" {
			return invalid("objectKey", "must be set when state is completed")
		}
		if f.Hash == nil {
			return invalid("hash", "must be set when state is completed")
		}
		if f.Size == nil {
			return invalid("size", "must be set when state is completed")
		}
	}
	if f.State == VideoFileStateErrored {
		return validateErrorReason("error", f.Error)
	}
	if f.Error != "" {
		return invalid("error", "must only be set when state is errored")
	}
	return nil
}
