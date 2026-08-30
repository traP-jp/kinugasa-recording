package operator

import (
	"fmt"
	"log/slog"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	recordingv1alpha1 "github.com/traP-jp/kinugasa-recording/api/v1alpha1"
	"github.com/traP-jp/kinugasa-recording/internal/operator/cameraconnection"
)

type Config struct {
	Namespace          string
	LeaderElection     bool
	MetricsAddress     string
	HealthProbeAddress string
	CameraConnection   cameraconnection.Config
}

func NewManager(
	kubeConfig *rest.Config,
	config Config,
	cameraSource cameraconnection.CameraSource,
	previewIngress cameraconnection.PreviewIngress,
	logger *slog.Logger,
) (ctrl.Manager, error) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("add core Kubernetes API to scheme: %w", err)
	}
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("add recording Kubernetes API to scheme: %w", err)
	}

	cacheOptions := cache.Options{}
	if config.Namespace != "" {
		cacheOptions.DefaultNamespaces = map[string]cache.Config{config.Namespace: {}}
	}
	manager, err := ctrl.NewManager(kubeConfig, ctrl.Options{
		Scheme:                  scheme,
		Cache:                   cacheOptions,
		Logger:                  logr.FromSlogHandler(logger.Handler()),
		LeaderElection:          config.LeaderElection,
		LeaderElectionID:        "kinugasa-recording-operator.recording.kinugasa.trap.jp",
		LeaderElectionNamespace: config.Namespace,
		Metrics: metricsserver.Options{
			BindAddress: config.MetricsAddress,
		},
		HealthProbeBindAddress: config.HealthProbeAddress,
	})
	if err != nil {
		return nil, fmt.Errorf("create operator manager: %w", err)
	}
	if err := (&cameraconnection.Reconciler{
		Client:         manager.GetClient(),
		Scheme:         manager.GetScheme(),
		Config:         config.CameraConnection,
		PreviewIngress: previewIngress,
	}).SetupWithManager(manager); err != nil {
		return nil, fmt.Errorf("set up CameraConnection controller: %w", err)
	}
	if err := manager.Add(&cameraconnection.Synchronizer{
		Client:    manager.GetClient(),
		Source:    cameraSource,
		Namespace: config.Namespace,
		Logger:    logger,
	}); err != nil {
		return nil, fmt.Errorf("add CameraConnection synchronizer: %w", err)
	}
	if err := manager.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return nil, fmt.Errorf("add operator health check: %w", err)
	}
	if err := manager.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return nil, fmt.Errorf("add operator readiness check: %w", err)
	}
	return manager, nil
}
