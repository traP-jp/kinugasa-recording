package operator

import (
	"context"
	"errors"
	"testing"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestTakeWorkloadReconcilerRecordsStopsUploadsAndCleansUp(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	session := cameraTestSession("Session-A", "recording")
	session.Spec.Cameras = []recordingv1alpha1.CameraSpec{{Name: "front", DesiredState: recordingv1alpha1.DesiredStatePresent}}
	session.Spec.Takes = []recordingv1alpha1.TakeSpec{{Name: "take-1", DesiredState: recordingv1alpha1.DesiredStateRecording, CameraNames: []string{"front"}}}
	kubernetesClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&batchv1.Job{}).WithObjects(session).Build()
	reconciler := &TakeWorkloadReconciler{Client: kubernetesClient, RecorderImage: "recorder:test", UploaderImage: "uploader:test", S3ConfigMapName: "s3", S3SecretName: "s3-credentials"}

	if err := reconciler.Reconcile(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	var jobs batchv1.JobList
	if err := kubernetesClient.List(context.Background(), &jobs, client.InNamespace(session.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 2 {
		t.Fatalf("jobs = %d", len(jobs.Items))
	}
	for _, job := range jobs.Items {
		if job.Spec.Template.Spec.Containers[0].ImagePullPolicy != corev1.PullIfNotPresent {
			t.Fatalf("job %s imagePullPolicy = %q", job.Name, job.Spec.Template.Spec.Containers[0].ImagePullPolicy)
		}
	}
	var claims corev1.PersistentVolumeClaimList
	if err := kubernetesClient.List(context.Background(), &claims, client.InNamespace(session.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(claims.Items) != 1 {
		t.Fatalf("claims = %d", len(claims.Items))
	}
	if err := reconciler.Reconcile(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if err := kubernetesClient.List(context.Background(), &jobs, client.InNamespace(session.Namespace)); err != nil {
		t.Fatal(err)
	}
	if err := kubernetesClient.List(context.Background(), &claims, client.InNamespace(session.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 2 || len(claims.Items) != 1 {
		t.Fatalf("idempotent resources: jobs=%d claims=%d", len(jobs.Items), len(claims.Items))
	}
	base := takeResourceName(session.Name, "take-1", "front")
	recorder, err := getJob(context.Background(), kubernetesClient, session.Namespace, base+"-recorder")
	if err != nil {
		t.Fatal(err)
	}
	recorder.Status.Active = 1
	if err := kubernetesClient.Status().Update(context.Background(), recorder); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Reconcile(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status.Takes[0].Phase != recordingv1alpha1.TakePhaseStarting {
		t.Fatalf("phase with unready active Job = %q", session.Status.Takes[0].Phase)
	}
	recorder, err = getJob(context.Background(), kubernetesClient, session.Namespace, base+"-recorder")
	if err != nil {
		t.Fatal(err)
	}
	recorder.Status.Ready = ptr(int32(1))
	if err := kubernetesClient.Status().Update(context.Background(), recorder); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Reconcile(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status.Takes[0].Phase != recordingv1alpha1.TakePhaseRecording {
		t.Fatalf("phase = %q", session.Status.Takes[0].Phase)
	}

	session.Spec.Takes[0].DesiredState = recordingv1alpha1.DesiredStateStopped
	if err := reconciler.Reconcile(context.Background(), session); !errors.Is(err, ErrWorkloadProgressing) {
		t.Fatalf("stop reconcile = %v", err)
	}
	if err := reconciler.Reconcile(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status.Takes[0].Phase != recordingv1alpha1.TakePhaseUploading {
		t.Fatalf("phase = %q", session.Status.Takes[0].Phase)
	}
	uploader, err := getJob(context.Background(), kubernetesClient, session.Namespace, base+"-uploader")
	if err != nil {
		t.Fatal(err)
	}
	uploader.Status.Succeeded = 1
	if err := kubernetesClient.Status().Update(context.Background(), uploader); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Reconcile(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status.Takes[0].Phase != recordingv1alpha1.TakePhaseCompleted {
		t.Fatalf("phase = %q", session.Status.Takes[0].Phase)
	}
	if err := kubernetesClient.List(context.Background(), &claims, client.InNamespace(session.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(claims.Items) != 0 {
		t.Fatalf("claims remain = %d", len(claims.Items))
	}
}
