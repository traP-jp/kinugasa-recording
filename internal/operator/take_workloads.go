package operator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const uploadConfigVersionAnnotation = "recording.kinugasa.tra.pt/upload-config-version"

type TakeWorkloadReconciler struct {
	Client                        client.Client
	RecorderImage, UploaderImage  string
	S3ConfigMapName, S3SecretName string
	VolumeSize                    resource.Quantity
	UploadStatus                  UploaderStatusReader
}

func (reconciler *TakeWorkloadReconciler) Reconcile(ctx context.Context, session *recordingv1alpha1.Session) error {
	if reconciler.Client == nil || reconciler.RecorderImage == "" || reconciler.UploaderImage == "" || reconciler.S3ConfigMapName == "" || reconciler.S3SecretName == "" {
		return fmt.Errorf("take workload reconciler is not configured")
	}
	for _, take := range session.Spec.Takes {
		if err := reconciler.reconcileTake(ctx, session, take); err != nil {
			return err
		}
	}
	return nil
}

func (reconciler *TakeWorkloadReconciler) reconcileTake(ctx context.Context, session *recordingv1alpha1.Session, take recordingv1alpha1.TakeSpec) error {
	status := takeStatus(session, take.Name)
	if take.DesiredState == recordingv1alpha1.DesiredStateRecording {
		allRunning := true
		for _, cameraName := range take.CameraNames {
			camera, found := findCamera(session.Spec.Cameras, cameraName)
			if !found {
				return fmt.Errorf("take %s references missing camera %s", take.Name, cameraName)
			}
			if err := reconciler.ensureRecordingResources(ctx, session, take, *camera); err != nil {
				return err
			}
			cameraState := takeCameraStatus(status, cameraName)
			recorder, err := getJob(ctx, reconciler.Client, session.Namespace, takeResourceName(session.Name, take.Name, cameraName)+"-recorder")
			if err != nil {
				return err
			}
			switch {
			case recorder.Status.Failed > 0:
				cameraState.RecorderPhase = recordingv1alpha1.ProcessPhaseFailed
			case recorder.Status.Ready != nil && *recorder.Status.Ready > 0:
				cameraState.RecorderPhase = recordingv1alpha1.ProcessPhaseRunning
			default:
				cameraState.RecorderPhase = recordingv1alpha1.ProcessPhasePending
				allRunning = false
			}
			uploader, err := getJob(ctx, reconciler.Client, session.Namespace, takeResourceName(session.Name, take.Name, cameraName)+"-uploader")
			if err != nil {
				return err
			}
			if uploader.Status.Failed > 0 {
				retrying, err := reconciler.retryUploaderAfterConfigChange(ctx, session.Namespace, uploader)
				if err != nil {
					return err
				}
				if retrying {
					return ErrWorkloadProgressing
				}
			}
			reconciler.updateUploadStatus(ctx, session, take.Name, cameraState, uploader)
		}
		if allRunning {
			status.Phase = recordingv1alpha1.TakePhaseRecording
		} else {
			status.Phase = recordingv1alpha1.TakePhaseStarting
		}
		return nil
	}

	status.Phase = recordingv1alpha1.TakePhaseStopping
	for _, cameraName := range take.CameraNames {
		base := takeResourceName(session.Name, take.Name, cameraName)
		stopped, err := reconciler.deleteJobAndWait(ctx, session.Namespace, base+"-recorder")
		if err != nil {
			return err
		}
		if !stopped {
			return ErrWorkloadProgressing
		}
	}
	status.Phase = recordingv1alpha1.TakePhaseUploading
	allUploaded := true
	for _, cameraName := range take.CameraNames {
		base := takeResourceName(session.Name, take.Name, cameraName)
		cameraState := takeCameraStatus(status, cameraName)
		cameraState.RecorderPhase = recordingv1alpha1.ProcessPhaseStopped
		uploader, err := getJob(ctx, reconciler.Client, session.Namespace, base+"-uploader")
		if apierrors.IsNotFound(err) && cameraState.UploadPhase == recordingv1alpha1.UploadPhaseFailed {
			camera, found := findCamera(session.Spec.Cameras, cameraName)
			if !found {
				return fmt.Errorf("take %s references missing camera %s", take.Name, cameraName)
			}
			if err := reconciler.ensureUploadResources(ctx, session, take, *camera); err != nil {
				return err
			}
			allUploaded = false
			continue
		}
		if apierrors.IsNotFound(err) && cameraState.UploadPhase == recordingv1alpha1.UploadPhaseCompleted {
			if err := reconciler.deleteObject(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: base + "-uploader", Namespace: session.Namespace}}); err != nil {
				return err
			}
			if err := reconciler.deleteObject(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: base, Namespace: session.Namespace}}); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if uploader.Status.Failed > 0 {
			retrying, err := reconciler.retryUploaderAfterConfigChange(ctx, session.Namespace, uploader)
			if err != nil {
				return err
			}
			if retrying {
				return ErrWorkloadProgressing
			}
		}
		switch {
		case uploader.Status.Succeeded > 0:
			cameraState.UploadPhase = recordingv1alpha1.UploadPhaseCompleted
			if uploadedFiles, found, err := reconciler.completedUploadCount(ctx, session.Namespace, uploader.Name); err != nil {
				return err
			} else if found {
				cameraState.UploadedFiles = uploadedFiles
			}
			deleted, err := reconciler.deleteJobAndWait(ctx, session.Namespace, uploader.Name)
			if err != nil {
				return err
			}
			if !deleted {
				return ErrWorkloadProgressing
			}
		case uploader.Status.Failed > 0:
			cameraState.UploadPhase = recordingv1alpha1.UploadPhaseFailed
			allUploaded = false
		default:
			reconciler.updateUploadStatus(ctx, session, take.Name, cameraState, uploader)
			allUploaded = false
		}
	}
	if allUploaded {
		status.Phase = recordingv1alpha1.TakePhaseCompleted
	}
	return nil
}

