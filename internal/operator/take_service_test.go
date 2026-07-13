package operator

import (
	"context"
	"errors"
	"testing"
	"time"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestTakeServiceStartsWithConnectedCamerasAndReportsExclusions(t *testing.T) {
	t.Parallel()
	scheme := cameraTestScheme(t)
	session := cameraTestSession("Session-A", "recording")
	session.Spec.Cameras = []recordingv1alpha1.CameraSpec{
		{Name: "front", DesiredState: recordingv1alpha1.DesiredStatePresent},
		{Name: "side", DesiredState: recordingv1alpha1.DesiredStatePresent},
	}
	session.Status.Cameras = []recordingv1alpha1.CameraStatus{{Name: "front", Phase: recordingv1alpha1.CameraPhaseConnected}, {Name: "side", Phase: recordingv1alpha1.CameraPhaseDisconnected}}
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&recordingv1alpha1.Session{}).WithObjects(session).Build()
	now := time.Date(2026, 7, 14, 1, 0, 0, 0, time.UTC)
	service := &TakeService{Client: client, Namespace: "recording", Now: func() time.Time { return now }}
	result, err := service.Start(context.Background(), "Session-A", "take-1", []string{"front", "side", "missing"}, "start-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Take.CameraNames) != 1 || result.Take.CameraNames[0] != "front" {
		t.Fatalf("selected = %#v", result.Take.CameraNames)
	}
	if len(result.ExcludedCameras) != 2 || result.ExcludedCameras[0].Reason != "CAMERA_DISCONNECTED" || result.ExcludedCameras[1].Reason != "CAMERA_NOT_FOUND" {
		t.Fatalf("excluded = %#v", result.ExcludedCameras)
	}
	if !result.Take.RequestedAt.Time.Equal(now) {
		t.Fatalf("requestedAt = %v", result.Take.RequestedAt)
	}
	var updated recordingv1alpha1.Session
	if err := client.Get(context.Background(), types.NamespacedName{Namespace: session.Namespace, Name: session.Name}, &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.Spec.ReservedTakeNames) != 1 || len(updated.Spec.Takes) != 1 {
		t.Fatalf("spec = %#v", updated.Spec)
	}
	replayed, err := service.Start(context.Background(), "Session-A", "take-1", []string{"front", "side", "missing"}, "start-1")
	if err != nil || replayed.Take.Name != "take-1" || len(replayed.ExcludedCameras) != 2 {
		t.Fatalf("replay = %#v, %v", replayed, err)
	}
}

func TestTakeServiceUsesAllConnectedCamerasWhenSelectionOmitted(t *testing.T) {
	t.Parallel()
	scheme := cameraTestScheme(t)
	session := cameraTestSession("Session-A", "recording")
	session.Spec.Cameras = []recordingv1alpha1.CameraSpec{{Name: "front", DesiredState: recordingv1alpha1.DesiredStatePresent}, {Name: "side", DesiredState: recordingv1alpha1.DesiredStatePresent}}
	session.Status.Cameras = []recordingv1alpha1.CameraStatus{{Name: "front", Phase: recordingv1alpha1.CameraPhaseConnected}, {Name: "side", Phase: recordingv1alpha1.CameraPhaseConnected}}
	service := &TakeService{Client: fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&recordingv1alpha1.Session{}).WithObjects(session).Build(), Namespace: "recording"}
	result, err := service.Start(context.Background(), "Session-A", "take-1", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Take.CameraNames) != 2 {
		t.Fatalf("camera names = %#v", result.Take.CameraNames)
	}
}

func TestTakeServiceRejectsNoAvailableCameraAndReservedName(t *testing.T) {
	t.Parallel()
	scheme := cameraTestScheme(t)
	session := cameraTestSession("Session-A", "recording")
	session.Spec.Cameras = []recordingv1alpha1.CameraSpec{{Name: "front", DesiredState: recordingv1alpha1.DesiredStatePresent}}
	session.Spec.ReservedTakeNames = []string{"old-take"}
	service := &TakeService{Client: fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&recordingv1alpha1.Session{}).WithObjects(session).Build(), Namespace: "recording"}
	if _, err := service.Start(context.Background(), "Session-A", "take-1", nil, ""); !errors.Is(err, ErrNoAvailableCamera) {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := service.Start(context.Background(), "Session-A", "old-take", nil, ""); !errors.Is(err, ErrTakeNameReserved) {
		t.Fatalf("reserved Start() error = %v", err)
	}
}

func TestTakeServiceStopsIdempotently(t *testing.T) {
	t.Parallel()
	scheme := cameraTestScheme(t)
	session := cameraTestSession("Session-A", "recording")
	session.Spec.ReservedTakeNames = []string{"take-1"}
	session.Spec.Takes = []recordingv1alpha1.TakeSpec{{Name: "take-1", DesiredState: recordingv1alpha1.DesiredStateRecording}}
	service := &TakeService{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(session).Build(), Namespace: "recording"}
	stopped, err := service.Stop(context.Background(), "Session-A", "take-1", "stop-1")
	if err != nil {
		t.Fatal(err)
	}
	if stopped.DesiredState != recordingv1alpha1.DesiredStateStopped || stopped.StopRequestedAt == nil {
		t.Fatalf("stopped = %#v", stopped)
	}
	if _, err := service.Stop(context.Background(), "Session-A", "take-1", "stop-1"); err != nil {
		t.Fatal(err)
	}
}
