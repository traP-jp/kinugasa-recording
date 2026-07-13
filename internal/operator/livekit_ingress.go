package operator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	livekit "github.com/livekit/protocol/livekit"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const whipURLSecretKey = "whip-url"

type LiveKitIngressAPI interface {
	CreateIngress(context.Context, *livekit.CreateIngressRequest) (*livekit.IngressInfo, error)
	ListIngress(context.Context, *livekit.ListIngressRequest) (*livekit.ListIngressResponse, error)
	DeleteIngress(context.Context, *livekit.DeleteIngressRequest) (*livekit.IngressInfo, error)
}

type LiveKitParticipantAPI interface {
	ListParticipants(context.Context, *livekit.ListParticipantsRequest) (*livekit.ListParticipantsResponse, error)
}

var ErrLiveKitParticipantPresent = errors.New("LiveKit ingress participant is still present")

type LiveKitIngressManager struct {
	Client       client.Client
	API          LiveKitIngressAPI
	Participants LiveKitParticipantAPI
	RoomName     string
}

func (manager *LiveKitIngressManager) Ensure(ctx context.Context, session *recordingv1alpha1.Session, camera recordingv1alpha1.CameraSpec) (*livekit.IngressInfo, error) {
	if manager.API == nil || manager.Client == nil || manager.RoomName == "" {
		return nil, fmt.Errorf("LiveKit ingress manager is not configured")
	}
	name := liveKitIngressName(session.Spec.Name, camera.Name)
	listed, err := manager.API.ListIngress(ctx, &livekit.ListIngressRequest{RoomName: manager.RoomName})
	if err != nil {
		return nil, fmt.Errorf("list LiveKit ingresses: %w", err)
	}
	var info *livekit.IngressInfo
	for _, item := range listed.Items {
		if item.Name == name {
			info = item
			break
		}
	}
	if info == nil {
		transcode := false
		metadata, _ := json.Marshal(map[string]string{"session": session.Spec.Name, "camera": camera.Name})
		info, err = manager.API.CreateIngress(ctx, &livekit.CreateIngressRequest{
			InputType: livekit.IngressInput_WHIP_INPUT, Name: name, RoomName: manager.RoomName,
			ParticipantIdentity: name, ParticipantName: camera.Name, ParticipantMetadata: string(metadata),
			EnableTranscoding: &transcode,
			Video:             &livekit.IngressVideoOptions{Name: camera.Name, Source: livekit.TrackSource_CAMERA},
		})
		if err != nil {
			return nil, fmt.Errorf("create LiveKit ingress: %w", err)
		}
	}
	if info.Url == "" || info.IngressId == "" || info.StreamKey == "" {
		return nil, fmt.Errorf("LiveKit ingress returned incomplete connection settings")
	}
	if err := manager.ensureSecret(ctx, session, camera.Name, whipPublishURL(info)); err != nil {
		return nil, err
	}
	return info, nil
}

func whipPublishURL(info *livekit.IngressInfo) string {
	return strings.TrimRight(info.Url, "/") + "/" + url.PathEscape(info.StreamKey)
}

func (manager *LiveKitIngressManager) Delete(ctx context.Context, session *recordingv1alpha1.Session, cameraName string) error {
	name := liveKitIngressName(session.Spec.Name, cameraName)
	listed, err := manager.API.ListIngress(ctx, &livekit.ListIngressRequest{RoomName: manager.RoomName})
	if err != nil {
		return fmt.Errorf("list LiveKit ingresses: %w", err)
	}
	for _, info := range listed.Items {
		if info.Name == name {
			if _, err := manager.API.DeleteIngress(ctx, &livekit.DeleteIngressRequest{IngressId: info.IngressId}); err != nil {
				return fmt.Errorf("delete LiveKit ingress: %w", err)
			}
		}
	}
	if manager.Participants == nil {
		return fmt.Errorf("LiveKit participant API is not configured")
	}
	participants, err := manager.Participants.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: manager.RoomName})
	if err != nil {
		return fmt.Errorf("list LiveKit participants: %w", err)
	}
	identity := liveKitIngressName(session.Spec.Name, cameraName)
	for _, participant := range participants.Participants {
		if participant.Identity == identity {
			return ErrLiveKitParticipantPresent
		}
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: cameraWHIPSecretName(session.Name, cameraName), Namespace: session.Namespace}}
	if err := manager.Client.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete WHIP secret: %w", err)
	}
	return nil
}

func (manager *LiveKitIngressManager) ensureSecret(ctx context.Context, session *recordingv1alpha1.Session, cameraName, url string) error {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: cameraWHIPSecretName(session.Name, cameraName), Namespace: session.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, manager.Client, secret, func() error {
		secret.Labels = cameraLabels(session.Name, cameraName)
		secret.Type = corev1.SecretTypeOpaque
		secret.StringData = map[string]string{whipURLSecretKey: url}
		return controllerutil.SetControllerReference(session, secret, manager.Client.Scheme())
	})
	if err != nil {
		return fmt.Errorf("publish WHIP connection secret: %w", err)
	}
	return nil
}

func liveKitIngressName(sessionName, cameraName string) string {
	digest := sha256.Sum256([]byte(sessionName + "\x00" + cameraName))
	return "camera-" + hex.EncodeToString(digest[:12])
}

func cameraWHIPSecretName(sessionResourceName, cameraName string) string {
	digest := sha256.Sum256([]byte(cameraName))
	return sessionResourceName + "-camera-" + hex.EncodeToString(digest[:6]) + "-whip"
}

func getSecret(ctx context.Context, reader client.Reader, namespace, name string) (*corev1.Secret, error) {
	var secret corev1.Secret
	if err := reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &secret); err != nil {
		return nil, err
	}
	return &secret, nil
}
