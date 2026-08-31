package workercontrol

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
)

func TestReportUploadAcknowledgesRepositoryCommit(t *testing.T) {
	repository := &workerRepositoryStub{}
	server := NewServer(repository, nil)
	server.now = func() time.Time { return time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC) }
	report := validUploadReport()
	acknowledgement, err := server.ReportUpload(context.Background(), report)
	if err != nil {
		t.Fatalf("ReportUpload() error = %v", err)
	}
	if !repository.uploadApplied || acknowledgement.TakeId != report.TakeId ||
		acknowledgement.CameraIdentityId != report.CameraIdentityId {
		t.Fatalf("acknowledgement/repository = %+v/%v", acknowledgement, repository.uploadApplied)
	}
}

func TestReportUploadRejectsInvalidReport(t *testing.T) {
	server := NewServer(&workerRepositoryStub{}, nil)
	report := validUploadReport()
	report.Sha256 = nil
	if _, err := server.ReportUpload(context.Background(), report); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ReportUpload() error = %v, want InvalidArgument", err)
	}
}

func validUploadReport() *workerv1.UploadReport {
	now := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	return &workerv1.UploadReport{
		SessionId: "session-id", CameraIdentityId: "camera-id", TakeId: "take-id",
		RelativePath: "recording/session/take/camera/video.mp4",
		StartedAt:    timestamppb.New(now), FinishedAt: timestamppb.New(now.Add(time.Minute)),
		State:     workerv1.UploadState_UPLOAD_STATE_COMPLETED,
		ObjectKey: "recording/session/take/camera/0000000000000000000000000000000000000000000000000000000000000000-video.mp4",
		Sha256:    make([]byte, 32), Size: 42,
		ObservedAt: timestamppb.New(now.Add(2 * time.Minute)),
	}
}
