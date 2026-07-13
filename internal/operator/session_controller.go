package operator

import (
	"context"
	"fmt"
	"time"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const defaultDependencyRetryInterval = 5 * time.Second

// Dependency reports whether an external dependency is currently usable.
type Dependency interface {
	Name() string
	Check(context.Context) error
}

// WorkloadReconciler applies the workloads implied by a Session desired state.
type WorkloadReconciler interface {
	Reconcile(context.Context, *recordingv1alpha1.Session) error
}

// SessionReconciler observes Session resources and records their current state.
type SessionReconciler struct {
	client.Client
	Recorder                events.EventRecorder
	Dependencies            []Dependency
	Workloads               WorkloadReconciler
	DependencyRetryInterval time.Duration
}

// Reconcile implements the controller-runtime reconciliation loop.
func (r *SessionReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	var session recordingv1alpha1.Session
	if err := r.Get(ctx, request.NamespacedName, &session); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	before := session.DeepCopy()
	session.Status.ObservedGeneration = session.Generation

	for _, dependency := range r.Dependencies {
		if err := dependency.Check(ctx); err != nil {
			message := fmt.Sprintf("%s is unavailable: %v", dependency.Name(), err)
			r.setDegraded(&session, "DependencyUnavailable", message)
			r.warning(&session, "DependencyUnavailable", message)

			if patchErr := r.Status().Patch(ctx, &session, client.MergeFrom(before)); patchErr != nil {
				return ctrl.Result{}, patchErr
			}

			return ctrl.Result{RequeueAfter: r.retryInterval()}, nil
		}
	}

	if r.Workloads != nil {
		if err := r.Workloads.Reconcile(ctx, &session); err != nil {
			message := fmt.Sprintf("failed to reconcile workloads: %v", err)
			r.setDegraded(&session, "WorkloadReconcileFailed", message)
			r.warning(&session, "WorkloadReconcileFailed", message)

			if patchErr := r.Status().Patch(ctx, &session, client.MergeFrom(before)); patchErr != nil {
				return ctrl.Result{}, patchErr
			}

			return ctrl.Result{RequeueAfter: r.retryInterval()}, nil
		}
	}

	session.Status.Phase = desiredSessionPhase(session.Spec.Takes)
	meta.SetStatusCondition(&session.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "ComponentsReady",
		Message:            "desired state has been reconciled",
		ObservedGeneration: session.Generation,
	})

	if err := r.Status().Patch(ctx, &session, client.MergeFrom(before)); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager registers the Session controller.
func (r *SessionReconciler) SetupWithManager(manager ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(manager).
		For(&recordingv1alpha1.Session{}).
		Complete(r)
}

func (r *SessionReconciler) setDegraded(session *recordingv1alpha1.Session, reason, message string) {
	session.Status.Phase = recordingv1alpha1.SessionPhaseDegraded
	meta.SetStatusCondition(&session.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: session.Generation,
	})
}

func (r *SessionReconciler) warning(session *recordingv1alpha1.Session, reason, message string) {
	if r.Recorder != nil {
		r.Recorder.Eventf(session, nil, "Warning", reason, "ReconcileSession", "%s", message)
	}
}

func (r *SessionReconciler) retryInterval() time.Duration {
	if r.DependencyRetryInterval > 0 {
		return r.DependencyRetryInterval
	}

	return defaultDependencyRetryInterval
}

func desiredSessionPhase(takes []recordingv1alpha1.TakeSpec) recordingv1alpha1.SessionPhase {
	for _, take := range takes {
		if take.DesiredState == recordingv1alpha1.DesiredStateRecording {
			return recordingv1alpha1.SessionPhaseRecording
		}
	}

	return recordingv1alpha1.SessionPhaseReady
}
