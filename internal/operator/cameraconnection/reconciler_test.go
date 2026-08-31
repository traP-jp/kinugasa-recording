package cameraconnection

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	recordingv1alpha1 "github.com/traP-jp/kinugasa-recording/api/v1alpha1"
	livekitingress "github.com/traP-jp/kinugasa-recording/internal/livekit/ingress"
)

func TestReconcileCreatesWorkerResources(t *testing.T) {
	scheme := testScheme(t)
	connection := testConnection()
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&recordingv1alpha1.CameraConnection{}).
		WithObjects(connection).
		Build()
	reconciler := testReconciler(fakeClient, scheme)
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(connection)}

	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var pvc corev1.PersistentVolumeClaim
	if err := fakeClient.Get(context.Background(), request.NamespacedName, &pvc); err != nil {
		t.Fatalf("get PVC: %v", err)
	}
	if got := storageRequest(&pvc); got.Cmp(resource.MustParse("20Gi")) != 0 {
		t.Fatalf("PVC storage = %s, want 20Gi", got.String())
	}
	assertControlledBy(t, &pvc, connection)

	var service corev1.Service
	if err := fakeClient.Get(context.Background(), request.NamespacedName, &service); err != nil {
		t.Fatalf("get Service: %v", err)
	}
	if service.Spec.Type != corev1.ServiceTypeLoadBalancer || len(service.Spec.Ports) != 1 || service.Spec.Ports[0].Name != "rist" {
		t.Fatalf("RIST Service = %+v", service.Spec)
	}
	assertControlledBy(t, &service, connection)
	var previewSecret corev1.Secret
	if err := fakeClient.Get(context.Background(), request.NamespacedName, &previewSecret); err != nil {
		t.Fatalf("get preview Secret: %v", err)
	}
	if string(previewSecret.Data[previewIngressIDKey]) != "IN_test" ||
		string(previewSecret.Data[previewTokenKey]) != "stream-key" {
		t.Fatalf("preview Secret data = %q", previewSecret.Data)
	}
	assertControlledBy(t, &previewSecret, connection)

	var pod corev1.Pod
	if err := fakeClient.Get(context.Background(), request.NamespacedName, &pod); err != nil {
		t.Fatalf("get Pod: %v", err)
	}
	if pod.Spec.RestartPolicy != corev1.RestartPolicyOnFailure {
		t.Fatalf("restartPolicy = %q, want OnFailure", pod.Spec.RestartPolicy)
	}
	if len(pod.Spec.InitContainers) != 0 || len(pod.Spec.Containers) != 3 {
		t.Fatalf("Pod containers = %d regular, %d init", len(pod.Spec.Containers), len(pod.Spec.InitContainers))
	}
	if pod.Spec.Containers[0].Name != gatewayContainer || pod.Spec.Containers[1].Name != workerContainer ||
		pod.Spec.Containers[2].Name != uploaderContainer {
		t.Fatalf("Pod containers = %q, %q, %q", pod.Spec.Containers[0].Name, pod.Spec.Containers[1].Name, pod.Spec.Containers[2].Name)
	}
	if len(pod.Spec.Volumes) != 2 || pod.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != connection.Name ||
		pod.Spec.Volumes[1].EmptyDir == nil {
		t.Fatalf("Pod volumes = %+v", pod.Spec.Volumes)
	}
	if len(pod.Spec.Containers[0].VolumeMounts) != 1 || len(pod.Spec.Containers[1].VolumeMounts) != 2 ||
		len(pod.Spec.Containers[2].VolumeMounts) != 1 {
		t.Fatalf("Pod container volume mounts = %+v", pod.Spec.Containers)
	}
	if !hasSecretEnvironment(pod.Spec.Containers[1].Env, "KINUGASA_LIVEKIT_WHIP_URL", previewURLKey) ||
		!hasSecretEnvironment(pod.Spec.Containers[1].Env, "KINUGASA_LIVEKIT_WHIP_TOKEN", previewTokenKey) {
		t.Fatalf("worker preview environment = %+v", pod.Spec.Containers[1].Env)
	}
	videoRTPURL := environmentValue(pod.Spec.Containers[0].Env, "KINUGASA_VIDEO_RTP_URL")
	audioRTPURL := environmentValue(pod.Spec.Containers[0].Env, "KINUGASA_AUDIO_RTP_URL")
	if videoRTPURL != "rtp://127.0.0.1:8000?rtcpport=8001" || audioRTPURL != videoRTPURL {
		t.Fatalf("gateway RTP URLs = video %q, audio %q", videoRTPURL, audioRTPURL)
	}
	workerSDP := environmentValue(pod.Spec.Containers[1].Env, "KINUGASA_RTP_SDP")
	if !strings.Contains(workerSDP, "m=video 8000 RTP/AVP 96") ||
		!strings.Contains(workerSDP, "m=audio 8000 RTP/AVP 97") {
		t.Fatalf("worker RTP SDP does not multiplex video and audio on port 8000: %q", workerSDP)
	}
	assertControlledBy(t, &pod, connection)

	var updated recordingv1alpha1.CameraConnection
	if err := fakeClient.Get(context.Background(), request.NamespacedName, &updated); err != nil {
		t.Fatalf("get CameraConnection: %v", err)
	}
	if !controllerutil.ContainsFinalizer(&updated, FinalizerName) {
		t.Fatal("CameraConnection does not contain upload protection finalizer")
	}
	if updated.Status.Phase != recordingv1alpha1.CameraConnectionPhaseActivating ||
		updated.Status.WorkerPodName != connection.Name || updated.Status.PVCName != connection.Name {
		t.Fatalf("CameraConnection status = %+v", updated.Status)
	}

	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	var pods corev1.PodList
	if err := fakeClient.List(context.Background(), &pods, client.InNamespace(connection.Namespace)); err != nil {
		t.Fatalf("list Pods: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("Pod count after second reconcile = %d, want 1", len(pods.Items))
	}
}

