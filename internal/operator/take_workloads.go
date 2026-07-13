package operator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type TakeWorkloadReconciler struct {
	Client                        client.Client
	RecorderImage, UploaderImage  string
	S3ConfigMapName, S3SecretName string
	VolumeSize                    resource.Quantity
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
			cameraState.UploadPhase = recordingv1alpha1.UploadPhaseUploading
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
		if apierrors.IsNotFound(err) && cameraState.UploadPhase == recordingv1alpha1.UploadPhaseCompleted {
			continue
		}
		if err != nil {
			return err
		}
		switch {
		case uploader.Status.Succeeded > 0:
			cameraState.UploadPhase = recordingv1alpha1.UploadPhaseCompleted
			if err := reconciler.deleteObject(ctx, uploader); err != nil {
				return err
			}
			if err := reconciler.deleteObject(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: base, Namespace: session.Namespace}}); err != nil {
				return err
			}
		case uploader.Status.Failed > 0:
			cameraState.UploadPhase = recordingv1alpha1.UploadPhaseFailed
			allUploaded = false
		default:
			cameraState.UploadPhase = recordingv1alpha1.UploadPhaseUploading
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
	}, volume); err != nil {
		return err
	}
	return reconciler.ensureJob(ctx, session, base+"-uploader", corev1.Container{
		Name: "video-uploader", Image: reconciler.UploaderImage, ImagePullPolicy: corev1.PullIfNotPresent,
		Env: []corev1.EnvVar{{Name: "SESSION_NAME", Value: session.Spec.Name}, {Name: "TAKE_NAME", Value: take.Name}, {Name: "CAMERA_NAME", Value: camera.Name}},
		EnvFrom: []corev1.EnvFromSource{
			{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: reconciler.S3ConfigMapName}}},
			{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: reconciler.S3SecretName}}},
		}, VolumeMounts: []corev1.VolumeMount{mount},
	}, volume)
}

func (reconciler *TakeWorkloadReconciler) ensureJob(ctx context.Context, owner *recordingv1alpha1.Session, name string, container corev1.Container, volume corev1.Volume) error {
	var existing batchv1.Job
	err := reconciler.Client.Get(ctx, types.NamespacedName{Namespace: owner.Namespace, Name: name}, &existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	backoff := int32(0)
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: owner.Namespace}, Spec: batchv1.JobSpec{
		BackoffLimit: &backoff,
		Template:     corev1.PodTemplateSpec{Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, TerminationGracePeriodSeconds: ptr(int64(15)), Containers: []corev1.Container{container}, Volumes: []corev1.Volume{volume}}},
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
