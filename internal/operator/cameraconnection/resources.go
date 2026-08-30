package cameraconnection

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	recordingv1alpha1 "github.com/traP-jp/kinugasa-recording/api/v1alpha1"
)

const (
	sharedVolumeName  = "recordings"
	workerContainer   = "video-worker"
	uploaderContainer = "video-uploader"
)

func labelsFor(connection *recordingv1alpha1.CameraConnection) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":                "kinugasa-recording",
		"app.kubernetes.io/component":           "video-worker",
		"recording.kinugasa.trap.jp/connection": connection.Name,
		"recording.kinugasa.trap.jp/session":    connection.Spec.SessionName,
		"recording.kinugasa.trap.jp/camera":     connection.Spec.CameraName,
	}
}

func desiredPVC(connection *recordingv1alpha1.CameraConnection, config Config) *corev1.PersistentVolumeClaim {
	storageClassName := &config.StorageClassName
	if config.StorageClassName == "" {
		storageClassName = nil
	}
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      connection.Name,
			Namespace: connection.Namespace,
			Labels:    labelsFor(connection),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: storageClassName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: config.SharedVolumeSize},
			},
		},
	}
}

func desiredService(connection *recordingv1alpha1.CameraConnection, config Config) *corev1.Service {
	labels := labelsFor(connection)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      connection.Name,
			Namespace: connection.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP:                corev1.ClusterIPNone,
			PublishNotReadyAddresses: true,
			Selector:                 labels,
			Ports: []corev1.ServicePort{
				{Name: "rtp", Protocol: corev1.ProtocolUDP, Port: config.RTPPort, TargetPort: intstr.FromString("rtp")},
				{Name: "rtcp", Protocol: corev1.ProtocolUDP, Port: config.RTCPPort, TargetPort: intstr.FromString("rtcp")},
			},
		},
	}
}

func desiredPod(connection *recordingv1alpha1.CameraConnection, config Config) *corev1.Pod {
	labels := labelsFor(connection)
	sharedEnvironment := []corev1.EnvVar{
		{Name: "KINUGASA_SESSION_ID", Value: connection.Spec.SessionID},
		{Name: "KINUGASA_CAMERA_IDENTITY_ID", Value: connection.Spec.CameraIdentityID},
		{Name: "KINUGASA_SHARED_VOLUME", Value: config.SharedVolumeMountPath},
	}
	workerEnvironment := append([]corev1.EnvVar{}, sharedEnvironment...)
	workerEnvironment = append(workerEnvironment,
		corev1.EnvVar{Name: "KINUGASA_CONSOLE_GRPC_ADDRESS", Value: config.ConsoleGRPCAddress},
	)
	uploaderEnvironment := append([]corev1.EnvVar{}, sharedEnvironment...)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      connection.Name,
			Namespace: connection.Namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyOnFailure,
			SecurityContext: &corev1.PodSecurityContext{
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{
				{
					Name:            workerContainer,
					Image:           config.WorkerImage,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Env:             workerEnvironment,
					Ports: []corev1.ContainerPort{
						{Name: "rtp", Protocol: corev1.ProtocolUDP, ContainerPort: config.RTPPort},
						{Name: "rtcp", Protocol: corev1.ProtocolUDP, ContainerPort: config.RTCPPort},
					},
					VolumeMounts:    []corev1.VolumeMount{{Name: sharedVolumeName, MountPath: config.SharedVolumeMountPath}},
					SecurityContext: restrictedContainerSecurityContext(),
				},
				{
					Name:            uploaderContainer,
					Image:           config.UploaderImage,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Env:             uploaderEnvironment,
					VolumeMounts:    []corev1.VolumeMount{{Name: sharedVolumeName, MountPath: config.SharedVolumeMountPath}},
					SecurityContext: restrictedContainerSecurityContext(),
				},
			},
			Volumes: []corev1.Volume{{
				Name: sharedVolumeName,
				VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: connection.Name,
				}},
			}},
		},
	}
	if config.ObjectStorageSecret != "" {
		pod.Spec.Containers[1].EnvFrom = []corev1.EnvFromSource{{
			SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: config.ObjectStorageSecret}},
		}}
	}
	return pod
}

func restrictedContainerSecurityContext() *corev1.SecurityContext {
	falseValue := false
	trueValue := true
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: &falseValue,
		Privileged:               &falseValue,
		ReadOnlyRootFilesystem:   &trueValue,
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}
}

func storageRequest(pvc *corev1.PersistentVolumeClaim) resource.Quantity {
	return pvc.Spec.Resources.Requests[corev1.ResourceStorage]
}
