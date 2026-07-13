package operator

import (
	"context"
	"errors"
	"testing"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	livekit "github.com/livekit/protocol/livekit"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type liveKitIngressStub struct {
	items            []*livekit.IngressInfo
	participants     []*livekit.ParticipantInfo
	creates, deletes int
}

func (stub *liveKitIngressStub) ListParticipants(context.Context, *livekit.ListParticipantsRequest) (*livekit.ListParticipantsResponse, error) {
	return &livekit.ListParticipantsResponse{Participants: stub.participants}, nil
}

func (stub *liveKitIngressStub) CreateIngress(_ context.Context, request *livekit.CreateIngressRequest) (*livekit.IngressInfo, error) {
	stub.creates++
	info := &livekit.IngressInfo{IngressId: "ingress-1", Name: request.Name, Url: "http://livekit-ingress:8080/whip/key", RoomName: request.RoomName, ParticipantIdentity: request.ParticipantIdentity}
	stub.items = append(stub.items, info)
	return info, nil
}
func (stub *liveKitIngressStub) ListIngress(context.Context, *livekit.ListIngressRequest) (*livekit.ListIngressResponse, error) {
	return &livekit.ListIngressResponse{Items: stub.items}, nil
}
func (stub *liveKitIngressStub) DeleteIngress(_ context.Context, request *livekit.DeleteIngressRequest) (*livekit.IngressInfo, error) {
	stub.deletes++
	for index, item := range stub.items {
		if item.IngressId == request.IngressId {
			stub.items = append(stub.items[:index], stub.items[index+1:]...)
			return item, nil
		}
	}
	return &livekit.IngressInfo{}, nil
}

func TestLiveKitIngressManagerEnsuresAndDeletesCameraIngress(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	session := cameraTestSession("Session-A", "recording")
	kubernetesClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(session).Build()
	liveKit := &liveKitIngressStub{}
	manager := &LiveKitIngressManager{Client: kubernetesClient, API: liveKit, Participants: liveKit, RoomName: "kinugasa-preview"}
	camera := recordingv1alpha1.CameraSpec{Name: "front"}

	info, err := manager.Ensure(context.Background(), session, camera)
	if err != nil {
		t.Fatalf("Ensure() returned %v", err)
	}
	if info.ParticipantIdentity != liveKitIngressName("Session-A", "front") || liveKit.creates != 1 {
		t.Fatalf("info = %#v, creates = %d", info, liveKit.creates)
	}
	if _, err := manager.Ensure(context.Background(), session, camera); err != nil {
		t.Fatal(err)
	}
	if liveKit.creates != 1 {
		t.Fatalf("idempotent Ensure created %d ingresses", liveKit.creates)
	}
	secret, err := getSecret(context.Background(), kubernetesClient, session.Namespace, cameraWHIPSecretName(session.Name, camera.Name))
	if err != nil {
		t.Fatal(err)
	}
	if secret.StringData[whipURLSecretKey] != info.Url {
		t.Fatalf("secret = %#v", secret.StringData)
	}

	liveKit.participants = []*livekit.ParticipantInfo{{Identity: info.ParticipantIdentity}}
	if err := manager.Delete(context.Background(), session, camera.Name); !errors.Is(err, ErrLiveKitParticipantPresent) {
		t.Fatalf("Delete() with participant returned %v", err)
	}
	if _, err := getSecret(context.Background(), kubernetesClient, session.Namespace, cameraWHIPSecretName(session.Name, camera.Name)); err != nil {
		t.Fatal("WHIP secret was deleted while participant remained")
	}
	liveKit.participants = nil
	if err := manager.Delete(context.Background(), session, camera.Name); err != nil {
		t.Fatal(err)
	}
	if liveKit.deletes != 1 {
		t.Fatalf("deletes = %d", liveKit.deletes)
	}
	if _, err := getSecret(context.Background(), kubernetesClient, session.Namespace, cameraWHIPSecretName(session.Name, camera.Name)); err == nil {
		t.Fatal("WHIP secret still exists")
	}
}
