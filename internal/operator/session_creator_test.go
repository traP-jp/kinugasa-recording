package operator

import (
	"context"
	"errors"
	"testing"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
	storagelib "github.com/comavius/kinugasa-recording/internal/storage"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSessionCreatorCreatesReservedSession(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add recording scheme: %v", err)
	}
	registry := &sessionRegistryStub{}
	creator := &SessionCreator{
		Client:    fake.NewClientBuilder().WithScheme(scheme).Build(),
		Registry:  registry,
		Namespace: "recording",
	}

	session, err := creator.Create(context.Background(), "-Session-A", "request-1")
	if err != nil {
		t.Fatalf("Create() returned %v", err)
	}
	if registry.name != "-Session-A" {
		t.Fatalf("reserved name = %q, want -Session-A", registry.name)
	}
	if session.Name != SessionResourceName("-Session-A") || session.Spec.Name != "-Session-A" {
		t.Fatalf("created Session = %#v", session)
	}
	if len(session.Name) > 63 {
		t.Fatalf("resource name has %d characters, want at most 63", len(session.Name))
	}
}

func TestSessionCreatorRejectsPreviouslyReservedName(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add recording scheme: %v", err)
	}
	creator := &SessionCreator{
		Client:    fake.NewClientBuilder().WithScheme(scheme).Build(),
		Registry:  &sessionRegistryStub{err: storagelib.ErrNameReserved},
		Namespace: "recording",
	}

	if _, err := creator.Create(context.Background(), "Session-A", ""); !errors.Is(err, ErrSessionNameReserved) {
		t.Fatalf("Create() returned %v, want ErrSessionNameReserved", err)
	}
}

func TestSessionCreatorReplaysIdempotentRequest(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := recordingv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add recording scheme: %v", err)
	}
	registry := &sessionRegistryStub{}
	creator := &SessionCreator{
		Client:    fake.NewClientBuilder().WithScheme(scheme).Build(),
		Registry:  registry,
		Namespace: "recording",
	}

	first, err := creator.Create(context.Background(), "Session-A", "request-1")
	if err != nil {
		t.Fatalf("first Create() returned %v", err)
	}
	second, err := creator.Create(context.Background(), "Session-A", "request-1")
	if err != nil {
		t.Fatalf("second Create() returned %v", err)
	}
	if second.Name != first.Name {
		t.Fatalf("second Session = %q, want %q", second.Name, first.Name)
	}
}

func TestSessionResourceNameIsCaseSensitiveAndStable(t *testing.T) {
	t.Parallel()

	first := SessionResourceName("Session-A")
	if first != SessionResourceName("Session-A") {
		t.Fatal("SessionResourceName() is not stable")
	}
	if first == SessionResourceName("session-a") {
		t.Fatal("SessionResourceName() is not case-sensitive")
	}
}

type sessionRegistryStub struct {
	name string
	err  error
}

func (stub *sessionRegistryStub) Reserve(_ context.Context, name string) error {
	stub.name = name
	return stub.err
}