func TestCameraURLForLoadBalancerService(t *testing.T) {
	service := &corev1.Service{
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 9000}}},
		Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{
			Ingress: []corev1.LoadBalancerIngress{{Hostname: "camera.example.com"}},
		}},
	}
	if got := cameraURLForService(service, testConfig()); got != "rist://camera.example.com:9000" {
		t.Fatalf("cameraURLForService() = %q", got)
	}
}

func TestReconcileAllocatesDistinctRISTNodePorts(t *testing.T) {
	scheme := testScheme(t)
	first := testConnection()
	second := first.DeepCopy()
	second.Name = "camera-019c240f"
	second.UID = types.UID("connection-uid-2")
	second.Spec.CameraIdentityID = "019c240f-3eb4-72d6-a6fa-adfe1df795c8"
	second.Spec.CameraName = "camera-2"
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&recordingv1alpha1.CameraConnection{}).
		WithObjects(first, second, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: first.Name, Namespace: first.Namespace},
			Spec: corev1.ServiceSpec{
				Type:                corev1.ServiceTypeLoadBalancer,
				HealthCheckNodePort: 30554,
				Ports: []corev1.ServicePort{{
					Name: "rist", Protocol: corev1.ProtocolUDP, Port: 9000, NodePort: 31211,
				}},
			},
		}).
		Build()
	reconciler := testReconciler(fakeClient, scheme)
	reconciler.Config.RISTPublicHost = "127.0.0.1"
	reconciler.Config.RISTNodePortMin = 32000
	reconciler.Config.RISTNodePortMax = 32099

	for _, connection := range []*recordingv1alpha1.CameraConnection{first, second} {
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(connection)}
		if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
			t.Fatalf("Reconcile(%s) error = %v", connection.Name, err)
		}
	}

	for index, connection := range []*recordingv1alpha1.CameraConnection{first, second} {
		var service corev1.Service
		if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(connection), &service); err != nil {
			t.Fatalf("get Service %s: %v", connection.Name, err)
		}
		wantPort := int32(32000 + index)
		if service.Spec.Type != corev1.ServiceTypeNodePort || service.Spec.Ports[0].NodePort != wantPort ||
			service.Spec.HealthCheckNodePort != 0 {
			t.Fatalf("Service %s = %+v, want NodePort %d", connection.Name, service.Spec, wantPort)
		}
		var updated recordingv1alpha1.CameraConnection
		if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(connection), &updated); err != nil {
			t.Fatalf("get CameraConnection %s: %v", connection.Name, err)
		}
		wantURL := fmt.Sprintf("rist://127.0.0.1:%d", wantPort)
		if updated.Status.CameraURL != wantURL || updated.Status.Phase != recordingv1alpha1.CameraConnectionPhaseWaiting {
			t.Fatalf("CameraConnection %s status = %+v, want URL %s", connection.Name, updated.Status, wantURL)
		}
	}
}

