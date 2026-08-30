package uploader

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	upv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_uploader/v1"
	"github.com/traP-jp/kinugasa-recording/internal/shared/uploadqueue"
)

func TestReporterPersistsAcknowledgement(t *testing.T) {
	volume := t.TempDir()
	path := "recording/session/take/camera/video.mp4"
	filePath := filepath.Join(volume, filepath.FromSlash(path))
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
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	if err := queue.Publish("take-id", path, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	manifests, _ := queue.List()
	manifest := manifests[0]
	manifest.State = uploadqueue.StateCompleted
	manifest.SHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	manifest.ObjectKey = "recording/session/take/camera/" + manifest.SHA256 + "-video.mp4"
	manifest.Size = 5
	if err := queue.Save(manifest); err != nil {
		t.Fatal(err)
	}
	service := &uploaderServiceStub{acceptedAt: now.Add(2 * time.Minute)}
	reporter, err := NewReporter(queue, service)
	if err != nil {
		t.Fatal(err)
	}
	reporter.now = func() time.Time { return now.Add(90 * time.Second) }
	count, err := reporter.ReportPending(context.Background())
	if err != nil || count != 1 || service.calls != 1 {
		t.Fatalf("ReportPending() = %d, %v; calls = %d", count, err, service.calls)
	}
	manifests, _ = queue.List()
	if !manifests[0].Reported() || !manifests[0].ReportedAt.Equal(service.acceptedAt) {
		t.Fatalf("reported manifest = %+v", manifests[0])
	}
	count, err = reporter.ReportPending(context.Background())
	if err != nil || count != 0 || service.calls != 1 {
		t.Fatalf("ReportPending(duplicate) = %d, %v; calls = %d", count, err, service.calls)
	}
}

type uploaderServiceStub struct {
	calls      int
	acceptedAt time.Time
}

func (s *uploaderServiceStub) ReportUpload(
	_ context.Context,
	report *upv1.UploadReport,
	_ ...grpc.CallOption,
) (*upv1.UploadReportAcknowledged, error) {
	s.calls++
	return &upv1.UploadReportAcknowledged{
		TakeId: report.TakeId, CameraIdentityId: report.CameraIdentityId,
		AcceptedAt: timestamppb.New(s.acceptedAt),
	}, nil
}
