package cameraconnection

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&recordingv1alpha1.CameraConnection{}).
		WithObjects(stale).Build()
	source := &cameraSourceStub{resources: []repository.CameraResource{{
		Camera: repository.Camera{
			Identity: domain.CameraIdentity{
				ID:        "019c240e-3eb4-72d6-a6fa-adfe1df795c8",
				SessionID: "019c240d-a6de-7de0-a826-0f26e8803fc0",
				Name:      "camera-1",
			},
			Connection: domain.CameraConnection{Status: domain.CameraConnectionStatusActivating},
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
	connection.Status.CameraURL = "rist://camera.example.com:9000"
	connection.Status.Phase = recordingv1alpha1.CameraConnectionPhaseWaiting
	if err := fakeClient.Status().Update(context.Background(), &connection); err != nil {
		t.Fatalf("update CameraConnection status: %v", err)
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
	if source.activatedID != "019c240e-3eb4-72d6-a6fa-adfe1df795c8" || source.activatedURL != "rist://camera.example.com:9000" {
		t.Fatalf("activated camera = %q, %q", source.activatedID, source.activatedURL)
	}
	source.resources[0].Connection = domain.CameraConnection{
		CameraIdentityID: "019c240e-3eb4-72d6-a6fa-adfe1df795c8",
		URL:              "rist://camera.example.com:9000",
		Status:           domain.CameraConnectionStatusConnected,
		VideoWorkerID:    "019c240e-5141-75e4-8b4b-5c611e7fab65",
	}
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(&connection), &connection); err != nil {
		t.Fatalf("refresh CameraConnection: %v", err)
	}
	connection.Status.CameraURL = "rist://127.0.0.1:32000"
	if err := fakeClient.Status().Update(context.Background(), &connection); err != nil {
		t.Fatalf("update reassigned CameraConnection URL: %v", err)
	}
	if err := synchronizer.Sync(context.Background()); err != nil {
		t.Fatalf("status Sync() error = %v", err)
	}
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(&connection), &connection); err != nil {
		t.Fatal(err)
	}
	if connection.Status.Phase != recordingv1alpha1.CameraConnectionPhaseConnected ||
		connection.Status.VideoWorkerID != "019c240e-5141-75e4-8b4b-5c611e7fab65" ||
		connection.Status.CameraURL != "rist://127.0.0.1:32000" ||
		source.activatedURL != "rist://127.0.0.1:32000" {
		t.Fatalf("synchronized CameraConnection status = %+v", connection.Status)
	}
}

func TestSynchronizerCompletesRequestedDeletionAfterResourceDisappears(t *testing.T) {
	scheme := testScheme(t)
	connection := testConnection()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&recordingv1alpha1.CameraConnection{}).
		WithObjects(connection).Build()
	source := &cameraSourceStub{resources: []repository.CameraResource{{
		Camera:   repository.Camera{Identity: domain.CameraIdentity{ID: domain.CameraIdentityID(connection.Spec.CameraIdentityID)}},
		Deleting: true,
	}}}
	synchronizer := Synchronizer{Client: fakeClient, Source: source, Namespace: connection.Namespace}
	if err := synchronizer.Sync(context.Background()); err != nil {
		t.Fatalf("delete Sync() error = %v", err)
	}
	if err := synchronizer.Sync(context.Background()); err != nil {
		t.Fatalf("complete Sync() error = %v", err)
	}
	if source.completedID != connection.Spec.CameraIdentityID {
		t.Fatalf("completed camera ID = %q", source.completedID)
	}
}

type cameraSourceStub struct {
	resources    []repository.CameraResource
	err          error
	activatedID  string
	activatedURL string
	completedID  string
}

func (s *cameraSourceStub) ActivateCameraConnection(_ context.Context, cameraID, cameraURL string) error {
	s.activatedID, s.activatedURL = cameraID, cameraURL
	return s.err
}

func (s *cameraSourceStub) ListCameraResources(context.Context) ([]repository.CameraResource, error) {
	return s.resources, s.err
}

func (s *cameraSourceStub) CompleteCameraDeletion(_ context.Context, cameraID string) error {
	s.completedID = cameraID
	return s.err
}

func (s *cameraSourceStub) MarkWorkerFailure(context.Context, string, string) error { return s.err }
