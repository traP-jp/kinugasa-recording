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
	livekitapi "github.com/comavius/kinugasa-recording/internal/livekit"
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
		httpAddress         string
		metricsAddress      string
		healthAddress       string
		namespace           string
		s3Bucket            string
		s3Endpoint          string
		s3Region            string
		s3UsePathStyle      bool
		publicMediaHost     string
		mediaNodePortMin    int
		mediaNodePortMax    int
		liveKitURL          string
		liveKitAPIKey       string
		liveKitAPISecret    string
		liveKitRoom         string
		fanoutImage         string
		liveKitIngressImage string
		liveKitPublicURL    string
		liveKitTokenTTL     time.Duration
	)

	flag.StringVar(&httpAddress, "http-bind-address", ":8080", "address for the Web UI HTTP API")
	flag.StringVar(&metricsAddress, "metrics-bind-address", ":8081", "address for controller metrics")
	flag.StringVar(&healthAddress, "health-probe-bind-address", ":8082", "address for manager health probes")
	flag.StringVar(&namespace, "namespace", envOrDefault("POD_NAMESPACE", "kinugasa-recording"), "namespace containing recording sessions")
	flag.StringVar(&s3Bucket, "s3-bucket", os.Getenv("S3_BUCKET"), "S3 bucket containing recordings")
	flag.StringVar(&s3Endpoint, "s3-endpoint", os.Getenv("S3_ENDPOINT"), "optional S3-compatible endpoint")
	flag.StringVar(&s3Region, "s3-region", envOrDefault("S3_REGION", "us-east-1"), "S3 region")
	flag.BoolVar(&s3UsePathStyle, "s3-use-path-style", envBool("S3_USE_PATH_STYLE"), "use path-style S3 URLs")
	flag.StringVar(&publicMediaHost, "public-media-host", os.Getenv("PUBLIC_MEDIA_HOST"), "LAN host advertised to camera clients")
	flag.IntVar(&mediaNodePortMin, "media-node-port-min", envInt("MEDIA_NODE_PORT_MIN", 30000), "first media NodePort")
	flag.IntVar(&mediaNodePortMax, "media-node-port-max", envInt("MEDIA_NODE_PORT_MAX", 32767), "last media NodePort")
	flag.StringVar(&liveKitURL, "livekit-url", os.Getenv("LIVEKIT_URL"), "LiveKit server API URL")
	flag.StringVar(&liveKitAPIKey, "livekit-api-key", os.Getenv("LIVEKIT_API_KEY"), "LiveKit API key")
	flag.StringVar(&liveKitAPISecret, "livekit-api-secret", os.Getenv("LIVEKIT_API_SECRET"), "LiveKit API secret")
	flag.StringVar(&liveKitRoom, "livekit-room", envOrDefault("LIVEKIT_ROOM", "kinugasa-preview"), "preview room name")
	flag.StringVar(&fanoutImage, "video-fanout-image", envOrDefault("VIDEO_FANOUT_IMAGE", "kinugasa-recording/video-fanout:latest"), "video-fanout image")
	flag.StringVar(&liveKitIngressImage, "livekit-ingress-image", envOrDefault("LIVEKIT_INGRESS_IMAGE", "kinugasa-recording/livekit-ingress:latest"), "livekit-ingress bridge image")
	flag.StringVar(&liveKitPublicURL, "livekit-public-url", os.Getenv("LIVEKIT_PUBLIC_URL"), "browser-reachable LiveKit WebSocket URL")
	flag.DurationVar(&liveKitTokenTTL, "livekit-token-ttl", envDuration("LIVEKIT_TOKEN_TTL", 5*time.Minute), "preview participant token TTL")
	zapOptions := zap.Options{Development: false}
	zapOptions.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOptions)))
	logger := ctrl.Log.WithName("setup")
	if s3Bucket == "" {
		logger.Error(fmt.Errorf("S3 bucket is required"), "validate configuration")
		os.Exit(1)
	}
	if publicMediaHost == "" || mediaNodePortMin < 30000 || mediaNodePortMax > 32767 || mediaNodePortMin > mediaNodePortMax {
		logger.Error(fmt.Errorf("PUBLIC_MEDIA_HOST and a valid media NodePort range are required"), "validate configuration")
		os.Exit(1)
	}
	if liveKitURL == "" || liveKitPublicURL == "" || liveKitAPIKey == "" || liveKitAPISecret == "" || liveKitRoom == "" {
		logger.Error(fmt.Errorf("LiveKit URL, API credentials, and room are required"), "validate configuration")
		os.Exit(1)
	}
	if liveKitTokenTTL < time.Minute || liveKitTokenTTL > livekitapi.MaximumPreviewTokenTTL {
		logger.Error(fmt.Errorf("LiveKit token TTL must be between 1m and %s", livekitapi.MaximumPreviewTokenTTL), "validate configuration")
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

	liveKitClient, err := livekitapi.NewClient(liveKitURL, liveKitAPIKey, liveKitAPISecret)
	if err != nil {
		logger.Error(err, "create LiveKit API client")
		os.Exit(1)
	}
	ingressManager := &operator.LiveKitIngressManager{Client: manager.GetClient(), API: liveKitClient, Participants: liveKitClient, RoomName: liveKitRoom}
	reconciler := &operator.SessionReconciler{Client: manager.GetClient(), Recorder: manager.GetEventRecorder("session-controller")}
	reconciler.Workloads = &operator.CameraWorkloadReconciler{
		Client: manager.GetClient(), Ingress: ingressManager, FanoutImage: fanoutImage,
		LiveKitIngressImage: liveKitIngressImage, PublicMediaHost: publicMediaHost,
	}
	must(reconciler.SetupWithManager(manager), "register Session reconciler")
	must(manager.Add(&operator.LiveKitRoomInitializer{API: liveKitClient, RoomName: liveKitRoom}), "register LiveKit room initializer")

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
	cameraService := &operator.CameraService{
		Client: manager.GetClient(), Namespace: namespace, PublicMediaHost: publicMediaHost,
		NodePortMin: int32(mediaNodePortMin), NodePortMax: int32(mediaNodePortMax),
	}
	tokenIssuer := &livekitapi.TokenIssuer{APIKey: liveKitAPIKey, APISecret: liveKitAPISecret, ServerURL: liveKitPublicURL, RoomName: liveKitRoom, TTL: liveKitTokenTTL}
	takeService := &operator.TakeService{Client: manager.GetClient(), Namespace: namespace}
	apiServer := httpapi.NewServer(manager.GetCache(), namespace, sessionCreator).
		WithCameraService(cameraService).WithTakeService(takeService).WithPreviewTokenService(tokenIssuer)
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

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
}
