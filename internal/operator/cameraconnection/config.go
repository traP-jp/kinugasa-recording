package cameraconnection

import (
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
)

const defaultSharedVolumeMountPath = "/var/lib/kinugasa-recording"

const minimumRISTEncryptionPepperLength = 32

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
	MPEGTSPort            int32
	RISTPort              int32
	RISTEncryptionPepper  string
	RISTPublicHost        string
	RISTNodePortMin       int32
	RISTNodePortMax       int32
}

func (c Config) withDefaults() Config {
	if c.SharedVolumeMountPath == "" {
		c.SharedVolumeMountPath = defaultSharedVolumeMountPath
	}
	if c.RTPPort == 0 {
		c.RTPPort = 8000
	}
	if c.MPEGTSPort == 0 {
		c.MPEGTSPort = 10000
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
	if c.MPEGTSPort < 1 || c.MPEGTSPort > 65535 {
		validationErrors = append(validationErrors, fmt.Errorf("MPEG-TS port %d is outside 1..65535", c.MPEGTSPort))
	}
	if c.RISTPort < 1 || c.RISTPort > 65535 {
		validationErrors = append(validationErrors, fmt.Errorf("RIST port %d is outside 1..65535", c.RISTPort))
	}
	if len(c.RISTEncryptionPepper) < minimumRISTEncryptionPepperLength {
		validationErrors = append(validationErrors, fmt.Errorf(
			"RIST encryption pepper must be at least %d bytes", minimumRISTEncryptionPepperLength,
		))
	}
	if (c.RISTNodePortMin == 0) != (c.RISTNodePortMax == 0) {
		validationErrors = append(validationErrors, errors.New("RIST node port minimum and maximum must be configured together"))
	}
	if c.RISTNodePortMin != 0 {
		if c.RISTPublicHost == "" {
			validationErrors = append(validationErrors, errors.New("RIST public host is required with a node port range"))
		}
		if c.RISTNodePortMin < 1 || c.RISTNodePortMax > 65535 || c.RISTNodePortMin > c.RISTNodePortMax {
			validationErrors = append(validationErrors, fmt.Errorf(
				"RIST node port range %d..%d is invalid", c.RISTNodePortMin, c.RISTNodePortMax,
			))
		}
	} else if c.RISTPublicHost != "" {
		validationErrors = append(validationErrors, errors.New("RIST node port range is required with a public host"))
	}
	if c.RTPPort == c.MPEGTSPort || c.RTPPort == c.RISTPort || c.MPEGTSPort == c.RISTPort {
		validationErrors = append(validationErrors, errors.New("RIST, RTP, and MPEG-TS ports must be distinct"))
	}
	return errors.Join(validationErrors...)
}

func (c Config) usesNodePort() bool {
	return c.RISTNodePortMin != 0 && c.RISTNodePortMax != 0
}
