package workercontrol

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
)

const (
	controlWorkerID1 = "019c260d-a6de-7de0-a826-0f26e8803fc0"
	controlWorkerID2 = "019c260e-3eb4-72d6-a6fa-adfe1df795c8"
	controlEventID   = "019c260e-4a04-73e3-8328-a32a246b8c47"
	controlCommandID = "019c260e-5141-75e4-8b4b-5c611e7fab65"
)

func TestControlRegistersWorkerAndAcknowledgesCommittedEvent(t *testing.T) {
	repository := &workerRepositoryStub{}
	_, client := newTestControlServer(t, repository)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.Control(ctx)
	if err != nil {
		t.Fatalf("Control() error = %v", err)
	}
	if err := stream.Send(workerHelloMessage(controlWorkerID1)); err != nil {
		t.Fatalf("Send(hello) error = %v", err)
	}
	registered, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv(registered) error = %v", err)
	}
	if registered.GetRegistered().WorkerId != controlWorkerID1 {
		t.Fatalf("registered worker ID = %q", registered.GetRegistered().WorkerId)
	}
	event := connectedEvent(1)
	if err := stream.Send(&workerv1.WorkerMessage{
		Payload: &workerv1.WorkerMessage_Event{Event: event},
	}); err != nil {
		t.Fatalf("Send(event) error = %v", err)
	}
	acknowledgement, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv(acknowledgement) error = %v", err)
	}
	if got := acknowledgement.GetEventsAcknowledged().EventIds; len(got) != 1 || got[0] != controlEventID {
		t.Fatalf("acknowledged event IDs = %v", got)
	}
	if repository.appliedWorkerID != controlWorkerID1 || !proto.Equal(repository.appliedEvent, event) {
		t.Fatalf("repository event = %q %+v", repository.appliedWorkerID, repository.appliedEvent)
	}
}

func TestControlRequiresHelloFirst(t *testing.T) {
	_, client := newTestControlServer(t, &workerRepositoryStub{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.Control(ctx)
	if err != nil {
		t.Fatalf("Control() error = %v", err)
	}
	if err := stream.Send(&workerv1.WorkerMessage{
		Payload: &workerv1.WorkerMessage_Event{Event: connectedEvent(1)},
	}); err != nil {
		t.Fatalf("Send(event) error = %v", err)
	}
	_, err = stream.Recv()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Recv() error = %v, code = %s, want InvalidArgument", err, status.Code(err))
	}
}

func TestNewStreamAbortsPreviousCameraStream(t *testing.T) {
	_, client := newTestControlServer(t, &workerRepositoryStub{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := client.Control(ctx)
	if err != nil {
		t.Fatalf("Control(first) error = %v", err)
	}
	if err := first.Send(workerHelloMessage(controlWorkerID1)); err != nil {
		t.Fatalf("Send(first hello) error = %v", err)
	}
	if _, err := first.Recv(); err != nil {
		t.Fatalf("Recv(first registered) error = %v", err)
	}
	second, err := client.Control(ctx)
	if err != nil {
		t.Fatalf("Control(second) error = %v", err)
	}
	if err := second.Send(workerHelloMessage(controlWorkerID2)); err != nil {
		t.Fatalf("Send(second hello) error = %v", err)
	}
	if _, err := second.Recv(); err != nil {
		t.Fatalf("Recv(second registered) error = %v", err)
	}
	if _, err := first.Recv(); status.Code(err) != codes.Aborted {
		t.Fatalf("Recv(first after replacement) error = %v, code = %s, want Aborted", err, status.Code(err))
	}
}

func TestRegistryDeliversCommandToCurrentStream(t *testing.T) {
	registry, client := newTestControlServer(t, &workerRepositoryStub{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.Control(ctx)
	if err != nil {
		t.Fatalf("Control() error = %v", err)
	}
	if err := stream.Send(workerHelloMessage(controlWorkerID1)); err != nil {
		t.Fatalf("Send(hello) error = %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("Recv(registered) error = %v", err)
	}
	command := &workerv1.WorkerCommand{
		CommandId: controlCommandID,
		IssuedAt:  timestamppb.Now(),
		Command:   &workerv1.WorkerCommand_Shutdown{Shutdown: &workerv1.Shutdown{Reason: "test"}},
	}
	if !registry.Enqueue("camera-id", command) {
		t.Fatal("Enqueue() = false")
	}
	received, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv(command) error = %v", err)
	}
	if !proto.Equal(received.GetCommand(), command) {
		t.Fatalf("received command = %+v, want %+v", received.GetCommand(), command)
	}
}

func newTestControlServer(
	t *testing.T,
	repository *workerRepositoryStub,
) (*Registry, workerv1.ConsoleVideoWorkerServiceClient) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	registry := NewRegistry()
	server := NewServer(repository, registry)
	server.now = func() time.Time { return time.Date(2026, 8, 31, 3, 0, 0, 0, time.UTC) }
	workerv1.RegisterConsoleVideoWorkerServiceServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})
	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	if err != nil {
		t.Fatalf("create gRPC client: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return registry, workerv1.NewConsoleVideoWorkerServiceClient(connection)
}

func workerHelloMessage(workerID string) *workerv1.WorkerMessage {
	return &workerv1.WorkerMessage{
		Payload: &workerv1.WorkerMessage_Hello{Hello: &workerv1.WorkerHello{
			WorkerId:         workerID,
			SessionId:        "session-id",
			CameraIdentityId: "camera-id",
			ObservedAt:       timestamppb.Now(),
			Snapshot: &workerv1.WorkerSnapshot{Input: &workerv1.InputStatus{
				State: workerv1.InputState_INPUT_STATE_WAITING,
			}},
		}},
	}
}

func connectedEvent(sequence uint64) *workerv1.WorkerEvent {
	return &workerv1.WorkerEvent{
		EventId:    controlEventID,
		OccurredAt: timestamppb.Now(),
		Sequence:   sequence,
		Event: &workerv1.WorkerEvent_InputStatusChanged{InputStatusChanged: &workerv1.InputStatus{
			State: workerv1.InputState_INPUT_STATE_CONNECTED,
		}},
	}
}

type workerRepositoryStub struct {
	mu              sync.Mutex
	appliedWorkerID string
	appliedEvent    *workerv1.WorkerEvent
}

func (r *workerRepositoryStub) RegisterWorker(context.Context, *workerv1.WorkerHello, time.Time) error {
	return nil
}

func (r *workerRepositoryStub) ApplyWorkerEvent(_ context.Context, workerID string, event *workerv1.WorkerEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.appliedWorkerID = workerID
	r.appliedEvent = proto.Clone(event).(*workerv1.WorkerEvent)
	return nil
}

func (r *workerRepositoryStub) SaveCommandResult(context.Context, string, *workerv1.CommandResult) error {
	return nil
}
