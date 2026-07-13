package operator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"sync"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	operatorvalidation "github.com/comavius/kinugasa-recording/internal/operator/validation"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	ErrCameraNameReserved  = errors.New("camera name is reserved")
	ErrCameraNotFound      = errors.New("camera was not found")
	ErrSessionNotFound     = errors.New("session was not found")
	ErrTakeRecording       = errors.New("a take is recording")
	ErrMediaPortsExhausted = errors.New("media node ports are exhausted")
)

const cameraIdempotencyAnnotationPrefix = "recording.kinugasa.tra.pt/camera-request-"

type CameraMutationResult struct {
	Camera         recordingv1alpha1.CameraSpec
	ConnectionURLs recordingv1alpha1.CameraEndpoints
}

type CameraService struct {
	Client          client.Client
	Namespace       string
	PublicMediaHost string
	NodePortMin     int32
	NodePortMax     int32
	mutex           sync.Mutex
}

func (service *CameraService) Add(ctx context.Context, sessionName, cameraName, idempotencyKey string) (*CameraMutationResult, error) {
	if operatorvalidation.Name(sessionName) != nil || operatorvalidation.Name(cameraName) != nil {
		return nil, ErrInvalidName
	}
	if service.PublicMediaHost == "" {
		return nil, fmt.Errorf("%w: PUBLIC_MEDIA_HOST is not configured", ErrDependencyUnavailable)
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()

	var result CameraMutationResult
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		session, err := service.getSession(ctx, sessionName)
		if err != nil {
			return err
		}
		requestDigest := mutationDigest("add", cameraName)
		if replay, conflict := cameraRequestReplay(session, idempotencyKey, requestDigest); conflict {
			return ErrIdempotencyConflict
		} else if replay {
			camera, found := findCamera(session.Spec.Cameras, cameraName)
			if !found {
				return ErrCameraNotFound
			}
			result = service.result(*camera)
			return nil
		}
		if hasRecordingTake(session.Spec.Takes) {
			return ErrTakeRecording
		}
		if slices.Contains(session.Spec.ReservedCameraNames, cameraName) {
			return ErrCameraNameReserved
		}
		rist, srt, err := service.allocatePorts(ctx)
		if err != nil {
			return err
		}
		camera := recordingv1alpha1.CameraSpec{
			Name: cameraName, DesiredState: recordingv1alpha1.DesiredStatePresent,
			Ingress: recordingv1alpha1.CameraIngressSpec{RISTNodePort: rist, SRTNodePort: srt},
		}
		session.Spec.ReservedCameraNames = append(session.Spec.ReservedCameraNames, cameraName)
		session.Spec.Cameras = append(session.Spec.Cameras, camera)
		rememberCameraRequest(session, idempotencyKey, requestDigest)
		if err := service.Client.Update(ctx, session); err != nil {
			return err
		}
		result = service.result(camera)
		return nil
	})
	if err != nil {
		return nil, mapCameraDependencyError(err)
	}
	return &result, nil
}

func (service *CameraService) Delete(ctx context.Context, sessionName, cameraName, idempotencyKey string) (*recordingv1alpha1.CameraSpec, error) {
	if operatorvalidation.Name(sessionName) != nil || operatorvalidation.Name(cameraName) != nil {
		return nil, ErrInvalidName
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()

	var result recordingv1alpha1.CameraSpec
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		session, err := service.getSession(ctx, sessionName)
		if err != nil {
			return err
		}
		requestDigest := mutationDigest("delete", cameraName)
		if replay, conflict := cameraRequestReplay(session, idempotencyKey, requestDigest); conflict {
			return ErrIdempotencyConflict
		} else if replay {
			camera, found := findCamera(session.Spec.Cameras, cameraName)
			if !found {
				return ErrCameraNotFound
			}
			result = *camera
			return nil
		}
		if hasRecordingTake(session.Spec.Takes) {
			return ErrTakeRecording
		}
		camera, found := findCamera(session.Spec.Cameras, cameraName)
		if !found {
			return ErrCameraNotFound
		}
		result = *camera
		if camera.DesiredState == recordingv1alpha1.DesiredStateAbsent {
			return nil
		}
		for index := range session.Spec.Cameras {
			if session.Spec.Cameras[index].Name == cameraName {
				session.Spec.Cameras[index].DesiredState = recordingv1alpha1.DesiredStateAbsent
				result = session.Spec.Cameras[index]
			}
		}
		rememberCameraRequest(session, idempotencyKey, requestDigest)
		return service.Client.Update(ctx, session)
	})
	if err != nil {
		return nil, mapCameraDependencyError(err)
	}
	return &result, nil
}

