package uploader

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	upv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_uploader/v1"
	"github.com/traP-jp/kinugasa-recording/internal/shared/uploadprotocol"
	"github.com/traP-jp/kinugasa-recording/internal/shared/uploadqueue"
)

type Reporter struct {
	queue   Queue
	service upv1.ConsoleVideoUploaderServiceClient
	now     func() time.Time
}

func NewReporter(queue Queue, service upv1.ConsoleVideoUploaderServiceClient) (*Reporter, error) {
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
	for _, manifest := range manifests {
		if !manifest.Terminal() || manifest.Reported() {
			continue
		}
		report, err := reportFromManifest(manifest, r.now().UTC())
		if err != nil {
			return reported, err
		}
		acknowledgement, err := r.service.ReportUpload(ctx, report)
		if err != nil {
			return reported, fmt.Errorf("report terminal upload: %w", err)
		}
		if acknowledgement.TakeId != manifest.TakeID || acknowledgement.CameraIdentityId != manifest.CameraIdentityID ||
			acknowledgement.AcceptedAt == nil || acknowledgement.AcceptedAt.CheckValid() != nil {
			return reported, fmt.Errorf("console returned an invalid upload acknowledgement")
		}
		acceptedAt := acknowledgement.AcceptedAt.AsTime().UTC()
		manifest.ReportedAt = &acceptedAt
		if err := r.queue.Save(manifest); err != nil {
			return reported, fmt.Errorf("persist upload acknowledgement: %w", err)
		}
		reported++
	}
	return reported, nil
}

func reportFromManifest(manifest uploadqueue.Manifest, observedAt time.Time) (*upv1.UploadReport, error) {
	report := &upv1.UploadReport{
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
		report.State = upv1.UploadState_UPLOAD_STATE_COMPLETED
		report.ObjectKey, report.Sha256, report.Size = manifest.ObjectKey, digest, manifest.Size
	case uploadqueue.StateErrored:
		report.State = upv1.UploadState_UPLOAD_STATE_ERRORED
		report.Error = manifest.Error
	default:
		return nil, fmt.Errorf("upload manifest is not terminal")
	}
	if err := uploadprotocol.ValidateReport(report); err != nil {
		return nil, err
	}
	return report, nil
}
