package operator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	operatorvalidation "github.com/comavius/kinugasa-recording/internal/operator/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	ErrTakeNameReserved  = errors.New("take name is reserved")
	ErrTakeNotFound      = errors.New("take was not found")
	ErrNoAvailableCamera = errors.New("no requested camera is available")
)

const takeIdempotencyAnnotationPrefix = "recording.kinugasa.tra.pt/take-request-"

type ExcludedCamera struct{ Name, Reason string }
type TakeMutationResult struct {
	Take            recordingv1alpha1.TakeSpec
	ExcludedCameras []ExcludedCamera
}

type TakeService struct {
	Client    client.Client
	Namespace string
	Now       func() time.Time
	mutex     sync.Mutex
}

func (service *TakeService) Start(ctx context.Context, sessionName, takeName string, requestedCameraNames []string, idempotencyKey string) (*TakeMutationResult, error) {
	if operatorvalidation.Name(sessionName) != nil || operatorvalidation.Name(takeName) != nil {
		return nil, ErrInvalidName
	}
	for _, name := range requestedCameraNames {
		if operatorvalidation.Name(name) != nil {
			return nil, ErrInvalidName
		}
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()
	var result TakeMutationResult
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		session, err := (&CameraService{Client: service.Client, Namespace: service.Namespace}).getSession(ctx, sessionName)
		if err != nil {
			return err
		}
		digest := takeMutationDigest("start", takeName, requestedCameraNames)
		if replay, conflict, excluded := takeRequestReplay(session, idempotencyKey, digest); conflict {
			return ErrIdempotencyConflict
		} else if replay {
			take, found := findTake(session.Spec.Takes, takeName)
			if !found {
				return ErrTakeNotFound
			}
			result.Take = *take
			result.ExcludedCameras = excluded
			return nil
		}
		if hasRecordingTake(session.Spec.Takes) {
			return ErrTakeRecording
		}
		if slices.Contains(session.Spec.ReservedTakeNames, takeName) {
			return ErrTakeNameReserved
		}
		selected, excluded := selectAvailableCameras(session, requestedCameraNames)
		if len(selected) == 0 {
			return ErrNoAvailableCamera
		}
		now := time.Now
		if service.Now != nil {
			now = service.Now
		}
		take := recordingv1alpha1.TakeSpec{Name: takeName, DesiredState: recordingv1alpha1.DesiredStateRecording, CameraNames: selected, RequestedAt: metav1.NewTime(now().UTC())}
		session.Spec.ReservedTakeNames = append(session.Spec.ReservedTakeNames, takeName)
		session.Spec.Takes = append(session.Spec.Takes, take)
		rememberTakeRequest(session, idempotencyKey, digest, excluded)
		if err := service.Client.Update(ctx, session); err != nil {
			return err
		}
		result = TakeMutationResult{Take: take, ExcludedCameras: excluded}
		return nil
	})
	if err != nil {
		return nil, mapTakeError(err)
	}
	return &result, nil
}

func (service *TakeService) Stop(ctx context.Context, sessionName, takeName, idempotencyKey string) (*recordingv1alpha1.TakeSpec, error) {
	if operatorvalidation.Name(sessionName) != nil || operatorvalidation.Name(takeName) != nil {
		return nil, ErrInvalidName
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()
	var result recordingv1alpha1.TakeSpec
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		session, err := (&CameraService{Client: service.Client, Namespace: service.Namespace}).getSession(ctx, sessionName)
		if err != nil {
			return err
		}
		digest := takeMutationDigest("stop", takeName, nil)
		if replay, conflict, _ := takeRequestReplay(session, idempotencyKey, digest); conflict {
			return ErrIdempotencyConflict
		} else if replay {
			take, found := findTake(session.Spec.Takes, takeName)
			if !found {
				return ErrTakeNotFound
			}
			result = *take
			return nil
		}
		index := slices.IndexFunc(session.Spec.Takes, func(take recordingv1alpha1.TakeSpec) bool { return take.Name == takeName })
		if index < 0 {
			return ErrTakeNotFound
		}
		if session.Spec.Takes[index].DesiredState == recordingv1alpha1.DesiredStateStopped {
			result = session.Spec.Takes[index]
			return nil
		}
		now := time.Now
		if service.Now != nil {
			now = service.Now
		}
		stoppedAt := metav1.NewTime(now().UTC())
		session.Spec.Takes[index].DesiredState = recordingv1alpha1.DesiredStateStopped
		session.Spec.Takes[index].StopRequestedAt = &stoppedAt
		result = session.Spec.Takes[index]
		rememberTakeRequest(session, idempotencyKey, digest, nil)
		return service.Client.Update(ctx, session)
	})
	if err != nil {
		return nil, mapTakeError(err)
	}
	return &result, nil
}

