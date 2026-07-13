package operator

import (
	"context"
	"errors"
	"testing"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCameraServiceAddsCameraWithClusterUniquePorts(t *testing.T) {
	t.Parallel()
	scheme := cameraTestScheme(t)
	target := cameraTestSession("Session-A", "recording")
	other := cameraTestSession("Other", "another-namespace")
	other.Spec.Cameras = []recordingv1alpha1.CameraSpec{{
		Name: "used", DesiredState: recordingv1alpha1.DesiredStatePresent,
		Ingress: recordingv1alpha1.CameraIngressSpec{RISTNodePort: 31000, SRTNodePort: 31001},
	}}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(target, other).Build()
	service := &CameraService{Client: client, Namespace: "recording", PublicMediaHost: "192.0.2.10", NodePortMin: 31000, NodePortMax: 31005}

	result, err := service.Add(context.Background(), "Session-A", "front", "request-1")
	if err != nil {
		t.Fatalf("Add() returned %v", err)
	}
	if result.Camera.Ingress.RISTNodePort != 31002 || result.Camera.Ingress.SRTNodePort != 31003 {
		t.Fatalf("ports = %#v", result.Camera.Ingress)
	}
	if result.ConnectionURLs.RIST != "rist://192.0.2.10:31002" || result.ConnectionURLs.SRT != "srt://192.0.2.10:31003?mode=caller&transtype=live" {
		t.Fatalf("connection URLs = %#v", result.ConnectionURLs)
	}

	var updated recordingv1alpha1.Session
	if err := client.Get(context.Background(), types.NamespacedName{Namespace: target.Namespace, Name: target.Name}, &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.Spec.Cameras) != 1 || len(updated.Spec.ReservedCameraNames) != 1 || updated.Spec.ReservedCameraNames[0] != "front" {
		t.Fatalf("updated spec = %#v", updated.Spec)
	}
	replayed, err := service.Add(context.Background(), "Session-A", "front", "request-1")
	if err != nil || replayed.Camera.Ingress != result.Camera.Ingress {
		t.Fatalf("replayed Add() = %#v, %v", replayed, err)
	}
	if _, err := service.Add(context.Background(), "Session-A", "front", "request-2"); !errors.Is(err, ErrCameraNameReserved) {
		t.Fatalf("duplicate Add() error = %v", err)
	}
}

func TestCameraServiceRejectsMutationWhileRecording(t *testing.T) {
	t.Parallel()
	scheme := cameraTestScheme(t)
	session := cameraTestSession("Session-A", "recording")
	session.Spec.Takes = []recordingv1alpha1.TakeSpec{{Name: "take", DesiredState: recordingv1alpha1.DesiredStateRecording}}
	service := &CameraService{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(session).Build(), Namespace: "recording", PublicMediaHost: "host"}
	if _, err := service.Add(context.Background(), "Session-A", "front", ""); !errors.Is(err, ErrTakeRecording) {
		t.Fatalf("Add() error = %v", err)
	}
}

func TestCameraServiceDeletesCameraAndPreservesReservation(t *testing.T) {
	t.Parallel()
	scheme := cameraTestScheme(t)
	session := cameraTestSession("Session-A", "recording")
	session.Spec.ReservedCameraNames = []string{"front"}
	session.Spec.Cameras = []recordingv1alpha1.CameraSpec{{Name: "front", DesiredState: recordingv1alpha1.DesiredStatePresent, Ingress: recordingv1alpha1.CameraIngressSpec{RISTNodePort: 31000, SRTNodePort: 31001}}}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(session).Build()
	service := &CameraService{Client: client, Namespace: "recording"}
	deleted, err := service.Delete(context.Background(), "Session-A", "front", "delete-1")
	if err != nil {
		t.Fatal(err)
	}
	if deleted.DesiredState != recordingv1alpha1.DesiredStateAbsent {
		t.Fatalf("desired state = %q", deleted.DesiredState)
	}
	var updated recordingv1alpha1.Session
	if err := client.Get(context.Background(), types.NamespacedName{Namespace: session.Namespace, Name: session.Name}, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Spec.Cameras[0].DesiredState != recordingv1alpha1.DesiredStateAbsent || len(updated.Spec.ReservedCameraNames) != 1 {
		t.Fatalf("updated spec = %#v", updated.Spec)
	}
}

func cameraTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func cameraTestSession(name, namespace string) *recordingv1alpha1.Session {
	return &recordingv1alpha1.Session{ObjectMeta: metav1.ObjectMeta{Name: SessionResourceName(name), Namespace: namespace}, Spec: recordingv1alpha1.SessionSpec{Name: name}}
}
