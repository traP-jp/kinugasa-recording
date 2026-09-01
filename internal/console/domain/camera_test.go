package domain

import (
	"testing"
	"time"
)

const (
	testSessionID = SessionID("019c240d-a6de-7de0-a826-0f26e8803fc0")
	testCameraID  = CameraIdentityID("019c240e-3eb4-72d6-a6fa-adfe1df795c8")
	testCamera2ID = CameraIdentityID("019c240e-4a04-73e3-8328-a32a246b8c47")
	testTakeID    = TakeID("019c240e-5141-75e4-8b4b-5c611e7fab65")
	testWorkerID  = VideoWorkerID("019c240e-5a60-7770-afad-af8697c0b37a")
)

var testTime = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

func TestCameraConnectionValidate(t *testing.T) {
	t.Parallel()

	valid := CameraConnection{
		CameraIdentityID: testCameraID,
		URL:              "rist://recording.example.com:9200",
		Status:           CameraConnectionStatusConnected,
		VideoWorkerID:    testWorkerID,
	}
	tests := []struct {
		name   string
		mutate func(*CameraConnection)
	}{
		{name: "valid", mutate: func(*CameraConnection) {}},
		{name: "activating without URL", mutate: func(c *CameraConnection) {
			c.Status = CameraConnectionStatusActivating
			c.URL = ""
		}},
		{name: "error with reason", mutate: func(c *CameraConnection) {
			c.Status = CameraConnectionStatusError
			c.Error = "unsupported frame rate"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			connection := valid
			test.mutate(&connection)
			if err := connection.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestCameraConnectionRejectsInvalidInvariants(t *testing.T) {
	t.Parallel()

	valid := CameraConnection{
		CameraIdentityID: testCameraID,
		URL:              "rist://recording.example.com:9200",
		Status:           CameraConnectionStatusWaiting,
	}
	tests := []struct {
		name   string
		mutate func(*CameraConnection)
	}{
		{name: "missing URL", mutate: func(c *CameraConnection) { c.URL = "" }},
		{name: "relative URL", mutate: func(c *CameraConnection) { c.URL = "/camera" }},
		{name: "reason outside error state", mutate: func(c *CameraConnection) { c.Error = "failure" }},
		{name: "error without reason", mutate: func(c *CameraConnection) { c.Status = CameraConnectionStatusError }},
		{name: "invalid worker UUID", mutate: func(c *CameraConnection) { c.VideoWorkerID = "worker-1" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			connection := valid
			test.mutate(&connection)
			if err := connection.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want invariant violation")
			}
		})
	}
}