func selectAvailableCameras(session *recordingv1alpha1.Session, requested []string) ([]string, []ExcludedCamera) {
	requested = append([]string(nil), requested...)
	if len(requested) == 0 {
		for _, camera := range session.Spec.Cameras {
			if camera.DesiredState == recordingv1alpha1.DesiredStatePresent {
				requested = append(requested, camera.Name)
			}
		}
	}
	unique := requested[:0]
	seen := map[string]bool{}
	for _, name := range requested {
		if !seen[name] {
			seen[name] = true
			unique = append(unique, name)
		}
	}
	requested = unique
	selected := make([]string, 0, len(requested))
	excluded := []ExcludedCamera{}
	for _, name := range requested {
		camera, found := findCamera(session.Spec.Cameras, name)
		if !found {
			excluded = append(excluded, ExcludedCamera{Name: name, Reason: "CAMERA_NOT_FOUND"})
			continue
		}
		if camera.DesiredState != recordingv1alpha1.DesiredStatePresent {
			excluded = append(excluded, ExcludedCamera{Name: name, Reason: "CAMERA_REMOVED"})
			continue
		}
		status := slices.IndexFunc(session.Status.Cameras, func(status recordingv1alpha1.CameraStatus) bool { return status.Name == name })
		if status < 0 || session.Status.Cameras[status].Phase != recordingv1alpha1.CameraPhaseConnected {
			excluded = append(excluded, ExcludedCamera{Name: name, Reason: "CAMERA_DISCONNECTED"})
			continue
		}
		selected = append(selected, name)
	}
	return selected, excluded
}

func findTake(takes []recordingv1alpha1.TakeSpec, name string) (*recordingv1alpha1.TakeSpec, bool) {
	for index := range takes {
		if takes[index].Name == name {
			return &takes[index], true
		}
	}
	return nil, false
}

func takeMutationDigest(operation, name string, cameras []string) string {
	digest := sha256.Sum256([]byte(operation + "\x00" + name + "\x00" + strings.Join(cameras, "\x00")))
	return hex.EncodeToString(digest[:])
}

type takeRequestRecord struct {
	Digest   string           `json:"digest"`
	Excluded []ExcludedCamera `json:"excluded,omitempty"`
}

func takeRequestReplay(session *recordingv1alpha1.Session, key, digest string) (bool, bool, []ExcludedCamera) {
	if key == "" {
		return false, false, nil
	}
	keyDigest := sha256.Sum256([]byte(key))
	value, found := session.Annotations[takeIdempotencyAnnotationPrefix+hex.EncodeToString(keyDigest[:8])]
	if !found {
		return false, false, nil
	}
	var record takeRequestRecord
	if json.Unmarshal([]byte(value), &record) != nil {
		return false, true, nil
	}
	return record.Digest == digest, record.Digest != digest, record.Excluded
}
func rememberTakeRequest(session *recordingv1alpha1.Session, key, digest string, excluded []ExcludedCamera) {
	if key == "" {
		return
	}
	if session.Annotations == nil {
		session.Annotations = map[string]string{}
	}
	keyDigest := sha256.Sum256([]byte(key))
	value, _ := json.Marshal(takeRequestRecord{Digest: digest, Excluded: excluded})
	session.Annotations[takeIdempotencyAnnotationPrefix+hex.EncodeToString(keyDigest[:8])] = string(value)
}
func mapTakeError(err error) error {
	for _, known := range []error{ErrInvalidName, ErrSessionNotFound, ErrTakeNameReserved, ErrTakeNotFound, ErrTakeRecording, ErrNoAvailableCamera, ErrIdempotencyConflict} {
		if errors.Is(err, known) {
			return err
		}
	}
	return fmt.Errorf("%w: %v", ErrDependencyUnavailable, err)
}
