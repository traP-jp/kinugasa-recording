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
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

const defaultSyncInterval = 30 * time.Second

type CameraSource interface {
	ListCameraResources(context.Context) ([]repository.CameraResource, error)
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
	desired := make(map[string]repository.CameraResource, len(desiredCameras))
	for _, camera := range desiredCameras {
		name := resourceName(camera)
		desired[name] = camera
		if err := s.ensureResource(ctx, name, camera); err != nil {
			return err
		}
	}

	var existing recordingv1alpha1.CameraConnectionList
	if err := s.Client.List(ctx, &existing, client.InNamespace(s.Namespace)); err != nil {
		return fmt.Errorf("list CameraConnection resources: %w", err)
	}
	for index := range existing.Items {
		connection := &existing.Items[index]
		if _, ok := desired[connection.Name]; ok {
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
) error {
	key := client.ObjectKey{Namespace: s.Namespace, Name: name}
	var existing recordingv1alpha1.CameraConnection
	if err := s.Client.Get(ctx, key, &existing); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get CameraConnection %q: %w", name, err)
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
		return fmt.Errorf("create CameraConnection %q: %w", name, err)
	}
	return nil
}

func resourceName(camera repository.CameraResource) string {
	return "camera-" + string(camera.Identity.ID)
}