func (reconciler *TakeWorkloadReconciler) ensureRecordingResources(ctx context.Context, session *recordingv1alpha1.Session, take recordingv1alpha1.TakeSpec, camera recordingv1alpha1.CameraSpec) error {
	base := takeResourceName(session.Name, take.Name, camera.Name)
	claim := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: base, Namespace: session.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, reconciler.Client, claim, func() error {
		size := reconciler.VolumeSize
		if size.IsZero() {
			size = resource.MustParse("20Gi")
		}
		claim.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
		claim.Spec.Resources.Requests = corev1.ResourceList{corev1.ResourceStorage: size}
		return controllerutil.SetControllerReference(session, claim, reconciler.Client.Scheme())
	})
	if err != nil {
		return err
	}
	volume := corev1.Volume{Name: "recording", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: base}}}
	mount := corev1.VolumeMount{Name: "recording", MountPath: "/recording"}
	if err := reconciler.ensureJob(ctx, session, base+"-recorder", corev1.Container{
		Name: "video-recorder", Image: reconciler.RecorderImage, ImagePullPolicy: corev1.PullIfNotPresent,
		Env:          []corev1.EnvVar{{Name: "INPUT_URL", Value: "srt://" + cameraWorkloadName(session.Name, camera.Name) + "-fanout:12000?mode=caller&transtype=live"}},
		VolumeMounts: []corev1.VolumeMount{mount},
	}, volume, nil); err != nil {
		return err
	}
	return reconciler.ensureUploadResources(ctx, session, take, camera)
}

