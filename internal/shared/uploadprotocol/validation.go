package uploadprotocol

import (
	"encoding/hex"
	"fmt"
	"path"
	"strings"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/shared/workerprotocol"
)

func ValidateReport(report *workerv1.UploadReport) error {
	if report == nil {
		return fmt.Errorf("upload report must be set")
	}
	if report.SessionId == "" || report.CameraIdentityId == "" || report.TakeId == "" {
		return fmt.Errorf("upload report identity fields must be set")
	}
	if err := workerprotocol.ValidateRelativePath("relative_path", report.RelativePath); err != nil {
		return err
	}
	if err := workerprotocol.ValidateTimestamp("started_at", report.StartedAt); err != nil {
		return err
	}
	if err := workerprotocol.ValidateTimestamp("finished_at", report.FinishedAt); err != nil {
		return err
	}
	if report.FinishedAt.AsTime().Before(report.StartedAt.AsTime()) {
		return fmt.Errorf("upload report finish time must not precede start time")
	}
	if err := workerprotocol.ValidateTimestamp("observed_at", report.ObservedAt); err != nil {
		return err
	}
	switch report.State {
	case workerv1.UploadState_UPLOAD_STATE_COMPLETED:
		if report.ObjectKey == "" || len(report.Sha256) != 32 || report.Size < 0 || report.Error != "" {
			return fmt.Errorf("completed upload report metadata is invalid")
		}
		expectedKey := path.Join(path.Dir(report.RelativePath), hex.EncodeToString(report.Sha256)+"-video.mp4")
		if report.ObjectKey != expectedKey {
			return fmt.Errorf("completed upload report object key does not match its path and SHA-256")
		}
	case workerv1.UploadState_UPLOAD_STATE_ERRORED:
		if strings.TrimSpace(report.Error) == "" {
			return fmt.Errorf("errored upload report must contain an error")
		}
		if report.ObjectKey != "" || len(report.Sha256) != 0 || report.Size != 0 {
			return fmt.Errorf("errored upload report must not contain object metadata")
		}
	default:
		return fmt.Errorf("upload report state must not be UNSPECIFIED")
	}
	return nil
}
