package uploadqueue

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQueuePublishesAndUpdatesManifestDurably(t *testing.T) {
	volume := t.TempDir()
	relativePath := "recordings/take-id/video.mp4"
	absolutePath := filepath.Join(volume, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o700); err != nil {
		t.Fatalf("mkdir recording: %v", err)
	}
	if err := os.WriteFile(absolutePath, []byte("video"), 0o600); err != nil {
		t.Fatalf("write recording: %v", err)
	}
	queue, err := Open(volume, "session-id", "camera-id")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	startedAt := time.Date(2026, 8, 31, 7, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(time.Minute)
	if err := queue.Publish("take-id", relativePath, startedAt, finishedAt); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := queue.Publish("take-id", relativePath, startedAt, finishedAt); err != nil {
		t.Fatalf("Publish(idempotent) error = %v", err)
	}
	manifests, err := queue.List()
	if err != nil || len(manifests) != 1 || manifests[0].State != StatePending {
		t.Fatalf("List() = %+v, %v", manifests, err)
	}
	manifest := manifests[0]
	manifest.State = StateCompleted
	manifest.ObjectKey = "recording/session/take/camera/hash-video.mp4"
	manifest.SHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	manifest.Size = 5
	if err := queue.Save(manifest); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	reopened, err := Open(volume, "session-id", "camera-id")
	if err != nil {
		t.Fatalf("Open(restart) error = %v", err)
	}
	manifests, err = reopened.List()
	if err != nil || len(manifests) != 1 || !manifests[0].Terminal() {
		t.Fatalf("List(restart) = %+v, %v", manifests, err)
	}
	reportedAt := finishedAt.Add(time.Minute)
	manifest.ReportedAt = &reportedAt
	if err := reopened.Save(manifest); err != nil {
		t.Fatalf("Save(report acknowledgement) error = %v", err)
	}
	manifest.Error = "changed terminal result"
	if err := reopened.Save(manifest); err == nil {
		t.Fatal("Save(changed terminal manifest) error = nil")
	}
}

func TestQueueWorkerCompletionMarker(t *testing.T) {
	queue, err := Open(t.TempDir(), "session-id", "camera-id")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if complete, err := queue.WorkerComplete(); err != nil || complete {
		t.Fatalf("WorkerComplete(initial) = %v, %v", complete, err)
	}
	if err := queue.MarkWorkerComplete(); err != nil {
		t.Fatalf("MarkWorkerComplete() error = %v", err)
	}
	if complete, err := queue.WorkerComplete(); err != nil || !complete {
		t.Fatalf("WorkerComplete() = %v, %v", complete, err)
	}
}

func TestQueueRejectsChangedManifestAndMissingFile(t *testing.T) {
	volume := t.TempDir()
	queue, err := Open(volume, "session-id", "camera-id")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	now := time.Now().UTC()
	if err := queue.Publish("take-id", "missing/video.mp4", now, now); err == nil {
		t.Fatal("Publish(missing file) error = nil")
	}
}
