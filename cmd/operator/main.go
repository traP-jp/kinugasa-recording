package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	operator "github.com/comavius/kinugasa-recording/internal/operator"
	"github.com/comavius/kinugasa-recording/internal/operator/httpapi"
	storagelib "github.com/comavius/kinugasa-recording/internal/storage"
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
		s3Bucket       string
		s3Endpoint     string
		s3Region       string
		s3UsePathStyle bool
	)

	flag.StringVar(&httpAddress, "http-bind-address", ":8080", "address for the Web UI HTTP API")
	flag.StringVar(&metricsAddress, "metrics-bind-address", ":8081", "address for controller metrics")
	flag.StringVar(&healthAddress, "health-probe-bind-address", ":8082", "address for manager health probes")
	flag.StringVar(&namespace, "namespace", envOrDefault("POD_NAMESPACE", "kinugasa-recording"), "namespace containing recording sessions")
	flag.StringVar(&s3Bucket, "s3-bucket", os.Getenv("S3_BUCKET"), "S3 bucket containing recordings")
	flag.StringVar(&s3Endpoint, "s3-endpoint", os.Getenv("S3_ENDPOINT"), "optional S3-compatible endpoint")
	flag.StringVar(&s3Region, "s3-region", envOrDefault("S3_REGION", "us-east-1"), "S3 region")
	flag.BoolVar(&s3UsePathStyle, "s3-use-path-style", envBool("S3_USE_PATH_STYLE"), "use path-style S3 URLs")
	zapOptions := zap.Options{Development: false}
	zapOptions.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOptions)))
	logger := ctrl.Log.WithName("setup")
	if s3Bucket == "" {
		logger.Error(fmt.Errorf("S3 bucket is required"), "validate configuration")
		os.Exit(1)
	}

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

	awsConfiguration, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(s3Region))
	if err != nil {
		logger.Error(err, "load S3 configuration")
		os.Exit(1)
	}
	s3Client := s3.NewFromConfig(awsConfiguration, func(options *s3.Options) {
		options.UsePathStyle = s3UsePathStyle
		if s3Endpoint != "" {
			options.BaseEndpoint = aws.String(s3Endpoint)
		}
	})
	sessionCreator := &operator.SessionCreator{
		Client:    manager.GetClient(),
		Registry:  storagelib.NewS3SessionRegistry(s3Client, s3Bucket),
		Namespace: namespace,
	}
	apiServer := httpapi.NewServer(manager.GetCache(), namespace, sessionCreator)
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

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}

	return fallback
}

func envBool(name string) bool {
	value, err := strconv.ParseBool(os.Getenv(name))
	return err == nil && value
}
