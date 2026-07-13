package operator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type CameraWorkloadReconciler struct {
	Client                                            client.Client
	Ingress                                           *LiveKitIngressManager
	FanoutImage, LiveKitIngressImage, PublicMediaHost string
}

var ErrWorkloadProgressing = fmt.Errorf("workload transition is still progressing")

func (reconciler *CameraWorkloadReconciler) Reconcile(ctx context.Context, session *recordingv1alpha1.Session) error {
	if reconciler.Client == nil || reconciler.Ingress == nil || reconciler.FanoutImage == "" || reconciler.LiveKitIngressImage == "" {
		return fmt.Errorf("camera workload reconciler is not configured")
	}
	for _, camera := range session.Spec.Cameras {
		if camera.DesiredState == recordingv1alpha1.DesiredStatePresent {
			if err := reconciler.ensureCamera(ctx, session, camera); err != nil {
				return err
			}
		} else {
			if err := reconciler.deleteCamera(ctx, session, camera); err != nil {
				return err
			}
		}
	}
	return nil
}

func (reconciler *CameraWorkloadReconciler) ensureCamera(ctx context.Context, session *recordingv1alpha1.Session, camera recordingv1alpha1.CameraSpec) error {
	info, err := reconciler.Ingress.Ensure(ctx, session, camera)
	if err != nil {
		return err
	}
	base := cameraWorkloadName(session.Name, camera.Name)
	labels := cameraLabels(session.Name, camera.Name)
	if err := reconciler.ensureService(ctx, session, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: base + "-input", Namespace: session.Namespace},
	}, labels, corev1.ServiceTypeNodePort, []corev1.ServicePort{
		{Name: "rist", Protocol: corev1.ProtocolUDP, Port: 10000, TargetPort: intstr.FromInt32(10000), NodePort: camera.Ingress.RISTNodePort},
		{Name: "srt", Protocol: corev1.ProtocolUDP, Port: 10001, TargetPort: intstr.FromInt32(10001), NodePort: camera.Ingress.SRTNodePort},
	}); err != nil {
		return err
	}
	if err := reconciler.ensureService(ctx, session, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: base + "-fanout", Namespace: session.Namespace}}, labels, corev1.ServiceTypeClusterIP,
		[]corev1.ServicePort{{Name: "recording", Protocol: corev1.ProtocolUDP, Port: 12000, TargetPort: intstr.FromInt32(12000)}}); err != nil {
		return err
	}
	ingressLabels := cameraLabels(session.Name, camera.Name+"-ingress")
	if err := reconciler.ensureService(ctx, session, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: base + "-ingress", Namespace: session.Namespace}}, ingressLabels, corev1.ServiceTypeClusterIP,
		[]corev1.ServicePort{{Name: "rtmp", Port: 1935, TargetPort: intstr.FromInt32(1935)}}); err != nil {
		return err
	}
	if err := reconciler.ensureDeployment(ctx, session, base+"-ingress", ingressLabels, reconciler.LiveKitIngressImage, []corev1.EnvVar{
		{Name: "RTMP_LISTEN_URL", Value: "rtmp://0.0.0.0:1935/live/" + camera.Name},
		{Name: "WHIP_URL", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: cameraWHIPSecretName(session.Name, camera.Name)}, Key: whipURLSecretKey}}},
	}, []corev1.ContainerPort{{Name: "rtmp", ContainerPort: 1935}, {Name: "status", ContainerPort: 8080}}); err != nil {
		return err
	}
	if err := reconciler.ensureDeployment(ctx, session, base+"-fanout", labels, reconciler.FanoutImage, []corev1.EnvVar{
		{Name: "RIST_PORT", Value: "10000"}, {Name: "SRT_PORT", Value: "10001"}, {Name: "RECORDING_PORT", Value: "12000"},
		{Name: "PREVIEW_RTMP_URL", Value: "rtmp://" + base + "-ingress:1935/live/" + camera.Name},
	}, []corev1.ContainerPort{{Name: "rist", ContainerPort: 10000, Protocol: corev1.ProtocolUDP}, {Name: "srt", ContainerPort: 10001, Protocol: corev1.ProtocolUDP}, {Name: "recording", ContainerPort: 12000, Protocol: corev1.ProtocolUDP}, {Name: "status", ContainerPort: 8080}}); err != nil {
		return err
	}
	status := cameraStatus(session, camera.Name)
	status.LiveKitIngressID = info.IngressId
	status.Phase = recordingv1alpha1.CameraPhaseWaiting
	status.Endpoints = cameraEndpoints(reconciler.PublicMediaHost, camera)
	return nil
}

