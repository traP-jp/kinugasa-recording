package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type storedUploadObject struct {
	body     string
	metadata map[string]string
}

type fakeUploadClient struct {
	objects   map[string]storedUploadObject
	putCount  int
	putErrors int
}

func (client *fakeUploadClient) HeadObject(_ context.Context, input *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	object, found := client.objects[aws.ToString(input.Key)]
	if !found {
		return nil, &smithy.GenericAPIError{Code: "NotFound", Message: "missing"}
	}
	return &s3.HeadObjectOutput{Metadata: object.metadata}, nil
}

func (client *fakeUploadClient) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	client.putCount++
	if client.putErrors > 0 {
		client.putErrors--
		return nil, &smithy.GenericAPIError{Code: "InternalError", Message: "temporary failure"}
	}
	contents, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}
	client.objects[aws.ToString(input.Key)] = storedUploadObject{body: string(contents), metadata: input.Metadata}
	return &s3.PutObjectOutput{}, nil
}

func TestUploaderUploadsReadyFilesIdempotentlyAndCompletes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, directory := range []string{"ready", "staging", "state"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	name := "segment-00000000000000000000.ts"
	if err := os.WriteFile(filepath.Join(root, "ready", name), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &fakeUploadClient{objects: map[string]storedUploadObject{}}
	config := UploadConfig{Root: root, Bucket: "recordings", Session: "session-1", Take: "take-1", Camera: "front"}
	uploader, err := NewUploader(client, config)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := uploader.Sync(context.Background())
	if err != nil || complete {
		t.Fatalf("first Sync() = %v, %v", complete, err)
	}
	key := "session-1/take-1/front/" + name
	if client.objects[key].body != "video" || client.putCount != 1 {
		t.Fatalf("uploaded object = %#v, puts = %d", client.objects[key], client.putCount)
	}

	restarted, err := NewUploader(client, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.putCount != 1 {
		t.Fatalf("idempotent sync uploaded again: %d puts", client.putCount)
	}
	if err := os.WriteFile(filepath.Join(root, "state", "recorder.done"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	complete, err = restarted.Sync(context.Background())
	if err != nil || !complete {
		t.Fatalf("completed Sync() = %v, %v", complete, err)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "uploader.done")); err != nil {
		t.Fatal(err)
	}
}

func TestUploaderRejectsDigestConflict(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "ready"), 0o750); err != nil {
		t.Fatal(err)
	}
	name := "segment-00000000000000000000.ts"
	if err := os.WriteFile(filepath.Join(root, "ready", name), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := "session/take/camera/" + name
	client := &fakeUploadClient{objects: map[string]storedUploadObject{key: {metadata: map[string]string{"sha256": "different"}}}}
	uploader, err := NewUploader(client, UploadConfig{Root: root, Bucket: "bucket", Session: "session", Take: "take", Camera: "camera"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uploader.Sync(context.Background()); !errors.Is(err, ErrObjectConflict) {
		t.Fatalf("Sync() error = %v", err)
	}
}

func TestUploaderRetriesTemporaryFailureAndCompletes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "ready"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o750); err != nil {
		t.Fatal(err)
	}
	name := "segment-00000000000000000000.ts"
	if err := os.WriteFile(filepath.Join(root, "ready", name), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state", "recorder.done"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &fakeUploadClient{objects: map[string]storedUploadObject{}, putErrors: 1}
	uploader, err := NewUploader(client, UploadConfig{
		Root: root, Bucket: "bucket", Session: "session", Take: "take", Camera: "camera",
		PollInterval: time.Millisecond, RetryMax: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := uploader.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.putCount != 2 {
		t.Fatalf("put attempts = %d, want 2", client.putCount)
	}
	if uploader.state.Phase != "Completed" || uploader.state.LastError != "" {
		t.Fatalf("state = %#v", uploader.state)
	}
}

func TestUploaderWaitsForStagingPartBeforeCompletion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, directory := range []string{"ready", "staging", "state"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "state", "recorder.done"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(root, "staging", "segment-00000000000000000000.ts.part")
	if err := os.WriteFile(part, []byte("incomplete"), 0o600); err != nil {
		t.Fatal(err)
	}
	uploader, err := NewUploader(&fakeUploadClient{objects: map[string]storedUploadObject{}}, UploadConfig{Root: root, Bucket: "bucket", Session: "session", Take: "take", Camera: "camera"})
	if err != nil {
		t.Fatal(err)
	}
	complete, err := uploader.Sync(context.Background())
	if err != nil || complete {
		t.Fatalf("Sync() with staging part = %v, %v", complete, err)
	}
}
