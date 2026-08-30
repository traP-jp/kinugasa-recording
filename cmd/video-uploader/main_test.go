package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/traP-jp/kinugasa-recording/internal/shared/uploadqueue"
)

func TestUploaderConfigFromEnvironment(t *testing.T) {
	t.Setenv("KINUGASA_SESSION_ID", "session-id")
	t.Setenv("KINUGASA_CAMERA_IDENTITY_ID", "camera-id")
	t.Setenv("KINUGASA_CONSOLE_GRPC_ADDRESS", "console:9090")
	t.Setenv("KINUGASA_S3_BUCKET", "recordings")
	t.Setenv("KINUGASA_S3_REGION", "us-east-1")
	t.Setenv("KINUGASA_S3_PATH_STYLE", "true")
	config, err := uploaderConfigFromEnvironment()
	if err != nil {
		t.Fatalf("uploaderConfigFromEnvironment() error = %v", err)
	}
	if config.S3.Bucket != "recordings" || !config.S3.UsePathStyle || config.MaximumAttempts != 5 {
		t.Fatalf("uploader config = %+v", config)
	}
}

func TestUploadWorkCompleteRequiresReportedTerminalManifests(t *testing.T) {
	volume := t.TempDir()
	relativePath := "recording/session/take/camera/video.mp4"
	filePath := filepath.Join(volume, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	queue, err := uploadqueue.Open(volume, "session-id", "camera-id")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := queue.Publish("take-id", relativePath, now, now); err != nil {
		t.Fatal(err)
	}
	if err := queue.MarkWorkerComplete(); err != nil {
		t.Fatal(err)
	}
	if complete, err := uploadWorkComplete(queue); err != nil || complete {
		t.Fatalf("uploadWorkComplete(pending) = %v, %v", complete, err)
	}
	manifests, _ := queue.List()
	manifest := manifests[0]
	manifest.State = uploadqueue.StateErrored
	manifest.Error = "upload failed"
	if err := queue.Save(manifest); err != nil {
		t.Fatal(err)
	}
	if complete, err := uploadWorkComplete(queue); err != nil || complete {
		t.Fatalf("uploadWorkComplete(unreported) = %v, %v", complete, err)
	}
	reportedAt := now.Add(time.Second)
	manifest.ReportedAt = &reportedAt
	if err := queue.Save(manifest); err != nil {
		t.Fatal(err)
	}
	if complete, err := uploadWorkComplete(queue); err != nil || !complete {
		t.Fatalf("uploadWorkComplete() = %v, %v", complete, err)
	}
}

func TestNextRetryDelayIsCapped(t *testing.T) {
	if got := nextRetryDelay(time.Second, 30*time.Second); got != 2*time.Second {
		t.Fatalf("nextRetryDelay(1s) = %v", got)
	}
	if got := nextRetryDelay(20*time.Second, 30*time.Second); got != 30*time.Second {
		t.Fatalf("nextRetryDelay(20s) = %v", got)
	}
}
