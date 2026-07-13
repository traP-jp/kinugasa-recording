package main

import (
	"flag"
	"net/http"
	"os"
	"time"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	operator "github.com/comavius/kinugasa-recording/internal/operator"
	"github.com/comavius/kinugasa-recording/internal/operator/httpapi"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	var (
		httpAddress    string
		metricsAddress string
		healthAddress  string
		namespace      string
	)

	flag.StringVar(&httpAddress, "http-bind-address", ":8080", "address for the Web UI HTTP API")
	flag.StringVar(&metricsAddress, "metrics-bind-address", ":8081", "address for controller metrics")
	flag.StringVar(&healthAddress, "health-probe-bind-address", ":8082", "address for manager health probes")
	flag.StringVar(&namespace, "namespace", os.Getenv("POD_NAMESPACE"), "namespace containing recording sessions")
	zapOptions := zap.Options{Development: false}
	zapOptions.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOptions)))
	logger := ctrl.Log.WithName("setup")

	scheme := runtime.NewScheme()
	must(clientgoscheme.AddToScheme(scheme), "register Kubernetes scheme")
	must(recordingv1alpha1.AddToScheme(scheme), "register recording scheme")

	manager, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 metricsserver.Options{BindAddress: metricsAddress},
		HealthProbeBindAddress:  healthAddress,
		LeaderElection:          true,
		LeaderElectionID:        "kinugasa-recording-operator",
		LeaderElectionNamespace: namespace,
	})
	if err != nil {
		logger.Error(err, "create manager")
		os.Exit(1)
	}

	reconciler := &operator.SessionReconciler{
		Client:   manager.GetClient(),
		Recorder: manager.GetEventRecorder("session-controller"),
	}
	must(reconciler.SetupWithManager(manager), "register Session reconciler")

	apiServer := httpapi.NewServer(manager.GetCache(), namespace)
	must(manager.Add(&httpapi.Runnable{HTTPServer: &http.Server{
		Addr:              httpAddress,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}}), "register HTTP API")
	must(manager.AddHealthzCheck("healthz", healthz.Ping), "register health check")
	must(manager.AddReadyzCheck("readyz", healthz.Ping), "register readiness check")

	if err := manager.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "run manager")
		os.Exit(1)
	}
}

func must(err error, action string) {
	if err != nil {
		ctrl.Log.WithName("setup").Error(err, action)
		os.Exit(1)
	}
}
