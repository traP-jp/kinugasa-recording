package cameraconnection

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	recordingv1alpha1 "github.com/traP-jp/kinugasa-recording/api/v1alpha1"
)

const (
	sharedVolumeName  = "recordings"
	runtimeVolumeName = "runtime"
	gatewayContainer  = "video-gateway"
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
			Type:                     corev1.ServiceTypeLoadBalancer,
			ExternalTrafficPolicy:    corev1.ServiceExternalTrafficPolicyLocal,
			PublishNotReadyAddresses: true,
			Selector:                 labels,
			Ports: []corev1.ServicePort{
				{Name: "rist", Protocol: corev1.ProtocolUDP, Port: config.RISTPort, TargetPort: intstr.FromString("rist")},
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
		corev1.EnvVar{Name: "KINUGASA_MEDIAMTX_BINARY", Value: "/mediamtx"},
		corev1.EnvVar{Name: "KINUGASA_RTP_ADDRESS", Value: fmt.Sprintf("0.0.0.0:%d", config.RTPPort)},
		corev1.EnvVar{Name: "KINUGASA_RTP_SDP", Value: workerRTPSDP(config)},
		secretEnvironment("KINUGASA_LIVEKIT_WHIP_URL", connection.Name, previewURLKey),
		secretEnvironment("KINUGASA_LIVEKIT_WHIP_TOKEN", connection.Name, previewTokenKey),
	)
	gatewayEnvironment := []corev1.EnvVar{
		{Name: "KINUGASA_RIST_ADDRESS", Value: fmt.Sprintf("0.0.0.0:%d", config.RISTPort)},
		{Name: "KINUGASA_VIDEO_RTP_URL", Value: fmt.Sprintf("rtp://127.0.0.1:%d?rtcpport=%d", config.RTPPort, config.RTCPPort)},
		{Name: "KINUGASA_AUDIO_RTP_URL", Value: fmt.Sprintf("rtp://127.0.0.1:%d?rtcpport=%d", config.RTPPort+2, config.RTCPPort+2)},
	}
	uploaderEnvironment := append([]corev1.EnvVar{}, sharedEnvironment...)
	uploaderEnvironment = append(uploaderEnvironment,
		corev1.EnvVar{Name: "KINUGASA_CONSOLE_GRPC_ADDRESS", Value: config.ConsoleGRPCAddress},
	)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      connection.Name,
			Namespace: connection.Namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyOnFailure,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   pointer(true),
				RunAsUser:      pointer[int64](65532),
				RunAsGroup:     pointer[int64](65532),
				FSGroup:        pointer[int64](65532),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{
				{
					Name:            gatewayContainer,
					Image:           config.GatewayImage,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Env:             gatewayEnvironment,
					Ports: []corev1.ContainerPort{
						{Name: "rist", Protocol: corev1.ProtocolUDP, ContainerPort: config.RISTPort},
					},
					VolumeMounts:    []corev1.VolumeMount{{Name: runtimeVolumeName, MountPath: "/tmp"}},
					SecurityContext: restrictedContainerSecurityContext(),
				},
				{
					Name:            workerContainer,
					Image:           config.WorkerImage,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Env:             workerEnvironment,
					Ports: []corev1.ContainerPort{
						{Name: "rtp", Protocol: corev1.ProtocolUDP, ContainerPort: config.RTPPort},
						{Name: "rtcp", Protocol: corev1.ProtocolUDP, ContainerPort: config.RTCPPort},
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: sharedVolumeName, MountPath: config.SharedVolumeMountPath},
						{Name: runtimeVolumeName, MountPath: "/tmp"},
					},
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
			Volumes: []corev1.Volume{
				{
					Name: sharedVolumeName,
					VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: connection.Name,
					}},
				},
				{Name: runtimeVolumeName, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
			},
		},
	}
	if config.ObjectStorageSecret != "" {
		for index := range pod.Spec.Containers {
			if pod.Spec.Containers[index].Name == uploaderContainer {
				pod.Spec.Containers[index].EnvFrom = []corev1.EnvFromSource{{
					SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: config.ObjectStorageSecret}},
				}}
			}
		}
	}
	return pod
}

func secretEnvironment(name, secretName, key string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
			Key:                  key,
		}},
	}
}

func workerRTPSDP(config Config) string {
	return fmt.Sprintf(`v=0
o=- 0 0 IN IP4 0.0.0.0
s=Kinugasa H264 Camera
c=IN IP4 0.0.0.0
t=0 0
m=video %d RTP/AVP 96
a=rtpmap:96 H264/90000
a=rtcp:%d
m=audio %d RTP/AVP 97
a=rtpmap:97 opus/48000/2
a=rtcp:%d
a=recvonly`, config.RTPPort, config.RTCPPort, config.RTPPort+2, config.RTCPPort+2)
}

func restrictedContainerSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: pointer(false),
		Privileged:               pointer(false),
		ReadOnlyRootFilesystem:   pointer(true),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}
}

func pointer[T any](value T) *T {
	return &value
}

func storageRequest(pvc *corev1.PersistentVolumeClaim) resource.Quantity {
	return pvc.Spec.Resources.Requests[corev1.ResourceStorage]
}