func TestObserveWorkerRestartReportsEachFailedRestartOnce(t *testing.T) {
	status := recordingv1alpha1.CameraConnectionStatus{WorkerPodUID: "pod-1"}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{UID: types.UID("pod-1")}, Status: corev1.PodStatus{
		ContainerStatuses: []corev1.ContainerStatus{{
			Name: workerContainer, RestartCount: 1,
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				ExitCode: 2, Reason: "Error",
			}},
		}},
	}}
	if failure := observeWorkerRestart(&status, pod); failure == "" || status.ObservedWorkerRestartCount != 1 {
		t.Fatalf("observeWorkerRestart() = %q, status = %+v", failure, status)
	}
	if failure := observeWorkerRestart(&status, pod); failure != "" {
		t.Fatalf("duplicate observeWorkerRestart() = %q", failure)
	}
	pod.UID = types.UID("pod-2")
	if failure := observeWorkerRestart(&status, pod); failure != "" || status.WorkerPodUID != "pod-2" {
		t.Fatalf("replacement observeWorkerRestart() = %q, status = %+v", failure, status)
	}
}

func TestDeletionWaitsForUploader(t *testing.T) {
	scheme := testScheme(t)
	connection := deletingConnection()
	pvc := desiredPVC(connection, testConfig())
	pod := desiredPod(connection, testConfig())
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&recordingv1alpha1.CameraConnection{}).
		WithObjects(connection, pvc, pod).
		Build()
	reconciler := testReconciler(fakeClient, scheme)
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(connection)}

	result, err := reconciler.Reconcile(context.Background(), request)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.RequeueAfter != 5*time.Second {
		t.Fatalf("Reconcile() requeueAfter = %s, want 5s", result.RequeueAfter)
	}
	var retained corev1.PersistentVolumeClaim
	if err := fakeClient.Get(context.Background(), request.NamespacedName, &retained); err != nil {
		t.Fatalf("PVC was removed before uploader completed: %v", err)
	}
}

func TestDeletionReleasesPreviewBeforeUploaderCompletes(t *testing.T) {
	scheme := testScheme(t)
	connection := deletingConnection()
	pod := desiredPod(connection, testConfig())
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: connection.Name, Namespace: connection.Namespace},
		Data:       map[string][]byte{previewIngressIDKey: []byte("IN_delete")},
	}
	if err := controllerutil.SetControllerReference(connection, secret, scheme); err != nil {
		t.Fatal(err)
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&recordingv1alpha1.CameraConnection{}).
		WithObjects(connection, pod, secret).
		Build()
	previewIngress := &previewIngressStub{}
	reconciler := testReconciler(fakeClient, scheme)
	reconciler.PreviewIngress = previewIngress
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(connection)}

	result, err := reconciler.Reconcile(context.Background(), request)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.RequeueAfter != 5*time.Second || len(previewIngress.deleted) != 1 ||
		previewIngress.deleted[0] != "IN_delete" {
		t.Fatalf("result/deleted = %+v / %q", result, previewIngress.deleted)
	}
	var removed corev1.Secret
	if err := fakeClient.Get(context.Background(), request.NamespacedName, &removed); !apierrors.IsNotFound(err) {
		t.Fatalf("preview Secret still exists or Get failed: %v", err)
	}
}

