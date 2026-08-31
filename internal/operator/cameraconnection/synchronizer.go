package cameraconnection

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	recordingv1alpha1 "github.com/traP-jp/kinugasa-recording/api/v1alpha1"
	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

const defaultSyncInterval = 30 * time.Second

type CameraSource interface {
	ListCameraResources(context.Context) ([]repository.CameraResource, error)
	ActivateCameraConnection(context.Context, string, string) error
	CompleteCameraDeletion(context.Context, string) error
	MarkWorkerFailure(context.Context, string, string) error
}

type Synchronizer struct {
	Client    client.Client
	Source    CameraSource
	Namespace string
	Interval  time.Duration
	Logger    *slog.Logger
}

func (s *Synchronizer) Start(ctx context.Context) error {
	interval := s.Interval
	if interval <= 0 {
		interval = defaultSyncInterval
	}
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}

	s.syncAndLog(ctx, logger)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.syncAndLog(ctx, logger)
		}
	}
}

func (s *Synchronizer) NeedLeaderElection() bool {
	return true
}

func (s *Synchronizer) syncAndLog(ctx context.Context, logger *slog.Logger) {
	if err := s.Sync(ctx); err != nil && ctx.Err() == nil {
		logger.ErrorContext(ctx, "synchronize CameraConnection resources", "error", err)
	}
}

func (s *Synchronizer) Sync(ctx context.Context) error {
	desiredCameras, err := s.Source.ListCameraResources(ctx)
	if err != nil {
		return fmt.Errorf("list desired camera connections: %w", err)
	}
	known := make(map[string]repository.CameraResource, len(desiredCameras))
	for _, camera := range desiredCameras {
		name := resourceName(camera)
		known[name] = camera
		if camera.Deleting {
			var resource recordingv1alpha1.CameraConnection
			err := s.Client.Get(ctx, client.ObjectKey{Namespace: s.Namespace, Name: name}, &resource)
			switch {
			case err == nil:
				if err := s.Client.Delete(ctx, &resource); client.IgnoreNotFound(err) != nil {
					return fmt.Errorf("delete requested CameraConnection %q: %w", name, err)
				}
			case apierrors.IsNotFound(err):
				if err := s.Source.CompleteCameraDeletion(ctx, string(camera.Identity.ID)); err != nil {
					return fmt.Errorf("complete camera deletion %q: %w", name, err)
				}
			default:
				return fmt.Errorf("get deleting CameraConnection %q: %w", name, err)
			}
			continue
		}
		resource, err := s.ensureResource(ctx, name, camera)
		if err != nil {
			return err
		}
		if resource.Status.CameraURL != "" {
			if err := s.Source.ActivateCameraConnection(ctx, string(camera.Identity.ID), resource.Status.CameraURL); err != nil {
				return fmt.Errorf("activate camera connection %q: %w", name, err)
			}
		}
	}

	var existing recordingv1alpha1.CameraConnectionList
	if err := s.Client.List(ctx, &existing, client.InNamespace(s.Namespace)); err != nil {
		return fmt.Errorf("list CameraConnection resources: %w", err)
	}
	for index := range existing.Items {
		connection := &existing.Items[index]
		if _, ok := known[connection.Name]; ok {
			continue
		}
		if err := s.Client.Delete(ctx, connection); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("delete stale CameraConnection %q: %w", connection.Name, err)
		}
	}
	return nil
}

func (s *Synchronizer) ensureResource(
	ctx context.Context,
	name string,
	camera repository.CameraResource,
) (*recordingv1alpha1.CameraConnection, error) {
	key := client.ObjectKey{Namespace: s.Namespace, Name: name}
	var existing recordingv1alpha1.CameraConnection
	if err := s.Client.Get(ctx, key, &existing); err == nil {
		if err := s.syncResourceStatus(ctx, &existing, camera); err != nil {
			return nil, err
		}
		return &existing, nil
	} else if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("get CameraConnection %q: %w", name, err)
	}

	connection := &recordingv1alpha1.CameraConnection{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.Namespace},
		Spec: recordingv1alpha1.CameraConnectionSpec{
			SessionID:        string(camera.Identity.SessionID),
			SessionName:      camera.SessionName,
			CameraIdentityID: string(camera.Identity.ID),
			CameraName:       camera.Identity.Name,
		},
	}
	if err := s.Client.Create(ctx, connection); err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("create CameraConnection %q: %w", name, err)
	}
	return connection, nil
}

func (s *Synchronizer) syncResourceStatus(
	ctx context.Context,
	resource *recordingv1alpha1.CameraConnection,
	camera repository.CameraResource,
) error {
	phase, err := resourcePhase(camera.Connection.Status)
	if err != nil {
		return err
	}
	base := resource.DeepCopy()
	resource.Status.Phase = phase
	// The Service allocation recorded by the reconciler is authoritative. Keep
	// it when present so a changed external endpoint can converge back to DB.
	if resource.Status.CameraURL == "" {
		resource.Status.CameraURL = camera.Connection.URL
	}
	resource.Status.VideoWorkerID = string(camera.Connection.VideoWorkerID)
	resource.Status.Error = camera.Connection.Error
	if err := s.Client.Status().Patch(ctx, resource, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("synchronize CameraConnection status %q: %w", resource.Name, err)
	}
	return nil
}

func resourcePhase(status domain.CameraConnectionStatus) (recordingv1alpha1.CameraConnectionPhase, error) {
	switch status {
	case domain.CameraConnectionStatusActivating:
		return recordingv1alpha1.CameraConnectionPhaseActivating, nil
	case domain.CameraConnectionStatusWaiting:
		return recordingv1alpha1.CameraConnectionPhaseWaiting, nil
	case domain.CameraConnectionStatusConnected:
		return recordingv1alpha1.CameraConnectionPhaseConnected, nil
	case domain.CameraConnectionStatusError:
		return recordingv1alpha1.CameraConnectionPhaseError, nil
	default:
		return "", fmt.Errorf("unsupported CameraConnection status %q", status)
	}
}

func resourceName(camera repository.CameraResource) string {
	return "camera-" + string(camera.Identity.ID)
}
