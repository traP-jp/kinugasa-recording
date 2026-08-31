package upload

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/shared/uploadprotocol"
	"github.com/traP-jp/kinugasa-recording/internal/shared/uploadqueue"
)

type Reporter struct {
	queue   Queue
	service UploadService
	now     func() time.Time
}

type UploadService interface {
	ReportUpload(context.Context, *workerv1.UploadReport, ...grpc.CallOption) (*workerv1.UploadReportAcknowledged, error)
}

func NewReporter(queue Queue, service UploadService) (*Reporter, error) {
	if queue == nil || service == nil {
		return nil, fmt.Errorf("upload reporter queue and service must be set")
	}
	return &Reporter{queue: queue, service: service, now: time.Now}, nil
}

func (r *Reporter) ReportPending(ctx context.Context) (int, error) {
	manifests, err := r.queue.List()
	if err != nil {
		return 0, err
	}
	reported := 0
	var reportErrors []error
	for _, manifest := range manifests {
		if !manifest.Terminal() || manifest.Reported() {
			continue
		}
		report, err := reportFromManifest(manifest, r.now().UTC())
		if err != nil {
			reportErrors = append(reportErrors, fmt.Errorf("build upload report for take %q: %w", manifest.TakeID, err))
			continue
		}
		acknowledgement, err := r.service.ReportUpload(ctx, report)
		if err != nil {
			reportErrors = append(reportErrors, fmt.Errorf("report terminal upload for take %q: %w", manifest.TakeID, err))
			continue
		}
		if acknowledgement.TakeId != manifest.TakeID || acknowledgement.CameraIdentityId != manifest.CameraIdentityID ||
			acknowledgement.AcceptedAt == nil || acknowledgement.AcceptedAt.CheckValid() != nil {
			reportErrors = append(reportErrors, fmt.Errorf("console returned an invalid upload acknowledgement for take %q", manifest.TakeID))
			continue
		}
		acceptedAt := acknowledgement.AcceptedAt.AsTime().UTC()
		manifest.ReportedAt = &acceptedAt
		if err := r.queue.Save(manifest); err != nil {
			reportErrors = append(reportErrors, fmt.Errorf("persist upload acknowledgement for take %q: %w", manifest.TakeID, err))
			continue
		}
		reported++
	}
	return reported, errors.Join(reportErrors...)
}

func reportFromManifest(manifest uploadqueue.Manifest, observedAt time.Time) (*workerv1.UploadReport, error) {
	report := &workerv1.UploadReport{
		SessionId: manifest.SessionID, CameraIdentityId: manifest.CameraIdentityID, TakeId: manifest.TakeID,
		RelativePath: manifest.RelativePath,
		StartedAt:    timestamppb.New(manifest.StartedAt), FinishedAt: timestamppb.New(manifest.FinishedAt),
		ObservedAt: timestamppb.New(observedAt),
	}
	switch manifest.State {
	case uploadqueue.StateCompleted:
		digest, err := hex.DecodeString(manifest.SHA256)
		if err != nil {
			return nil, fmt.Errorf("decode upload manifest SHA-256: %w", err)
		}
		report.State = workerv1.UploadState_UPLOAD_STATE_COMPLETED
		report.ObjectKey, report.Sha256, report.Size = manifest.ObjectKey, digest, manifest.Size
	case uploadqueue.StateErrored:
		report.State = workerv1.UploadState_UPLOAD_STATE_ERRORED
		report.Error = manifest.Error
	default:
		return nil, fmt.Errorf("upload manifest is not terminal")
	}
	if err := uploadprotocol.ValidateReport(report); err != nil {
		return nil, err
	}
	return report, nil
}
