package cameraconnection

import (
	"context"
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
	service := &corev1.Service{Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{
		Ingress: []corev1.LoadBalancerIngress{{Hostname: "camera.example.com"}},
	}}}
	if got := cameraURLForService(service, 9000); got != "rist://camera.example.com:9000" {
		t.Fatalf("cameraURLForService() = %q", got)
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
	return &Reconciler{Client: fakeClient, Scheme: scheme, Config: testConfig()}
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
