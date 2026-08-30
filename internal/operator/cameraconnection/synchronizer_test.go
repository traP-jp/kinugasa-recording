package cameraconnection

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	recordingv1alpha1 "github.com/traP-jp/kinugasa-recording/api/v1alpha1"
	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

func TestSynchronizerConvergesResourcesToDatabase(t *testing.T) {
	scheme := testScheme(t)
	stale := &recordingv1alpha1.CameraConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "camera-stale", Namespace: "recording"},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(stale).Build()
	source := &cameraSourceStub{resources: []repository.CameraResource{{
		Camera: repository.Camera{
			Identity: domain.CameraIdentity{
				ID:        "019c240e-3eb4-72d6-a6fa-adfe1df795c8",
				SessionID: "019c240d-a6de-7de0-a826-0f26e8803fc0",
				Name:      "camera-1",
			},
		},
		SessionName: "session-1",
	}}}
	synchronizer := Synchronizer{Client: fakeClient, Source: source, Namespace: "recording"}

	if err := synchronizer.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	var resources recordingv1alpha1.CameraConnectionList
	if err := fakeClient.List(context.Background(), &resources); err != nil {
		t.Fatalf("list CameraConnections: %v", err)
	}
	if len(resources.Items) != 1 {
		t.Fatalf("CameraConnection count = %d, want 1", len(resources.Items))
	}
	connection := resources.Items[0]
	if connection.Name != "camera-019c240e-3eb4-72d6-a6fa-adfe1df795c8" ||
		connection.Spec.SessionName != "session-1" || connection.Spec.CameraName != "camera-1" {
		t.Fatalf("CameraConnection = %+v", connection)
	}

	if err := synchronizer.Sync(context.Background()); err != nil {
		t.Fatalf("second Sync() error = %v", err)
	}
	resources.Items = nil
	if err := fakeClient.List(context.Background(), &resources); err != nil {
		t.Fatalf("list CameraConnections after second sync: %v", err)
	}
	if len(resources.Items) != 1 {
		t.Fatalf("CameraConnection count after second sync = %d, want 1", len(resources.Items))
	}
}

type cameraSourceStub struct {
	resources []repository.CameraResource
	err       error
}

func (s *cameraSourceStub) ListCameraResources(context.Context) ([]repository.CameraResource, error) {
	return s.resources, s.err
}
