package cameraconnection

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
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
		string(previewSecret.Data[previewTokenKey]) != "stream-key" ||
		string(previewSecret.Data[ristSecretKey]) != deriveRISTSecret(connection, testConfig().RISTEncryptionPepper) {
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
	if len(pod.Spec.InitContainers) != 0 || len(pod.Spec.Containers) != 2 {
		t.Fatalf("Pod containers = %d regular, %d init", len(pod.Spec.Containers), len(pod.Spec.InitContainers))
	}
	if pod.Spec.Containers[0].Name != gatewayContainer || pod.Spec.Containers[1].Name != workerContainer {
		t.Fatalf("Pod containers = %q, %q", pod.Spec.Containers[0].Name, pod.Spec.Containers[1].Name)
	}
	if len(pod.Spec.Volumes) != 2 || pod.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != connection.Name ||
		pod.Spec.Volumes[1].EmptyDir == nil {
		t.Fatalf("Pod volumes = %+v", pod.Spec.Volumes)
	}
	if len(pod.Spec.Containers[0].VolumeMounts) != 1 || len(pod.Spec.Containers[1].VolumeMounts) != 2 {
		t.Fatalf("Pod container volume mounts = %+v", pod.Spec.Containers)
	}
	if len(pod.Spec.Containers[1].EnvFrom) != 1 || pod.Spec.Containers[1].EnvFrom[0].SecretRef == nil ||
		pod.Spec.Containers[1].EnvFrom[0].SecretRef.Name != "object-storage" {
		t.Fatalf("worker object storage environment = %+v", pod.Spec.Containers[1].EnvFrom)
	}
	if !hasSecretEnvironment(pod.Spec.Containers[1].Env, "KINUGASA_LIVEKIT_WHIP_URL", previewURLKey) ||
		!hasSecretEnvironment(pod.Spec.Containers[1].Env, "KINUGASA_LIVEKIT_WHIP_TOKEN", previewTokenKey) {
		t.Fatalf("worker preview environment = %+v", pod.Spec.Containers[1].Env)
	}
	if !hasSecretEnvironment(pod.Spec.Containers[0].Env, "KINUGASA_RIST_SECRET", ristSecretKey) {
		t.Fatalf("gateway RIST environment = %+v", pod.Spec.Containers[0].Env)
	}
	if got, want := pod.Spec.Containers[0].Args, []string{
		"-i", "rist://@0.0.0.0:9000",
		"-o", "rtp://127.0.0.1:8000",
		"-p", "1",
		"-S", "1000",
		"-s", "$(KINUGASA_RIST_SECRET)",
		"-e", "256",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("gateway arguments = %q, want %q", got, want)
	}
	if got := environmentValue(pod.Spec.Containers[1].Env, "KINUGASA_MPEGTS_ADDRESS"); got != "127.0.0.1:10000" {
		t.Fatalf("worker MPEG-TS address = %q", got)
	}
	if got := environmentValue(pod.Spec.Containers[1].Env, "KINUGASA_FFPROBE_BINARY"); got != "/usr/bin/ffprobe" {
		t.Fatalf("worker ffprobe binary = %q", got)
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

func TestEnsureConnectionSecretAddsRISTKeyAndRestartsPod(t *testing.T) {
	scheme := testScheme(t)
	connection := testConnection()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: connection.Name, Namespace: connection.Namespace},
		Data: map[string][]byte{
			previewIngressIDKey: []byte("IN_test"),
			previewURLKey:       []byte("https://ingress.example.com/whip"),
			previewTokenKey:     []byte("stream-key"),
		},
	}
	if err := controllerutil.SetControllerReference(connection, secret, scheme); err != nil {
		t.Fatal(err)
	}
	pod := desiredPod(connection, testConfig())
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(connection, secret, pod).Build()
	reconciler := testReconciler(fakeClient, scheme)
	key := client.ObjectKeyFromObject(connection)

	if err := reconciler.ensureConnectionSecret(context.Background(), connection); err != nil {
		t.Fatalf("ensureConnectionSecret() error = %v", err)
	}
	var updated corev1.Secret
	if err := fakeClient.Get(context.Background(), key, &updated); err != nil {
		t.Fatalf("get connection Secret: %v", err)
	}
	if got, want := string(updated.Data[ristSecretKey]), deriveRISTSecret(connection, testConfig().RISTEncryptionPepper); got != want {
		t.Fatalf("RIST secret = %q, want %q", got, want)
	}
	var removed corev1.Pod
	if err := fakeClient.Get(context.Background(), key, &removed); !apierrors.IsNotFound(err) {
		t.Fatalf("worker Pod still exists or Get failed: %v", err)
	}
}

func TestCameraURLForLoadBalancerService(t *testing.T) {
	service := &corev1.Service{
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 9000}}},
		Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{
			Ingress: []corev1.LoadBalancerIngress{{Hostname: "camera.example.com"}},
		}},
	}
	connection := testConnection()
	assertEncryptedRISTURL(t, cameraURLForService(service, connection, testConfig()), connection, "camera.example.com:9000")
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
		wantHost := fmt.Sprintf("127.0.0.1:%d", wantPort)
		if updated.Status.Phase != recordingv1alpha1.CameraConnectionPhaseWaiting {
			t.Fatalf("CameraConnection %s status = %+v", connection.Name, updated.Status)
		}
		assertEncryptedRISTURL(t, updated.Status.CameraURL, connection, wantHost)
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
	if failure := observeWorkerRestart(&status, pod); failure == "" || status.WorkerPodUID != "pod-2" {
		t.Fatalf("replacement observeWorkerRestart() = %q, status = %+v", failure, status)
	}
}

func TestDeletionReleasesResourcesImmediately(t *testing.T) {
	scheme := testScheme(t)
	connection := deletingConnection()
	config := testConfig()
	pvc := desiredPVC(connection, config)
	service := desiredService(connection, config)
	pod := desiredPod(connection, config)
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
		GatewayImage:         "registry.example/video-gateway:test",
		WorkerImage:          "registry.example/video-worker:test",
		ConsoleGRPCAddress:   "console-server.recording.svc:9090",
		ObjectStorageSecret:  "object-storage",
		SharedVolumeSize:     resource.MustParse("20Gi"),
		RISTEncryptionPepper: "test-pepper-with-at-least-32-bytes",
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

func assertEncryptedRISTURL(
	t *testing.T,
	rawURL string,
	connection *recordingv1alpha1.CameraConnection,
	wantHost string,
) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse RIST URL %q: %v", rawURL, err)
	}
	if parsed.Scheme != "rist" || parsed.Host != wantHost || parsed.Query().Get("aes-type") != "256" ||
		parsed.Query().Get("secret") != deriveRISTSecret(connection, testConfig().RISTEncryptionPepper) {
		t.Fatalf("RIST URL = %q", rawURL)
	}
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
