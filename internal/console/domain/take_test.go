package domain

import "testing"

func TestOngoingTakeValidate(t *testing.T) {
	t.Parallel()

	take := OngoingTake{
		ID:        testTakeID,
		SessionID: testSessionID,
		Name:      "take-1",
		StartedAt: testTime,
		Cameras: []RecordingCamera{{
			OngoingTakeID:    testTakeID,
			CameraIdentityID: testCameraID,
			State:            RecordingCameraStateRecording,
			StartedAt:        testTime,
		}},
	}
	if err := take.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	take.Cameras = append(take.Cameras, take.Cameras[0])
	if err := take.Validate(); err == nil {
		t.Fatal("Validate() error = nil for duplicate camera")
	}
}

func TestFinishedTakeStateMatchesVideoFiles(t *testing.T) {
	t.Parallel()

	size := int64(42)
	hash := ContentHash{1, 2, 3}
	completed := VideoFile{
		FinishedTakeID:   testTakeID,
		CameraIdentityID: testCameraID,
		State:            VideoFileStateCompleted,
		StartedAt:        testTime,
		FinishedAt:       testTime.Add(timeMinute),
		ObjectKey:        "recording/session/take/camera/hash-video.mp4",
		Hash:             &hash,
		Size:             &size,
	}
	uploading := VideoFile{
		FinishedTakeID:   testTakeID,
		CameraIdentityID: testCamera2ID,
		State:            VideoFileStateUploading,
		StartedAt:        testTime,
		FinishedAt:       testTime.Add(timeMinute),
	}

	tests := []struct {
		name  string
		state FinishedTakeState
		error string
		files []VideoFile
		valid bool
	}{
		{name: "uploading with an uploading file", state: FinishedTakeStateUploading, files: []VideoFile{completed, uploading}, valid: true},
		{name: "uploading without an uploading file", state: FinishedTakeStateUploading, files: []VideoFile{completed}, valid: false},
		{name: "completed with completed files", state: FinishedTakeStateCompleted, files: []VideoFile{completed}, valid: true},
		{name: "completed with uploading file", state: FinishedTakeStateCompleted, files: []VideoFile{uploading}, valid: false},
		{name: "errored directly", state: FinishedTakeStateErrored, error: "system failure", valid: true},
		{name: "errored with uploading file", state: FinishedTakeStateErrored, error: "upload failed", files: []VideoFile{uploading}, valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			take := FinishedTake{
				ID:         testTakeID,
				SessionID:  testSessionID,
				Name:       "take-1",
				State:      test.state,
				StartedAt:  testTime,
				FinishedAt: testTime.Add(timeMinute),
				Error:      test.error,
				VideoFiles: test.files,
			}
			err := take.Validate()
			if (err == nil) != test.valid {
				t.Fatalf("Validate() error = %v, valid = %v", err, test.valid)
			}
		})
	}
}

func TestVideoFileCompletedRequiresMetadata(t *testing.T) {
	t.Parallel()

	file := VideoFile{
		FinishedTakeID:   testTakeID,
		CameraIdentityID: testCameraID,
		State:            VideoFileStateCompleted,
		StartedAt:        testTime,
		FinishedAt:       testTime.Add(timeMinute),
	}
	if err := file.Validate(); err == nil {
		t.Fatal("Validate() error = nil without completed metadata")
	}
}

const timeMinute = 60_000_000_000
