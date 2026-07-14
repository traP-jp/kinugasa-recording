package operator

import (
	"context"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
)

type SessionWorkloadReconciler struct {
	Cameras WorkloadReconciler
	Takes   WorkloadReconciler
}

func (reconciler *SessionWorkloadReconciler) Reconcile(ctx context.Context, session *recordingv1alpha1.Session) error {
	if reconciler.Cameras != nil {
		if err := reconciler.Cameras.Reconcile(ctx, session); err != nil {
			return err
		}
	}
	if reconciler.Takes != nil {
		return reconciler.Takes.Reconcile(ctx, session)
	}
	return nil
}
