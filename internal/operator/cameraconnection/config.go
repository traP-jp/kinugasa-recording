package cameraconnection

import (
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
)

const defaultSharedVolumeMountPath = "/var/lib/kinugasa-recording"

type Config struct {
	GatewayImage          string
	WorkerImage           string
	UploaderImage         string
	ConsoleGRPCAddress    string
	ObjectStorageSecret   string
	StorageClassName      string
	SharedVolumeSize      resource.Quantity
	SharedVolumeMountPath string
	RTPPort               int32
	RTCPPort              int32
	RISTPort              int32
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
	if c.RISTPort == 0 {
		c.RISTPort = 9000
	}
	return c
}

func (c Config) validate() error {
	var validationErrors []error
	if c.WorkerImage == "" {
		validationErrors = append(validationErrors, errors.New("worker image is required"))
	}
	if c.GatewayImage == "" {
		validationErrors = append(validationErrors, errors.New("gateway image is required"))
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
	if c.RISTPort < 1 || c.RISTPort > 65535 {
		validationErrors = append(validationErrors, fmt.Errorf("RIST port %d is outside 1..65535", c.RISTPort))
	}
	if c.RTPPort > 65533 || c.RTCPPort > 65533 {
		validationErrors = append(validationErrors, errors.New("RTP and RTCP ports must leave room for audio ports at +2"))
	}
	ports := map[int32]struct{}{}
	for _, port := range []int32{c.RTPPort, c.RTCPPort, c.RTPPort + 2, c.RTCPPort + 2} {
		ports[port] = struct{}{}
	}
	if len(ports) != 4 {
		validationErrors = append(validationErrors, errors.New("video and audio RTP/RTCP ports must be distinct"))
	}
	return errors.Join(validationErrors...)
}
