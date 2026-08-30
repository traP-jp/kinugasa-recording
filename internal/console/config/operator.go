package config

import (
	"fmt"
	"os"
	"strconv"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/traP-jp/kinugasa-recording/internal/operator"
	"github.com/traP-jp/kinugasa-recording/internal/operator/cameraconnection"
)

type OperatorConfig struct {
	Enabled bool
	Manager operator.Config
}

func OperatorFromEnvironment() (OperatorConfig, error) {
	enabled, err := booleanOrDefault(os.Getenv("OPERATOR_ENABLED"), true)
	if err != nil {
		return OperatorConfig{}, fmt.Errorf("OPERATOR_ENABLED: %w", err)
	}
	if !enabled {
		return OperatorConfig{Enabled: false}, nil
	}
	leaderElection, err := booleanOrDefault(os.Getenv("OPERATOR_LEADER_ELECTION"), true)
	if err != nil {
		return OperatorConfig{}, fmt.Errorf("OPERATOR_LEADER_ELECTION: %w", err)
	}
	rtpPort, err := portOrDefault(os.Getenv("VIDEO_WORKER_RTP_PORT"), 8000)
	if err != nil {
		return OperatorConfig{}, fmt.Errorf("VIDEO_WORKER_RTP_PORT: %w", err)
	}
	rtcpPort, err := portOrDefault(os.Getenv("VIDEO_WORKER_RTCP_PORT"), 8001)
	if err != nil {
		return OperatorConfig{}, fmt.Errorf("VIDEO_WORKER_RTCP_PORT: %w", err)
	}
	volumeSize, err := resource.ParseQuantity(valueOrDefault(os.Getenv("SHARED_VOLUME_SIZE"), "100Gi"))
	if err != nil || volumeSize.Sign() <= 0 {
		return OperatorConfig{}, fmt.Errorf("SHARED_VOLUME_SIZE must be a positive Kubernetes quantity")
	}

	workerImage := os.Getenv("VIDEO_WORKER_IMAGE")
	uploaderImage := os.Getenv("VIDEO_UPLOADER_IMAGE")
	consoleGRPCAddress := os.Getenv("CONSOLE_GRPC_ADDRESS")
	if workerImage == "" || uploaderImage == "" || consoleGRPCAddress == "" {
		return OperatorConfig{}, fmt.Errorf("VIDEO_WORKER_IMAGE, VIDEO_UPLOADER_IMAGE, and CONSOLE_GRPC_ADDRESS are required")
	}
	return OperatorConfig{
		Enabled: true,
		Manager: operator.Config{
			Namespace:          valueOrDefault(os.Getenv("OPERATOR_NAMESPACE"), "default"),
			LeaderElection:     leaderElection,
			MetricsAddress:     valueOrDefault(os.Getenv("OPERATOR_METRICS_ADDRESS"), ":8081"),
			HealthProbeAddress: valueOrDefault(os.Getenv("OPERATOR_HEALTH_ADDRESS"), ":8082"),
			CameraConnection: cameraconnection.Config{
				WorkerImage:           workerImage,
				UploaderImage:         uploaderImage,
				ConsoleGRPCAddress:    consoleGRPCAddress,
				ObjectStorageSecret:   os.Getenv("OBJECT_STORAGE_SECRET"),
				StorageClassName:      os.Getenv("SHARED_VOLUME_STORAGE_CLASS"),
				SharedVolumeSize:      volumeSize,
				SharedVolumeMountPath: os.Getenv("SHARED_VOLUME_MOUNT_PATH"),
				RTPPort:               rtpPort,
				RTCPPort:              rtcpPort,
			},
		},
	}, nil
}

func booleanOrDefault(value string, fallback bool) (bool, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("must be true or false")
	}
	return parsed, nil
}

func portOrDefault(value string, fallback int32) (int32, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed < 1 || parsed > 65535 {
		return 0, fmt.Errorf("must be an integer between 1 and 65535")
	}
	return int32(parsed), nil
}
