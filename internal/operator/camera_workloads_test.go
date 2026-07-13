package operator

import (
	"context"
	"errors"
	"testing"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCameraWorkloadReconcilerCreatesAndDeletesResourcesInOrder(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	session := cameraTestSession("Session-A", "recording")
	session.Spec.Cameras = []recordingv1alpha1.CameraSpec{{Name: "front", DesiredState: recordingv1alpha1.DesiredStatePresent, Ingress: recordingv1alpha1.CameraIngressSpec{RISTNodePort: 31000, SRTNodePort: 31001}}}
	kubernetesClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(session).Build()
	liveKit := &liveKitIngressStub{}
	manager := &LiveKitIngressManager{Client: kubernetesClient, API: liveKit, Participants: liveKit, RoomName: "kinugasa-preview"}
	reconciler := &CameraWorkloadReconciler{Client: kubernetesClient, Ingress: manager, FanoutImage: "fanout:test", LiveKitIngressImage: "ingress:test", PublicMediaHost: "192.0.2.10"}

	if err := reconciler.Reconcile(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	var deployments appsv1.DeploymentList
	if err := kubernetesClient.List(context.Background(), &deployments, client.InNamespace(session.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(deployments.Items) != 2 {
		t.Fatalf("deployments = %d", len(deployments.Items))
	}
	var services corev1.ServiceList
	if err := kubernetesClient.List(context.Background(), &services, client.InNamespace(session.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(services.Items) != 3 {
		t.Fatalf("services = %d", len(services.Items))
	}
	if len(session.Status.Cameras) != 1 || session.Status.Cameras[0].LiveKitIngressID != "ingress-1" || session.Status.Cameras[0].Endpoints.RIST != "rist://192.0.2.10:31000?rist_profile=main" {
		t.Fatalf("camera status = %#v", session.Status.Cameras)
	}

	session.Spec.Cameras[0].DesiredState = recordingv1alpha1.DesiredStateAbsent
	err := reconciler.Reconcile(context.Background(), session)
	if !errors.Is(err, ErrWorkloadProgressing) {
		t.Fatalf("first delete reconcile = %v", err)
	}
	if liveKit.deletes != 0 {
		t.Fatal("LiveKit ingress deleted before bridge deployment stopped")
	}
	if err := reconciler.Reconcile(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if liveKit.deletes != 1 {
		t.Fatalf("LiveKit deletes = %d", liveKit.deletes)
	}
	if err := kubernetesClient.List(context.Background(), &deployments, client.InNamespace(session.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(deployments.Items) != 0 {
		t.Fatalf("deployments remain: %d", len(deployments.Items))
	}
	if session.Status.Cameras[0].Phase != recordingv1alpha1.CameraPhaseRemoved {
		t.Fatalf("phase = %q", session.Status.Cameras[0].Phase)
	}
}
