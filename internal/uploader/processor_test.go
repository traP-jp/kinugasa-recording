package uploader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/traP-jp/kinugasa-recording/internal/shared/uploadqueue"
	"github.com/traP-jp/kinugasa-recording/internal/uploader/objectstore/filesystem"
)

func TestProcessorHashesAndUploadsFinalizedRecording(t *testing.T) {
	volume := t.TempDir()
	relativePath := "recording/session/take/camera/video.mp4"
	content := []byte("deterministic finalized mp4 substitute")
	writeUploadTestFile(t, volume, relativePath, content)
	queue, err := uploadqueue.Open(volume, "session-id", "camera-id")
	if err != nil {
		t.Fatalf("uploadqueue.Open() error = %v", err)
	}
	now := time.Date(2026, 8, 31, 8, 0, 0, 0, time.UTC)
	if err := queue.Publish("take-id", relativePath, now, now.Add(time.Minute)); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	objectRoot := t.TempDir()
	objects, err := filesystem.New(objectRoot)
	if err != nil {
		t.Fatalf("filesystem.New() error = %v", err)
	}
	processor, err := NewProcessor(volume, queue, objects, 3)
	if err != nil {
		t.Fatalf("NewProcessor() error = %v", err)
	}
	processed, err := processor.ProcessPending(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("ProcessPending() = %d, %v", processed, err)
	}
	manifests, err := queue.List()
	if err != nil || len(manifests) != 1 {
		t.Fatalf("List() = %+v, %v", manifests, err)
	}
	manifest := manifests[0]
	if manifest.State != uploadqueue.StateCompleted || manifest.Size != int64(len(content)) ||
		manifest.ObjectKey != "recording/session/take/camera/bd9f5f7501c214634474a069164789c3a0587f1c8bbade91e3b6d33005172b7b-video.mp4" {
		t.Fatalf("completed manifest = %+v", manifest)
	}
	uploaded, err := os.ReadFile(filepath.Join(objectRoot, filepath.FromSlash(manifest.ObjectKey)))
	if err != nil || !bytes.Equal(uploaded, content) {
		t.Fatalf("uploaded object = %q, %v", uploaded, err)
	}
}

func TestProcessorRetriesThenPersistsTerminalFailure(t *testing.T) {
	volume := t.TempDir()
	relativePath := "recording/session/take/camera/video.mp4"
	writeUploadTestFile(t, volume, relativePath, []byte("video"))
	queue, err := uploadqueue.Open(volume, "session-id", "camera-id")
	if err != nil {
		t.Fatalf("uploadqueue.Open() error = %v", err)
	}
	now := time.Now().UTC()
	if err := queue.Publish("take-id", relativePath, now, now); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	processor, err := NewProcessor(volume, queue, failingObjectStore{}, 2)
	if err != nil {
		t.Fatalf("NewProcessor() error = %v", err)
	}
	if _, err := processor.ProcessPending(context.Background()); err == nil {
		t.Fatal("ProcessPending(first failure) error = nil")
	}
	if _, err := processor.ProcessPending(context.Background()); err != nil {
		t.Fatalf("ProcessPending(terminal failure) error = %v", err)
	}
	manifests, err := queue.List()
	if err != nil || len(manifests) != 1 || manifests[0].State != uploadqueue.StateErrored ||
		manifests[0].Attempts != 2 || manifests[0].Error == "" {
		t.Fatalf("terminal manifest = %+v, %v", manifests, err)
	}
}

type failingObjectStore struct{}

func (failingObjectStore) Put(context.Context, string, io.Reader, int64, string) error {
	return errors.New("temporary object store failure")
}

func writeUploadTestFile(t *testing.T, volume, relativePath string, content []byte) {
	t.Helper()
	filePath := filepath.Join(volume, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		t.Fatalf("mkdir upload test file: %v", err)
	}
	if err := os.WriteFile(filePath, content, 0o600); err != nil {
		t.Fatalf("write upload test file: %v", err)
	}
}