func (reconciler *TakeWorkloadReconciler) ensureUploadResources(ctx context.Context, session *recordingv1alpha1.Session, take recordingv1alpha1.TakeSpec, camera recordingv1alpha1.CameraSpec) error {
	base := takeResourceName(session.Name, take.Name, camera.Name)
	volume := corev1.Volume{Name: "recording", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: base}}}
	mount := corev1.VolumeMount{Name: "recording", MountPath: "/recording"}
	configVersion, err := reconciler.uploadConfigVersion(ctx, session.Namespace)
	if err != nil {
		return err
	}
	annotations := map[string]string{uploadConfigVersionAnnotation: configVersion}
	if err := reconciler.ensureJob(ctx, session, base+"-uploader", corev1.Container{
		Name: "video-uploader", Image: reconciler.UploaderImage, ImagePullPolicy: corev1.PullIfNotPresent,
		TerminationMessagePath: "/dev/termination-log", TerminationMessagePolicy: corev1.TerminationMessageReadFile,
		Env: []corev1.EnvVar{{Name: "SESSION_NAME", Value: session.Spec.Name}, {Name: "TAKE_NAME", Value: take.Name}, {Name: "CAMERA_NAME", Value: camera.Name}},
		EnvFrom: []corev1.EnvFromSource{
			{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: reconciler.S3ConfigMapName}}},
			{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: reconciler.S3SecretName}}},
		}, VolumeMounts: []corev1.VolumeMount{mount}, Ports: []corev1.ContainerPort{{Name: "status", ContainerPort: 8080}},
	}, volume, annotations); err != nil {
		return err
	}
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: base + "-uploader", Namespace: session.Namespace}}
	_, err = controllerutil.CreateOrUpdate(ctx, reconciler.Client, service, func() error {
		service.Spec.Selector = map[string]string{"batch.kubernetes.io/job-name": base + "-uploader"}
		service.Spec.Ports = []corev1.ServicePort{{Name: "status", Port: 8080, TargetPort: intstr.FromString("status")}}
		return controllerutil.SetControllerReference(session, service, reconciler.Client.Scheme())
	})
	return err
}

func (reconciler *TakeWorkloadReconciler) uploadConfigVersion(ctx context.Context, namespace string) (string, error) {
	var config corev1.ConfigMap
	if err := reconciler.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: reconciler.S3ConfigMapName}, &config); err != nil {
		return "", err
	}
	var secret corev1.Secret
	if err := reconciler.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: reconciler.S3SecretName}, &secret); err != nil {
		return "", err
	}
	return config.ResourceVersion + "/" + secret.ResourceVersion, nil
}

func (reconciler *TakeWorkloadReconciler) retryUploaderAfterConfigChange(ctx context.Context, namespace string, job *batchv1.Job) (bool, error) {
	configVersion, err := reconciler.uploadConfigVersion(ctx, namespace)
	if err != nil {
		return false, err
	}
	if job.Annotations[uploadConfigVersionAnnotation] == configVersion {
		return false, nil
	}
	_, err = reconciler.deleteJobAndWait(ctx, namespace, job.Name)
	return true, err
}

func (reconciler *TakeWorkloadReconciler) completedUploadCount(ctx context.Context, namespace, jobName string) (int32, bool, error) {
	var pods corev1.PodList
	if err := reconciler.Client.List(ctx, &pods, client.InNamespace(namespace), client.MatchingLabels{"batch.kubernetes.io/job-name": jobName}); err != nil {
		return 0, false, err
	}
	for _, pod := range pods.Items {
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name != "video-uploader" || status.State.Terminated == nil || status.State.Terminated.Message == "" {
				continue
			}
			var summary struct {
				Phase         string `json:"phase"`
				UploadedFiles int32  `json:"uploadedFiles"`
			}
			if err := json.Unmarshal([]byte(status.State.Terminated.Message), &summary); err != nil || summary.Phase != "Completed" || summary.UploadedFiles < 0 {
				continue
			}
			return summary.UploadedFiles, true, nil
		}
	}
	return 0, false, nil
}

