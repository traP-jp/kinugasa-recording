package cameraconnection

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	recordingv1alpha1 "github.com/traP-jp/kinugasa-recording/api/v1alpha1"
)

const (
	FinalizerName             = "recording.kinugasa.trap.jp/protect-uploads"
	resourcesReadyCondition   = "ResourcesReady"
	uploadsProtectedCondition = "UploadsProtected"
)

type Reconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	Config         Config
	PreviewIngress PreviewIngress
}

// +kubebuilder:rbac:groups=recording.kinugasa.trap.jp,resources=cameraconnections,verbs=create;delete;get;list;watch;patch;update
// +kubebuilder:rbac:groups=recording.kinugasa.trap.jp,resources=cameraconnections/status,verbs=get;patch;update
// +kubebuilder:rbac:groups=recording.kinugasa.trap.jp,resources=cameraconnections/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims;pods;services,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=create;get;list;patch;update;watch

func (r *Reconciler) SetupWithManager(manager ctrl.Manager) error {
	if err := r.Config.withDefaults().validate(); err != nil {
		return fmt.Errorf("validate camera connection controller config: %w", err)
	}
	if r.PreviewIngress == nil {
		return fmt.Errorf("LiveKit preview ingress is not configured")
	}
	return ctrl.NewControllerManagedBy(manager).
		For(&recordingv1alpha1.CameraConnection{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	config := r.Config.withDefaults()
	if err := config.validate(); err != nil {
		return ctrl.Result{}, fmt.Errorf("validate camera connection controller config: %w", err)
	}

	var connection recordingv1alpha1.CameraConnection
	if err := r.Get(ctx, request.NamespacedName, &connection); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !connection.DeletionTimestamp.IsZero() {
		return r.reconcileDeletion(ctx, &connection)
	}
	if !controllerutil.ContainsFinalizer(&connection, FinalizerName) {
		base := connection.DeepCopy()
		controllerutil.AddFinalizer(&connection, FinalizerName)
		if err := r.Patch(ctx, &connection, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("add upload protection finalizer: %w", err)
		}
	}

	if err := r.ensurePVC(ctx, &connection, config); err != nil {
		return r.resourceFailure(ctx, &connection, err)
	}
	if err := r.ensureService(ctx, &connection, config); err != nil {
		return r.resourceFailure(ctx, &connection, err)
	}
	if err := r.ensurePreviewSecret(ctx, &connection); err != nil {
		return r.resourceFailure(ctx, &connection, err)
	}
	if err := r.ensurePod(ctx, &connection, config); err != nil {
		return r.resourceFailure(ctx, &connection, err)
	}

	base := connection.DeepCopy()
	connection.Status.ObservedGeneration = connection.Generation
	connection.Status.WorkerPodName = connection.Name
	connection.Status.PVCName = connection.Name
	var service corev1.Service
	if err := r.Get(ctx, client.ObjectKeyFromObject(&connection), &service); err != nil {
		return r.resourceFailure(ctx, &connection, fmt.Errorf("load RIST Service status: %w", err))
	}
	if cameraURL := cameraURLForService(&service, config.RISTPort); cameraURL != "" {
		connection.Status.CameraURL = cameraURL
		connection.Status.Phase = recordingv1alpha1.CameraConnectionPhaseWaiting
	} else {
		connection.Status.CameraURL = ""
		connection.Status.Phase = recordingv1alpha1.CameraConnectionPhaseActivating
	}
	meta.SetStatusCondition(&connection.Status.Conditions, metav1.Condition{
		Type:               resourcesReadyCondition,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: connection.Generation,
		Reason:             "Provisioned",
		Message:            "gateway and worker Pod, RIST Service, preview ingress, and shared PVC are present",
	})
	if !reflect.DeepEqual(base.Status, connection.Status) {
		if err := r.Status().Patch(ctx, &connection, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("update camera connection status: %w", err)
		}
	}
	return ctrl.Result{}, nil
}

func cameraURLForService(service *corev1.Service, port int32) string {
	if len(service.Status.LoadBalancer.Ingress) == 0 {
		return ""
	}
	address := service.Status.LoadBalancer.Ingress[0].IP
	if address == "" {
		address = service.Status.LoadBalancer.Ingress[0].Hostname
	}
	if address == "" {
		return ""
	}
	return "rist://" + net.JoinHostPort(address, fmt.Sprintf("%d", port))
}

func (r *Reconciler) ensurePVC(ctx context.Context, connection *recordingv1alpha1.CameraConnection, config Config) error {
	desired := desiredPVC(connection, config)
	if err := controllerutil.SetControllerReference(connection, desired, r.Scheme); err != nil {
		return fmt.Errorf("set PVC owner: %w", err)
	}
	var existing corev1.PersistentVolumeClaim
	key := client.ObjectKeyFromObject(desired)
	if err := r.Get(ctx, key, &existing); err == nil {
		existingStorage := storageRequest(&existing)
		if existingStorage.Cmp(config.SharedVolumeSize) < 0 {
			base := existing.DeepCopy()
			existing.Spec.Resources.Requests[corev1.ResourceStorage] = config.SharedVolumeSize
			if err := r.Patch(ctx, &existing, client.MergeFrom(base)); err != nil {
				return fmt.Errorf("expand PVC: %w", err)
			}
		}
		return nil
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get PVC: %w", err)
	}
	if err := r.Create(ctx, desired); err != nil {
		return fmt.Errorf("create PVC: %w", err)
	}
	return nil
}

func (r *Reconciler) ensureService(ctx context.Context, connection *recordingv1alpha1.CameraConnection, config Config) error {
	desired := desiredService(connection, config)
	if err := controllerutil.SetControllerReference(connection, desired, r.Scheme); err != nil {
		return fmt.Errorf("set Service owner: %w", err)
	}
	var existing corev1.Service
	key := client.ObjectKeyFromObject(desired)
	if err := r.Get(ctx, key, &existing); err == nil {
		base := existing.DeepCopy()
		existing.Labels = desired.Labels
		existing.Spec.Type = desired.Spec.Type
		existing.Spec.ExternalTrafficPolicy = desired.Spec.ExternalTrafficPolicy
		existing.Spec.Selector = desired.Spec.Selector
		existing.Spec.Ports = desired.Spec.Ports
		existing.Spec.PublishNotReadyAddresses = true
		if err := r.Patch(ctx, &existing, client.MergeFrom(base)); err != nil {
			return fmt.Errorf("update RTP Service: %w", err)
		}
		return nil
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get RTP Service: %w", err)
	}
	if err := r.Create(ctx, desired); err != nil {
		return fmt.Errorf("create RTP Service: %w", err)
	}
	return nil
}

func (r *Reconciler) ensurePod(ctx context.Context, connection *recordingv1alpha1.CameraConnection, config Config) error {
	desired := desiredPod(connection, config)
	if err := controllerutil.SetControllerReference(connection, desired, r.Scheme); err != nil {
		return fmt.Errorf("set Pod owner: %w", err)
	}
	var existing corev1.Pod
	if err := r.Get(ctx, client.ObjectKeyFromObject(desired), &existing); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get worker Pod: %w", err)
	}
	if err := r.Create(ctx, desired); err != nil {
		return fmt.Errorf("create worker Pod: %w", err)
	}
	return nil
}

func (r *Reconciler) resourceFailure(
	ctx context.Context,
	connection *recordingv1alpha1.CameraConnection,
	reconcileError error,
) (ctrl.Result, error) {
	base := connection.DeepCopy()
	meta.SetStatusCondition(&connection.Status.Conditions, metav1.Condition{
		Type:               resourcesReadyCondition,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: connection.Generation,
		Reason:             "ProvisioningFailed",
		Message:            reconcileError.Error(),
	})
	statusError := r.Status().Patch(ctx, connection, client.MergeFrom(base))
	return ctrl.Result{}, errors.Join(reconcileError, statusError)
}

func (r *Reconciler) reconcileDeletion(
	ctx context.Context,
	connection *recordingv1alpha1.CameraConnection,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(connection, FinalizerName) {
		return ctrl.Result{}, nil
	}
	if err := r.releasePreview(ctx, connection); err != nil {
		return ctrl.Result{}, err
	}
	var pod corev1.Pod
	err := r.Get(ctx, client.ObjectKeyFromObject(connection), &pod)
	if err == nil && !uploaderCompleted(&pod) {
		base := connection.DeepCopy()
		meta.SetStatusCondition(&connection.Status.Conditions, metav1.Condition{
			Type:               uploadsProtectedCondition,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: connection.Generation,
			Reason:             "WaitingForUploader",
			Message:            "shared volume is retained until video-uploader exits successfully",
		})
		_ = r.Status().Patch(ctx, connection, client.MergeFrom(base))
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("get worker Pod during deletion: %w", err)
	}
	if err == nil {
		if err := r.Delete(ctx, &pod); client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, fmt.Errorf("delete completed worker Pod: %w", err)
		}
		return ctrl.Result{RequeueAfter: time.Millisecond}, nil
	}

	for _, object := range []client.Object{
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: connection.Name, Namespace: connection.Namespace}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: connection.Name, Namespace: connection.Namespace}},
	} {
		if err := r.Delete(ctx, object); client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, fmt.Errorf("delete %T: %w", object, err)
		}
	}

	base := connection.DeepCopy()
	controllerutil.RemoveFinalizer(connection, FinalizerName)
	if err := r.Patch(ctx, connection, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove upload protection finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

func uploaderCompleted(pod *corev1.Pod) bool {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == uploaderContainer {
			return status.State.Terminated != nil && status.State.Terminated.ExitCode == 0
		}
	}
	return false
}
