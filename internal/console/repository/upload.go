package repository

import (
	"context"
	"errors"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
)

var ErrUploadReportMismatch = errors.New("repository: upload report mismatch")

type UploadRepository interface {
	ApplyUploadReport(context.Context, *workerv1.UploadReport) error
}
