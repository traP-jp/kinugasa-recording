package cameraconnection

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	recordingv1alpha1 "github.com/traP-jp/kinugasa-recording/api/v1alpha1"
	livekitingress "github.com/traP-jp/kinugasa-recording/internal/livekit/ingress"
)

const (
	previewIngressIDKey = "ingress-id"
	previewURLKey       = "url"
	previewTokenKey     = "token"
)

type PreviewIngress interface {
	Create(context.Context, string, string, string) (livekitingress.Endpoint, error)
	Delete(context.Context, string) error
}

func (r *Reconciler) ensurePreviewSecret(
	ctx context.Context,
	connection *recordingv1alpha1.CameraConnection,
) error {
	if r.PreviewIngress == nil {
		return fmt.Errorf("LiveKit preview ingress is not configured")
	}
	var existing corev1.Secret
	key := client.ObjectKeyFromObject(connection)
	if err := r.Get(ctx, key, &existing); err == nil {
		if !metav1.IsControlledBy(&existing, connection) {
			return fmt.Errorf("preview Secret %s is not controlled by CameraConnection", existing.Name)
		}
		if len(existing.Data[previewIngressIDKey]) == 0 || len(existing.Data[previewURLKey]) == 0 ||
			len(existing.Data[previewTokenKey]) == 0 {
			return fmt.Errorf("preview Secret %s is incomplete", existing.Name)
		}
		return nil
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get preview Secret: %w", err)
	}

	endpoint, err := r.PreviewIngress.Create(
		ctx,
		connection.Spec.SessionName,
		connection.Spec.CameraName,
		"kinugasa-"+connection.Name,
	)
	if err != nil {
		return fmt.Errorf("create LiveKit WHIP ingress: %w", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      connection.Name,
			Namespace: connection.Namespace,
			Labels:    labelsFor(connection),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			previewIngressIDKey: []byte(endpoint.IngressID),
			previewURLKey:       []byte(endpoint.URL),
			previewTokenKey:     []byte(endpoint.StreamKey),
		},
	}
	if err := controllerutil.SetControllerReference(connection, secret, r.Scheme); err != nil {
		cleanupError := r.PreviewIngress.Delete(ctx, endpoint.IngressID)
		return errors.Join(fmt.Errorf("set preview Secret owner: %w", err), cleanupError)
	}
	if err := r.Create(ctx, secret); err != nil {
		cleanupError := r.PreviewIngress.Delete(ctx, endpoint.IngressID)
		return errors.Join(fmt.Errorf("create preview Secret: %w", err), cleanupError)
	}
	var stalePod corev1.Pod
	if err := r.Get(ctx, key, &stalePod); err == nil {
		if err := r.Delete(ctx, &stalePod); err != nil {
			return fmt.Errorf("restart worker Pod after replacing preview ingress: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get worker Pod after replacing preview ingress: %w", err)
	}
	return nil
}

func (r *Reconciler) releasePreview(ctx context.Context, connection *recordingv1alpha1.CameraConnection) error {
	var secret corev1.Secret
	if err := r.Get(ctx, client.ObjectKeyFromObject(connection), &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get preview Secret during deletion: %w", err)
	}
	if r.PreviewIngress == nil {
		return fmt.Errorf("LiveKit preview ingress is not configured")
	}
	if ingressID := string(secret.Data[previewIngressIDKey]); ingressID != "" {
		if err := r.PreviewIngress.Delete(ctx, ingressID); err != nil {
			return fmt.Errorf("delete LiveKit WHIP ingress: %w", err)
		}
	}
	if err := r.Delete(ctx, &secret); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("delete preview Secret: %w", err)
	}
	return nil
}
