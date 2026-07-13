package operator

import (
	"context"
	"errors"
	"testing"
	"time"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSessionReconcilerRecordsReadyState(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	session := testSession()
	workloads := &workloadStub{}
	kubernetesClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&recordingv1alpha1.Session{}).
		WithObjects(session).
		Build()
	reconciler := &SessionReconciler{Client: kubernetesClient, Workloads: workloads}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{
		Namespace: session.Namespace,
		Name:      session.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile() returned %v", err)
	}
	if result.RequeueAfter != defaultDependencyRetryInterval {
		t.Fatalf("RequeueAfter = %v, want %v", result.RequeueAfter, defaultDependencyRetryInterval)
	}
	if !workloads.called {
		t.Fatal("Reconcile() did not invoke the workload reconciler")
	}

	var updated recordingv1alpha1.Session
	if err := kubernetesClient.Get(context.Background(), types.NamespacedName{Namespace: session.Namespace, Name: session.Name}, &updated); err != nil {
		t.Fatalf("get reconciled Session: %v", err)
	}
	if updated.Status.Phase != recordingv1alpha1.SessionPhaseRecording {
		t.Fatalf("phase = %q, want %q", updated.Status.Phase, recordingv1alpha1.SessionPhaseRecording)
	}
	if updated.Status.ObservedGeneration != session.Generation {
		t.Fatalf("observedGeneration = %d, want %d", updated.Status.ObservedGeneration, session.Generation)
	}
	if len(updated.Status.Conditions) != 1 || updated.Status.Conditions[0].Status != metav1.ConditionTrue {
		t.Fatalf("conditions = %#v, want one true condition", updated.Status.Conditions)
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: session.Namespace, Name: session.Name}}); err != nil {
		t.Fatalf("second Reconcile() returned %v", err)
	}
	if err := kubernetesClient.Get(context.Background(), types.NamespacedName{Namespace: session.Namespace, Name: session.Name}, &updated); err != nil {
		t.Fatalf("get twice-reconciled Session: %v", err)
	}
	if len(updated.Status.Conditions) != 1 {
		t.Fatalf("idempotent reconcile conditions = %#v", updated.Status.Conditions)
	}
}

func TestSessionReconcilerRequeuesDependencyFailure(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	session := testSession()
	kubernetesClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&recordingv1alpha1.Session{}).
		WithObjects(session).
		Build()
	reconciler := &SessionReconciler{
		Client:                  kubernetesClient,
		Dependencies:            []Dependency{dependencyStub{err: errors.New("temporary failure")}},
		DependencyRetryInterval: time.Second,
	}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{
		Namespace: session.Namespace,
		Name:      session.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile() returned %v", err)
	}
	if result.RequeueAfter != time.Second {
		t.Fatalf("RequeueAfter = %v, want %v", result.RequeueAfter, time.Second)
	}

	var updated recordingv1alpha1.Session
	if err := kubernetesClient.Get(context.Background(), types.NamespacedName{Namespace: session.Namespace, Name: session.Name}, &updated); err != nil {
		t.Fatalf("get reconciled Session: %v", err)
	}
	if updated.Status.Phase != recordingv1alpha1.SessionPhaseDegraded {
		t.Fatalf("phase = %q, want %q", updated.Status.Phase, recordingv1alpha1.SessionPhaseDegraded)
	}
	if len(updated.Status.Conditions) != 1 || updated.Status.Conditions[0].Reason != "DependencyUnavailable" {
		t.Fatalf("conditions = %#v, want DependencyUnavailable", updated.Status.Conditions)
	}
}

func TestSessionReconcilerRequeuesExpectedWorkloadTransitionWithoutDegrading(t *testing.T) {
	t.Parallel()
	scheme := testScheme(t)
	session := testSession()
	kubernetesClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&recordingv1alpha1.Session{}).WithObjects(session).Build()
	reconciler := &SessionReconciler{Client: kubernetesClient, Workloads: &workloadStub{err: ErrWorkloadProgressing}, DependencyRetryInterval: time.Second}
	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: session.Namespace, Name: session.Name}})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter != time.Second {
		t.Fatalf("RequeueAfter = %v", result.RequeueAfter)
	}
	var updated recordingv1alpha1.Session
	if err := kubernetesClient.Get(context.Background(), types.NamespacedName{Namespace: session.Namespace, Name: session.Name}, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Phase == recordingv1alpha1.SessionPhaseDegraded {
		t.Fatal("expected transition marked Session degraded")
	}
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add recording API to scheme: %v", err)
	}

	return scheme
}

func testSession() *recordingv1alpha1.Session {
	return &recordingv1alpha1.Session{
		ObjectMeta: metav1.ObjectMeta{Name: "session-hash", Namespace: "recording", Generation: 3},
		Spec: recordingv1alpha1.SessionSpec{
			Name:  "Session-1",
			Takes: []recordingv1alpha1.TakeSpec{{Name: "take-1", DesiredState: recordingv1alpha1.DesiredStateRecording}},
		},
	}
}

type workloadStub struct {
	called bool
	err    error
}

func (stub *workloadStub) Reconcile(_ context.Context, _ *recordingv1alpha1.Session) error {
	stub.called = true
	return stub.err
}

type dependencyStub struct {
	err error
}

func (dependencyStub) Name() string {
	return "test dependency"
}

func (stub dependencyStub) Check(context.Context) error {
	return stub.err
}
