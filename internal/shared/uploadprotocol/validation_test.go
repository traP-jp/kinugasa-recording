package uploadprotocol

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	upv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_uploader/v1"
)

func TestValidateReportRequiresContentAddressedObjectKey(t *testing.T) {
	now := time.Date(2026, 8, 31, 15, 0, 0, 0, time.UTC)
	report := &upv1.UploadReport{
		SessionId: "session-id", CameraIdentityId: "camera-id", TakeId: "take-id",
		RelativePath: "recording/session/take/camera/video.mp4",
		StartedAt:    timestamppb.New(now), FinishedAt: timestamppb.New(now.Add(time.Minute)),
		State:     upv1.UploadState_UPLOAD_STATE_COMPLETED,
		ObjectKey: "recording/session/take/camera/incorrect-video.mp4",
		Sha256:    make([]byte, 32), Size: 42, ObservedAt: timestamppb.New(now.Add(2 * time.Minute)),
	}
	if err := ValidateReport(report); err == nil {
		t.Fatal("ValidateReport() error = nil")
	}
	report.ObjectKey = "recording/session/take/camera/" + strings.Repeat("0", 64) + "-video.mp4"
	if err := ValidateReport(report); err != nil {
		t.Fatalf("ValidateReport(valid) error = %v", err)
	}
}
