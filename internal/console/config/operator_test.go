package config

import "testing"

func TestOperatorFromEnvironment(t *testing.T) {
	t.Setenv("OPERATOR_ENABLED", "true")
	t.Setenv("VIDEO_WORKER_IMAGE", "registry.example/worker:test")
	t.Setenv("VIDEO_UPLOADER_IMAGE", "registry.example/uploader:test")
	t.Setenv("CONSOLE_GRPC_ADDRESS", "console-server:9090")
	t.Setenv("OPERATOR_NAMESPACE", "recording")
	t.Setenv("SHARED_VOLUME_SIZE", "20Gi")

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
}

func TestOperatorCanBeDisabled(t *testing.T) {
	t.Setenv("OPERATOR_ENABLED", "false")
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
	t.Setenv("VIDEO_WORKER_IMAGE", "")
	t.Setenv("VIDEO_UPLOADER_IMAGE", "")
	t.Setenv("CONSOLE_GRPC_ADDRESS", "")

	if _, err := OperatorFromEnvironment(); err == nil {
		t.Fatal("OperatorFromEnvironment() error = nil, want required image error")
	}
}