func (reconciler *TakeWorkloadReconciler) updateUploadStatus(ctx context.Context, session *recordingv1alpha1.Session, takeName string, camera *recordingv1alpha1.TakeCameraStatus, job *batchv1.Job) {
	if job.Status.Failed > 0 {
		camera.UploadPhase = recordingv1alpha1.UploadPhaseFailed
		setUploadCondition(session, camera, metav1.ConditionFalse, "PermanentFailure", "Upload failed and requires operator action.")
		return
	}
	camera.UploadPhase = recordingv1alpha1.UploadPhaseUploading
	if reconciler.UploadStatus == nil {
		return
	}
	base := takeResourceName(session.Name, takeName, camera.Name)
	status, err := reconciler.UploadStatus.Read(ctx, fmt.Sprintf("http://%s-uploader.%s.svc:8080/status", base, session.Namespace))
	if err != nil {
		return
	}
	camera.UploadedFiles = status.UploadedFiles
	switch status.Phase {
	case "Retrying":
		setUploadCondition(session, camera, metav1.ConditionFalse, "Retrying", "Upload is retrying after an object storage error.")
	case "Failed":
		camera.UploadPhase = recordingv1alpha1.UploadPhaseFailed
		setUploadCondition(session, camera, metav1.ConditionFalse, "PermanentFailure", "Upload failed and requires operator action.")
	default:
		setUploadCondition(session, camera, metav1.ConditionTrue, "Uploading", "Upload is operating normally.")
	}
}

func setUploadCondition(session *recordingv1alpha1.Session, camera *recordingv1alpha1.TakeCameraStatus, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&camera.Conditions, metav1.Condition{Type: "UploadHealthy", Status: status, Reason: reason, Message: message, ObservedGeneration: session.Generation})
}

func (reconciler *TakeWorkloadReconciler) ensureJob(ctx context.Context, owner *recordingv1alpha1.Session, name string, container corev1.Container, volume corev1.Volume, annotations map[string]string) error {
	var existing batchv1.Job
	err := reconciler.Client.Get(ctx, types.NamespacedName{Namespace: owner.Namespace, Name: name}, &existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	backoff := int32(0)
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: owner.Namespace, Annotations: annotations}, Spec: batchv1.JobSpec{
		BackoffLimit: &backoff,
		Template:     corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Annotations: annotations}, Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, TerminationGracePeriodSeconds: ptr(int64(15)), Containers: []corev1.Container{container}, Volumes: []corev1.Volume{volume}}},
	}}
	if err := controllerutil.SetControllerReference(owner, job, reconciler.Client.Scheme()); err != nil {
		return err
	}
	return reconciler.Client.Create(ctx, job)
}

func (reconciler *TakeWorkloadReconciler) deleteJobAndWait(ctx context.Context, namespace, name string) (bool, error) {
	job, err := getJob(ctx, reconciler.Client, namespace, name)
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if job.DeletionTimestamp.IsZero() {
		if err := reconciler.Client.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
	}
	return false, nil
}
func (reconciler *TakeWorkloadReconciler) deleteObject(ctx context.Context, object client.Object) error {
	err := reconciler.Client.Delete(ctx, object)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
func getJob(ctx context.Context, reader client.Reader, namespace, name string) (*batchv1.Job, error) {
	var job batchv1.Job
	if err := reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &job); err != nil {
		return nil, err
	}
	return &job, nil
}
func takeResourceName(sessionResource, takeName, cameraName string) string {
	digest := sha256.Sum256([]byte(sessionResource + "\x00" + takeName + "\x00" + cameraName))
	return "take-" + hex.EncodeToString(digest[:12])
}
func takeStatus(session *recordingv1alpha1.Session, name string) *recordingv1alpha1.TakeStatus {
	for index := range session.Status.Takes {
		if session.Status.Takes[index].Name == name {
			return &session.Status.Takes[index]
		}
	}
	session.Status.Takes = append(session.Status.Takes, recordingv1alpha1.TakeStatus{Name: name})
	return &session.Status.Takes[len(session.Status.Takes)-1]
}
func takeCameraStatus(take *recordingv1alpha1.TakeStatus, name string) *recordingv1alpha1.TakeCameraStatus {
	for index := range take.Cameras {
		if take.Cameras[index].Name == name {
			return &take.Cameras[index]
		}
	}
	take.Cameras = append(take.Cameras, recordingv1alpha1.TakeCameraStatus{Name: name})
	return &take.Cameras[len(take.Cameras)-1]
}
func ptr[T any](value T) *T { return &value }