func (service *CameraService) getSession(ctx context.Context, name string) (*recordingv1alpha1.Session, error) {
	var session recordingv1alpha1.Session
	err := service.Client.Get(ctx, types.NamespacedName{Namespace: service.Namespace, Name: SessionResourceName(name)}, &session)
	if apierrors.IsNotFound(err) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (service *CameraService) allocatePorts(ctx context.Context) (int32, int32, error) {
	minimum, maximum := service.NodePortMin, service.NodePortMax
	if minimum == 0 {
		minimum = 30000
	}
	if maximum == 0 {
		maximum = 32767
	}
	if minimum > maximum {
		return 0, 0, fmt.Errorf("invalid media node port range")
	}
	var sessions recordingv1alpha1.SessionList
	if err := service.Client.List(ctx, &sessions); err != nil {
		return 0, 0, err
	}
	used := map[int32]bool{}
	for _, session := range sessions.Items {
		for _, camera := range session.Spec.Cameras {
			used[camera.Ingress.RISTNodePort] = true
			used[camera.Ingress.SRTNodePort] = true
		}
	}
	ports := make([]int32, 0, 2)
	for port := minimum; port <= maximum && len(ports) < 2; port++ {
		if !used[port] {
			ports = append(ports, port)
		}
	}
	if len(ports) != 2 {
		return 0, 0, ErrMediaPortsExhausted
	}
	return ports[0], ports[1], nil
}

func (service *CameraService) result(camera recordingv1alpha1.CameraSpec) CameraMutationResult {
	host := service.PublicMediaHost
	return CameraMutationResult{Camera: camera, ConnectionURLs: recordingv1alpha1.CameraEndpoints{
		RIST: "rist://" + net.JoinHostPort(host, strconv.Itoa(int(camera.Ingress.RISTNodePort))),
		SRT:  "srt://" + net.JoinHostPort(host, strconv.Itoa(int(camera.Ingress.SRTNodePort))) + "?mode=caller&transtype=live",
	}}
}

func hasRecordingTake(takes []recordingv1alpha1.TakeSpec) bool {
	return slices.ContainsFunc(takes, func(take recordingv1alpha1.TakeSpec) bool {
		return take.DesiredState == recordingv1alpha1.DesiredStateRecording
	})
}

func findCamera(cameras []recordingv1alpha1.CameraSpec, name string) (*recordingv1alpha1.CameraSpec, bool) {
	for index := range cameras {
		if cameras[index].Name == name {
			return &cameras[index], true
		}
	}
	return nil, false
}

func mutationDigest(operation, name string) string {
	digest := sha256.Sum256([]byte(operation + "\x00" + name))
	return hex.EncodeToString(digest[:])
}

func cameraRequestReplay(session *recordingv1alpha1.Session, key, digest string) (bool, bool) {
	if key == "" {
		return false, false
	}
	keyDigest := sha256.Sum256([]byte(key))
	value, found := session.Annotations[cameraIdempotencyAnnotationPrefix+hex.EncodeToString(keyDigest[:8])]
	return found && value == digest, found && value != digest
}

func rememberCameraRequest(session *recordingv1alpha1.Session, key, digest string) {
	if key == "" {
		return
	}
	if session.Annotations == nil {
		session.Annotations = map[string]string{}
	}
	keyDigest := sha256.Sum256([]byte(key))
	session.Annotations[cameraIdempotencyAnnotationPrefix+hex.EncodeToString(keyDigest[:8])] = digest
}

func mapCameraDependencyError(err error) error {
	for _, known := range []error{ErrInvalidName, ErrSessionNotFound, ErrCameraNameReserved, ErrCameraNotFound, ErrTakeRecording, ErrMediaPortsExhausted, ErrIdempotencyConflict} {
		if errors.Is(err, known) {
			return err
		}
	}
	return fmt.Errorf("%w: %v", ErrDependencyUnavailable, err)
}
