package uploadreport

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	upv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_uploader/v1"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
	"github.com/traP-jp/kinugasa-recording/internal/shared/uploadprotocol"
)

type Server struct {
	upv1.UnimplementedConsoleVideoUploaderServiceServer
	repository repository.UploadRepository
	now        func() time.Time
}

func NewServer(repository repository.UploadRepository) *Server {
	return &Server{repository: repository, now: time.Now}
}

func (s *Server) ReportUpload(
	ctx context.Context,
	report *upv1.UploadReport,
) (*upv1.UploadReportAcknowledged, error) {
	if err := uploadprotocol.ValidateReport(report); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid upload report: %v", err)
	}
	if err := s.repository.ApplyUploadReport(ctx, report); err != nil {
		if errors.Is(err, repository.ErrUploadReportMismatch) {
			return nil, status.Error(codes.FailedPrecondition, "upload report does not match finalized recording")
		}
		return nil, status.Error(codes.Internal, "apply upload report failed")
	}
	return &upv1.UploadReportAcknowledged{
		TakeId:           report.TakeId,
		CameraIdentityId: report.CameraIdentityId,
		AcceptedAt:       timestamppb.New(s.now().UTC()),
	}, nil
}
