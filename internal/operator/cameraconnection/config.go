package cameraconnection

import (
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
)

const defaultSharedVolumeMountPath = "/var/lib/kinugasa-recording"

type Config struct {
	WorkerImage           string
	UploaderImage         string
	ConsoleGRPCAddress    string
	ObjectStorageSecret   string
	StorageClassName      string
	SharedVolumeSize      resource.Quantity
	SharedVolumeMountPath string
	RTPPort               int32
	RTCPPort              int32
}

func (c Config) withDefaults() Config {
	if c.SharedVolumeMountPath == "" {
		c.SharedVolumeMountPath = defaultSharedVolumeMountPath
	}
	if c.RTPPort == 0 {
		c.RTPPort = 8000
	}
	if c.RTCPPort == 0 {
		c.RTCPPort = 8001
	}
	return c
}

func (c Config) validate() error {
	var validationErrors []error
	if c.WorkerImage == "" {
		validationErrors = append(validationErrors, errors.New("worker image is required"))
	}
	if c.UploaderImage == "" {
		validationErrors = append(validationErrors, errors.New("uploader image is required"))
	}
	if c.ConsoleGRPCAddress == "" {
		validationErrors = append(validationErrors, errors.New("console gRPC address is required"))
	}
	if c.SharedVolumeSize.IsZero() || c.SharedVolumeSize.Sign() < 0 {
		validationErrors = append(validationErrors, errors.New("shared volume size must be positive"))
	}
	if c.RTPPort < 1 || c.RTPPort > 65535 {
		validationErrors = append(validationErrors, fmt.Errorf("RTP port %d is outside 1..65535", c.RTPPort))
	}
	if c.RTCPPort < 1 || c.RTCPPort > 65535 {
		validationErrors = append(validationErrors, fmt.Errorf("RTCP port %d is outside 1..65535", c.RTCPPort))
	}
	return errors.Join(validationErrors...)
}
