package operator

import (
	"context"
	"errors"
	"testing"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type uploaderStatusStub struct {
	status UploaderStatus
}

func (stub uploaderStatusStub) Read(context.Context, string) (UploaderStatus, error) {
	return stub.status, nil
}

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
	s3Config := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "s3", Namespace: session.Namespace}, Data: map[string]string{"S3_BUCKET": "recordings"}}
	s3Secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "s3-credentials", Namespace: session.Namespace}, Data: map[string][]byte{"AWS_ACCESS_KEY_ID": []byte("old")}}
	kubernetesClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&batchv1.Job{}).WithObjects(session, s3Config, s3Secret).Build()
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
	var services corev1.ServiceList
	if err := kubernetesClient.List(context.Background(), &services, client.InNamespace(session.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(services.Items) != 1 || services.Items[0].Spec.Ports[0].Port != 8080 {
		t.Fatalf("uploader services = %#v", services.Items)
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
	reconciler.UploadStatus = uploaderStatusStub{status: UploaderStatus{Phase: "Retrying", UploadedFiles: 2}}
	if err := reconciler.Reconcile(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	cameraStatus := session.Status.Takes[0].Cameras[0]
	if cameraStatus.UploadPhase != recordingv1alpha1.UploadPhaseUploading || cameraStatus.UploadedFiles != 2 || len(cameraStatus.Conditions) != 1 || cameraStatus.Conditions[0].Reason != "Retrying" {
		t.Fatalf("retrying upload status = %#v", cameraStatus)
	}
	uploader, err := getJob(context.Background(), kubernetesClient, session.Namespace, base+"-uploader")
	if err != nil {
		t.Fatal(err)
	}
	uploader.Status.Failed = 1
	if err := kubernetesClient.Status().Update(context.Background(), uploader); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Reconcile(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	cameraStatus = session.Status.Takes[0].Cameras[0]
	if session.Status.Takes[0].Phase != recordingv1alpha1.TakePhaseRecording || cameraStatus.UploadPhase != recordingv1alpha1.UploadPhaseFailed || cameraStatus.Conditions[0].Reason != "PermanentFailure" {
		t.Fatalf("permanent upload failure status = %#v", session.Status.Takes[0])
	}
	initialConfigVersion := uploader.Annotations[uploadConfigVersionAnnotation]
	if err := kubernetesClient.Get(context.Background(), client.ObjectKeyFromObject(s3Secret), s3Secret); err != nil {
		t.Fatal(err)
	}
	s3Secret.Data["AWS_ACCESS_KEY_ID"] = []byte("new")
	if err := kubernetesClient.Update(context.Background(), s3Secret); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Reconcile(context.Background(), session); !errors.Is(err, ErrWorkloadProgressing) {
		t.Fatalf("config change retry reconcile = %v", err)
	}
	if err := reconciler.Reconcile(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	uploader, err = getJob(context.Background(), kubernetesClient, session.Namespace, base+"-uploader")
	if err != nil {
		t.Fatal(err)
	}
	if uploader.Annotations[uploadConfigVersionAnnotation] == initialConfigVersion {
		t.Fatalf("uploader config version was not updated: %q", initialConfigVersion)
	}
	reconciler.UploadStatus = uploaderStatusStub{status: UploaderStatus{Phase: "Uploading", UploadedFiles: 3}}

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
	uploader, err = getJob(context.Background(), kubernetesClient, session.Namespace, base+"-uploader")
	if err != nil {
		t.Fatal(err)
	}
	uploader.Status.Failed = 1
	if err := kubernetesClient.Status().Update(context.Background(), uploader); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Reconcile(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status.Takes[0].Cameras[0].UploadPhase != recordingv1alpha1.UploadPhaseFailed {
		t.Fatalf("stopped take upload phase = %q", session.Status.Takes[0].Cameras[0].UploadPhase)
	}
	if err := kubernetesClient.Get(context.Background(), client.ObjectKeyFromObject(s3Config), s3Config); err != nil {
		t.Fatal(err)
	}
	s3Config.Data["S3_BUCKET"] = "recovered-recordings"
	if err := kubernetesClient.Update(context.Background(), s3Config); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Reconcile(context.Background(), session); !errors.Is(err, ErrWorkloadProgressing) {
		t.Fatalf("stopped config change retry reconcile = %v", err)
	}
	if err := reconciler.Reconcile(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	uploader, err = getJob(context.Background(), kubernetesClient, session.Namespace, base+"-uploader")
	if err != nil {
		t.Fatal(err)
	}
	uploader.Status.Succeeded = 1
	if err := kubernetesClient.Status().Update(context.Background(), uploader); err != nil {
		t.Fatal(err)
	}
	uploaderPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: base + "-uploader-pod", Namespace: session.Namespace, Labels: map[string]string{"batch.kubernetes.io/job-name": base + "-uploader"}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name: "video-uploader", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Message: `{"phase":"Completed","uploadedFiles":4}`}},
		}}},
	}
	if err := kubernetesClient.Create(context.Background(), uploaderPod); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Reconcile(context.Background(), session); !errors.Is(err, ErrWorkloadProgressing) {
		t.Fatalf("uploader cleanup reconcile = %v", err)
	}
	if err := reconciler.Reconcile(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status.Takes[0].Phase != recordingv1alpha1.TakePhaseCompleted {
		t.Fatalf("phase = %q", session.Status.Takes[0].Phase)
	}
	if session.Status.Takes[0].Cameras[0].UploadedFiles != 4 {
		t.Fatalf("final uploaded files = %d", session.Status.Takes[0].Cameras[0].UploadedFiles)
	}
	if err := kubernetesClient.List(context.Background(), &claims, client.InNamespace(session.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(claims.Items) != 0 {
		t.Fatalf("claims remain = %d", len(claims.Items))
	}
	if err := kubernetesClient.List(context.Background(), &services, client.InNamespace(session.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(services.Items) != 0 {
		t.Fatalf("uploader services remain = %d", len(services.Items))
	}
}