func (reconciler *CameraWorkloadReconciler) deleteCamera(ctx context.Context, session *recordingv1alpha1.Session, camera recordingv1alpha1.CameraSpec) error {
	base := cameraWorkloadName(session.Name, camera.Name)
	// Stop the WHIP publisher before deleting the LiveKit ingress.
	stopped, err := reconciler.deleteDeploymentAndWait(ctx, session.Namespace, base+"-ingress")
	if err != nil {
		return err
	}
	if !stopped {
		cameraStatus(session, camera.Name).Phase = recordingv1alpha1.CameraPhaseDeleting
		return ErrWorkloadProgressing
	}
	if err := reconciler.Ingress.Delete(ctx, session, camera.Name); err != nil {
		if errors.Is(err, ErrLiveKitParticipantPresent) {
			return ErrWorkloadProgressing
		}
		return err
	}
	for _, object := range []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: base + "-fanout", Namespace: session.Namespace}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: base + "-input", Namespace: session.Namespace}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: base + "-fanout", Namespace: session.Namespace}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: base + "-ingress", Namespace: session.Namespace}},
	} {
		if err := reconciler.deleteObject(ctx, object); err != nil {
			return err
		}
	}
	status := cameraStatus(session, camera.Name)
	status.Phase = recordingv1alpha1.CameraPhaseRemoved
	status.LiveKitIngressID = ""
	return nil
}

func (reconciler *CameraWorkloadReconciler) ensureService(ctx context.Context, owner *recordingv1alpha1.Session, service *corev1.Service, labels map[string]string, serviceType corev1.ServiceType, ports []corev1.ServicePort) error {
	_, err := controllerutil.CreateOrUpdate(ctx, reconciler.Client, service, func() error {
		service.Labels, service.Spec.Selector, service.Spec.Type, service.Spec.Ports = labels, labels, serviceType, ports
		return controllerutil.SetControllerReference(owner, service, reconciler.Client.Scheme())
	})
	return err
}

func (reconciler *CameraWorkloadReconciler) ensureDeployment(ctx context.Context, owner *recordingv1alpha1.Session, name string, labels map[string]string, image string, environment []corev1.EnvVar, ports []corev1.ContainerPort) error {
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: owner.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, reconciler.Client, deployment, func() error {
		replicas := int32(1)
		deployment.Labels = labels
		deployment.Spec.Replicas = &replicas
		deployment.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		deployment.Spec.Template.Labels = labels
		deployment.Spec.Template.Spec.Containers = []corev1.Container{{
			Name: "media", Image: image, ImagePullPolicy: corev1.PullIfNotPresent, Env: environment, Ports: ports,
			ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt32(8080)}}, InitialDelaySeconds: 2, PeriodSeconds: 2},
		}}
		return controllerutil.SetControllerReference(owner, deployment, reconciler.Client.Scheme())
	})
	return err
}

func (reconciler *CameraWorkloadReconciler) deleteObject(ctx context.Context, object client.Object) error {
	err := reconciler.Client.Delete(ctx, object)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (reconciler *CameraWorkloadReconciler) deleteDeploymentAndWait(ctx context.Context, namespace, name string) (bool, error) {
	deployment, err := getDeployment(ctx, reconciler.Client, namespace, name)
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if deployment.DeletionTimestamp.IsZero() {
		if err := reconciler.Client.Delete(ctx, deployment, client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
	}
	return false, nil
}

func cameraWorkloadName(sessionResourceName, cameraName string) string {
	digest := sha256.Sum256([]byte(sessionResourceName + "\x00" + cameraName))
	return "camera-" + hex.EncodeToString(digest[:12])
}

func cameraLabels(sessionResourceName, cameraIdentity string) map[string]string {
	digest := sha256.Sum256([]byte(cameraIdentity))
	return map[string]string{"app.kubernetes.io/managed-by": "kinugasa-recording", "recording.kinugasa.tra.pt/session": sessionResourceName, "recording.kinugasa.tra.pt/camera": hex.EncodeToString(digest[:8])}
}

func cameraStatus(session *recordingv1alpha1.Session, name string) *recordingv1alpha1.CameraStatus {
	for index := range session.Status.Cameras {
		if session.Status.Cameras[index].Name == name {
			return &session.Status.Cameras[index]
		}
	}
	session.Status.Cameras = append(session.Status.Cameras, recordingv1alpha1.CameraStatus{Name: name})
	return &session.Status.Cameras[len(session.Status.Cameras)-1]
}

func cameraEndpoints(host string, camera recordingv1alpha1.CameraSpec) recordingv1alpha1.CameraEndpoints {
	return recordingv1alpha1.CameraEndpoints{
		RIST: "rist://" + net.JoinHostPort(host, strconv.Itoa(int(camera.Ingress.RISTNodePort))) + "?rist_profile=main",
		SRT:  "srt://" + net.JoinHostPort(host, strconv.Itoa(int(camera.Ingress.SRTNodePort))) + "?mode=caller&transtype=live",
	}
}

func getDeployment(ctx context.Context, reader client.Reader, namespace, name string) (*appsv1.Deployment, error) {
	var deployment appsv1.Deployment
	if err := reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &deployment); err != nil {
		return nil, err
	}
	return &deployment, nil
}
