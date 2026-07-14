package operator

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"sync"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	operatorvalidation "github.com/comavius/kinugasa-recording/internal/operator/validation"
	storagelib "github.com/comavius/kinugasa-recording/internal/storage"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// ErrInvalidName indicates a name that violates the public naming rules.
	ErrInvalidName = errors.New("invalid name")
	// ErrSessionNameReserved indicates a current or historical duplicate.
	ErrSessionNameReserved = errors.New("session name is reserved")
	// ErrDependencyUnavailable indicates a temporary Kubernetes or S3 failure.
	ErrDependencyUnavailable = errors.New("dependency is unavailable")
	// ErrIdempotencyConflict indicates reuse of an idempotency key for another request.
	ErrIdempotencyConflict = errors.New("idempotency key conflicts with an earlier request")
)

const idempotencyKeyAnnotation = "recording.kinugasa.tra.pt/idempotency-key"

// SessionNameRegistry durably reserves a session name outside Kubernetes.
type SessionNameRegistry interface {
	Reserve(context.Context, string) error
}

// SessionCreator creates Session resources after reserving their user-facing names.
type SessionCreator struct {
	Client    client.Client
	Registry  SessionNameRegistry
	Namespace string
	mutex     sync.Mutex
}

// Create validates, reserves, and creates a Session.
func (creator *SessionCreator) Create(ctx context.Context, name, idempotencyKey string) (*recordingv1alpha1.Session, error) {
	creator.mutex.Lock()
	defer creator.mutex.Unlock()

	if err := operatorvalidation.Name(name); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidName, err)
	}
	if creator.Registry == nil {
		return nil, fmt.Errorf("%w: S3 session registry is not configured", ErrDependencyUnavailable)
	}
	if idempotencyKey != "" {
		var sessions recordingv1alpha1.SessionList
		if err := creator.Client.List(ctx, &sessions, client.InNamespace(creator.Namespace)); err != nil {
			return nil, fmt.Errorf("%w: list Sessions: %v", ErrDependencyUnavailable, err)
		}
		for index := range sessions.Items {
			if sessions.Items[index].Annotations[idempotencyKeyAnnotation] != idempotencyKey {
				continue
			}
			if sessions.Items[index].Spec.Name != name {
				return nil, ErrIdempotencyConflict
			}

			return sessions.Items[index].DeepCopy(), nil
		}
	}

	resourceName := SessionResourceName(name)
	var existing recordingv1alpha1.Session
	if err := creator.Client.Get(ctx, types.NamespacedName{Namespace: creator.Namespace, Name: resourceName}, &existing); err == nil {
		return nil, ErrSessionNameReserved
	} else if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("%w: get Session: %v", ErrDependencyUnavailable, err)
	}

	if err := creator.Registry.Reserve(ctx, name); err != nil {
		if errors.Is(err, storagelib.ErrNameReserved) {
			return nil, ErrSessionNameReserved
		}

		return nil, fmt.Errorf("%w: reserve S3 prefix: %v", ErrDependencyUnavailable, err)
	}
	var annotations map[string]string
	if idempotencyKey != "" {
		annotations = map[string]string{idempotencyKeyAnnotation: idempotencyKey}
	}

	session := &recordingv1alpha1.Session{
		TypeMeta: metav1.TypeMeta{APIVersion: recordingv1alpha1.GroupVersion.String(), Kind: "Session"},
		ObjectMeta: metav1.ObjectMeta{
			Name:        resourceName,
			Namespace:   creator.Namespace,
			Annotations: annotations,
		},
		Spec: recordingv1alpha1.SessionSpec{Name: name},
	}
	if err := creator.Client.Create(ctx, session); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil, ErrSessionNameReserved
		}

		return nil, fmt.Errorf("%w: create Session: %v", ErrDependencyUnavailable, err)
	}

	return session, nil
}

// SessionResourceName maps an unrestricted valid display name to a DNS-safe stable CR name.
func SessionResourceName(name string) string {
	digest := sha256.Sum256([]byte(name))
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:])

	return "session-" + strings.ToLower(encoded)
}