func TestDeletionReleasesResourcesAfterUploaderCompletion(t *testing.T) {
	scheme := testScheme(t)
	connection := deletingConnection()
	config := testConfig()
	pvc := desiredPVC(connection, config)
	service := desiredService(connection, config)
	pod := desiredPod(connection, config)
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name: uploaderContainer,
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			ExitCode: 0,
		}},
	}}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&recordingv1alpha1.CameraConnection{}).
		WithObjects(connection, pvc, service, pod).
		Build()
	reconciler := testReconciler(fakeClient, scheme)
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(connection)}

	result, err := reconciler.Reconcile(context.Background(), request)
	if err != nil {
		t.Fatalf("Reconcile(delete Pod) error = %v", err)
	}
	if result.RequeueAfter != time.Millisecond {
		t.Fatalf("Reconcile(delete Pod) requeueAfter = %s, want 1ms", result.RequeueAfter)
	}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("Reconcile(release finalizer) error = %v", err)
	}

	for _, object := range []client.Object{&corev1.Pod{}, &corev1.Service{}, &corev1.PersistentVolumeClaim{}} {
		err := fakeClient.Get(context.Background(), request.NamespacedName, object)
		if !apierrors.IsNotFound(err) {
			t.Fatalf("%T still exists or Get failed: %v", object, err)
		}
	}
}

func testConnection() *recordingv1alpha1.CameraConnection {
	return &recordingv1alpha1.CameraConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "camera-019c240e",
			Namespace:  "recording",
			UID:        types.UID("connection-uid"),
			Generation: 1,
		},
		Spec: recordingv1alpha1.CameraConnectionSpec{
			SessionID:        "019c240d-a6de-7de0-a826-0f26e8803fc0",
			SessionName:      "session-1",
			CameraIdentityID: "019c240e-3eb4-72d6-a6fa-adfe1df795c8",
			CameraName:       "camera-1",
		},
	}
}

func deletingConnection() *recordingv1alpha1.CameraConnection {
	connection := testConnection()
	now := metav1.Now()
	connection.DeletionTimestamp = &now
	connection.Finalizers = []string{FinalizerName}
	return connection
}

func testConfig() Config {
	return Config{
		GatewayImage:        "registry.example/video-gateway:test",
		WorkerImage:         "registry.example/video-worker:test",
		UploaderImage:       "registry.example/video-uploader:test",
		ConsoleGRPCAddress:  "console-server.recording.svc:9090",
		ObjectStorageSecret: "object-storage",
		SharedVolumeSize:    resource.MustParse("20Gi"),
	}.withDefaults()
}

func testReconciler(fakeClient client.Client, scheme *runtime.Scheme) *Reconciler {
	return &Reconciler{
		Client: fakeClient, APIReader: fakeClient, Scheme: scheme, Config: testConfig(), PreviewIngress: &previewIngressStub{},
		WorkerFailures: workerFailureStub{},
	}
}

type workerFailureStub struct{}

func (workerFailureStub) MarkWorkerFailure(context.Context, string, string) error { return nil }

type previewIngressStub struct {
	deleted []string
}

func (s *previewIngressStub) Create(
	_ context.Context,
	room, participantIdentity, name string,
) (livekitingress.Endpoint, error) {
	if room == "" || participantIdentity == "" || name == "" {
		return livekitingress.Endpoint{}, fmt.Errorf("incomplete ingress identity")
	}
	return livekitingress.Endpoint{
		IngressID: "IN_test", URL: "https://ingress.example.com/whip", StreamKey: "stream-key",
	}, nil
}

func (s *previewIngressStub) Delete(_ context.Context, ingressID string) error {
	s.deleted = append(s.deleted, ingressID)
	return nil
}

func hasSecretEnvironment(environment []corev1.EnvVar, name, key string) bool {
	for _, variable := range environment {
		if variable.Name == name && variable.ValueFrom != nil && variable.ValueFrom.SecretKeyRef != nil &&
			variable.ValueFrom.SecretKeyRef.Key == key {
			return true
		}
	}
	return false
}

func environmentValue(environment []corev1.EnvVar, name string) string {
	for _, variable := range environment {
		if variable.Name == name {
			return variable.Value
		}
	}
	return ""
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core API to scheme: %v", err)
	}
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add recording API to scheme: %v", err)
	}
	return scheme
}

func assertControlledBy(t *testing.T, object metav1.Object, owner *recordingv1alpha1.CameraConnection) {
	t.Helper()
	if !metav1.IsControlledBy(object, owner) {
		t.Fatalf("%T is not controlled by CameraConnection", object)
	}
}
