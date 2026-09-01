package uploadqueue

import "time"

const schemaVersion = 1

type State string

const (
	StatePending   State = "pending"
	StateUploading State = "uploading"
	StateCompleted State = "completed"
	StateErrored   State = "errored"
)

type Manifest struct {
	SchemaVersion    int        `json:"schemaVersion"`
	SessionID        string     `json:"sessionId"`
	CameraIdentityID string     `json:"cameraIdentityId"`
	TakeID           string     `json:"takeId"`
	RelativePath     string     `json:"relativePath"`
	MediaType        string     `json:"mediaType"`
	StartedAt        time.Time  `json:"startedAt"`
	FinishedAt       time.Time  `json:"finishedAt"`
	State            State      `json:"state"`
	Attempts         int        `json:"attempts"`
	ObjectKey        string     `json:"objectKey,omitempty"`
	SHA256           string     `json:"sha256,omitempty"`
	Size             int64      `json:"size,omitempty"`
	Error            string     `json:"error,omitempty"`
	ReportedAt       *time.Time `json:"reportedAt,omitempty"`
}

func (m Manifest) Reported() bool { return m.ReportedAt != nil }

func (m Manifest) Terminal() bool {
	return m.State == StateCompleted || m.State == StateErrored
}
