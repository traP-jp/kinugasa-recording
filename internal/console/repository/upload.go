package repository

import (
	"context"
	"errors"

	upv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_uploader/v1"
)

var ErrUploadReportMismatch = errors.New("repository: upload report mismatch")

type UploadRepository interface {
	ApplyUploadReport(context.Context, *upv1.UploadReport) error
}
