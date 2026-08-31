package config

import "testing"

func TestOperatorFromEnvironment(t *testing.T) {
	t.Setenv("OPERATOR_ENABLED", "true")
	t.Setenv("VIDEO_GATEWAY_IMAGE", "registry.example/gateway:test")
	t.Setenv("VIDEO_WORKER_IMAGE", "registry.example/worker:test")
	t.Setenv("VIDEO_UPLOADER_IMAGE", "registry.example/uploader:test")
	t.Setenv("CONSOLE_GRPC_ADDRESS", "console-server:9090")
	t.Setenv("OPERATOR_NAMESPACE", "recording")
	t.Setenv("SHARED_VOLUME_SIZE", "20Gi")
	t.Setenv("VIDEO_GATEWAY_RIST_PUBLIC_HOST", "127.0.0.1")
	t.Setenv("VIDEO_GATEWAY_RIST_NODE_PORT_MIN", "32000")
	t.Setenv("VIDEO_GATEWAY_RIST_NODE_PORT_MAX", "32099")
	t.Setenv("VIDEO_GATEWAY_RIST_ENCRYPTION_PEPPER", "test-pepper-with-at-least-32-bytes")

	config, err := OperatorFromEnvironment()
	if err != nil {
		t.Fatalf("OperatorFromEnvironment() error = %v", err)
	}
	if !config.Enabled || config.Manager.Namespace != "recording" {
		t.Fatalf("OperatorFromEnvironment() = %+v", config)
	}
	if got := config.Manager.CameraConnection.SharedVolumeSize.String(); got != "20Gi" {
		t.Fatalf("shared volume size = %q, want 20Gi", got)
	}
	if cameraConfig := config.Manager.CameraConnection; cameraConfig.RISTPublicHost != "127.0.0.1" ||
		cameraConfig.RISTNodePortMin != 32000 || cameraConfig.RISTNodePortMax != 32099 ||
		cameraConfig.RISTEncryptionPepper != "test-pepper-with-at-least-32-bytes" {
		t.Fatalf("RIST publication config = %+v", cameraConfig)
	}
}

func TestOperatorCanBeDisabled(t *testing.T) {
	t.Setenv("OPERATOR_ENABLED", "false")
	t.Setenv("VIDEO_GATEWAY_IMAGE", "")
	t.Setenv("VIDEO_WORKER_IMAGE", "")

	config, err := OperatorFromEnvironment()
	if err != nil {
		t.Fatalf("OperatorFromEnvironment() error = %v", err)
	}
	if config.Enabled {
		t.Fatal("OperatorFromEnvironment() enabled = true")
	}
}

func TestOperatorRequiresImages(t *testing.T) {
	t.Setenv("OPERATOR_ENABLED", "true")
	t.Setenv("VIDEO_GATEWAY_IMAGE", "")
	t.Setenv("VIDEO_WORKER_IMAGE", "")
	t.Setenv("VIDEO_UPLOADER_IMAGE", "")
	t.Setenv("CONSOLE_GRPC_ADDRESS", "")

	if _, err := OperatorFromEnvironment(); err == nil {
		t.Fatal("OperatorFromEnvironment() error = nil, want required image error")
	}
}
