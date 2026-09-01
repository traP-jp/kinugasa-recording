package control

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
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
	workerstate "github.com/traP-jp/kinugasa-recording/internal/worker/state"
)

const (
	clientWorkerID  = "019c278f-a6de-7de0-a826-0f26e8803fc0"
	clientWorkerID2 = "019c278f-b4f0-7617-af64-672416551659"
	clientEventID   = "019c278f-bf77-7e55-b297-0947b6bdbe15"
	clientCommandID = "019c278f-c7e0-75d1-b020-309660bdd377"
)

var clientTestTime = time.Date(2026, 8, 31, 4, 0, 0, 0, time.UTC)

func TestClientReplaysEventsAndAcknowledgesThem(t *testing.T) {
	store := newClientState(t)
	event, err := store.AppendEvent(&workerv1.WorkerEvent{
		EventId:    clientEventID,
		OccurredAt: timestamppb.New(clientTestTime),
		Event: &workerv1.WorkerEvent_InputStatusChanged{InputStatusChanged: &workerv1.InputStatus{
			State: workerv1.InputState_INPUT_STATE_CONNECTED,
		}},
	})
	if err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	acknowledged := make(chan struct{})
	service := newControlTestService(t, func(stream workerv1.ConsoleVideoWorkerService_ControlServer) error {
		hello := receiveHello(t, stream)
		sendRegistered(t, stream, hello.GetHello().WorkerId)
		message, err := stream.Recv()
		if err != nil {
			t.Errorf("Recv(event) error = %v", err)
			return err
		}
		if !proto.Equal(message.GetEvent(), event) {
			t.Errorf("received event = %+v, want %+v", message.GetEvent(), event)
		}
		if err := stream.Send(&workerv1.ConsoleMessage{
			Payload: &workerv1.ConsoleMessage_EventsAcknowledged{
				EventsAcknowledged: &workerv1.WorkerEventsAcknowledged{EventIds: []string{clientEventID}},
			},
		}); err != nil {
			t.Errorf("Send(acknowledgement) error = %v", err)
			return err
		}
		close(acknowledged)
		<-stream.Context().Done()
		return stream.Context().Err()
	})
	client := newControlTestClient(t, service, store, &executorStub{})
	ctx, cancel := context.WithCancel(context.Background())
	done := runClient(t, ctx, client)
	waitForSignal(t, acknowledged)
	eventually(t, func() bool {
		pending, pendingError := store.PendingEvents()
		return pendingError == nil && len(pending) == 0
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestClientPersistsCommandResultAndDoesNotReexecuteDuplicate(t *testing.T) {
	store := newClientState(t)
	executor := &executorStub{execute: func(command *workerv1.WorkerCommand) (*workerv1.CommandResult, error) {
		if _, err := store.AppendEvent(&workerv1.WorkerEvent{
			EventId:    clientEventID,
			OccurredAt: timestamppb.New(clientTestTime),
			Event: &workerv1.WorkerEvent_RecordingStatusChanged{
				RecordingStatusChanged: &workerv1.RecordingStatus{
					TakeId:    "take-id",
					State:     workerv1.RecordingState_RECORDING_STATE_RECORDING,
					StartedAt: timestamppb.New(clientTestTime),
				},
			},
		}); err != nil {
			return nil, err
		}
		return appliedResult(command.CommandId), nil
	}}
	command := startCommand()
	resultsReceived := make(chan struct{})
	service := newControlTestService(t, func(stream workerv1.ConsoleVideoWorkerService_ControlServer) error {
		hello := receiveHello(t, stream)
		sendRegistered(t, stream, hello.GetHello().WorkerId)
		for index := 0; index < 2; index++ {
			if err := stream.Send(&workerv1.ConsoleMessage{
				Payload: &workerv1.ConsoleMessage_Command{Command: command},
			}); err != nil {
				t.Errorf("Send(command %d) error = %v", index, err)
				return err
			}
			if index == 0 {
				eventMessage, err := stream.Recv()
				if err != nil {
					t.Errorf("Recv(event) error = %v", err)
					return err
				}
				if eventMessage.GetEvent() == nil {
					t.Errorf("first response payload = %T, want event", eventMessage.Payload)
				}
			}
			resultMessage, err := stream.Recv()
			if err != nil {
				t.Errorf("Recv(result %d) error = %v", index, err)
				return err
			}
			stored, found, storeError := store.CommandResult(clientCommandID)
			if storeError != nil || !found || !proto.Equal(stored, resultMessage.GetCommandResult()) {
				t.Errorf("stored result = %+v, %v, %v; wire result = %+v", stored, found, storeError, resultMessage.GetCommandResult())
			}
		}
		close(resultsReceived)
		<-stream.Context().Done()
		return stream.Context().Err()
	})
	client := newControlTestClient(t, service, store, executor)
	ctx, cancel := context.WithCancel(context.Background())
	done := runClient(t, ctx, client)
	waitForSignal(t, resultsReceived)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if executor.calls.Load() != 1 {
		t.Fatalf("executor calls = %d, want 1", executor.calls.Load())
	}
}

func TestClientReconnectsControlStreamWithBackoff(t *testing.T) {
	store := newClientState(t)
	var streams atomic.Int32
	registered := make(chan struct{})
	service := newControlTestService(t, func(stream workerv1.ConsoleVideoWorkerService_ControlServer) error {
		hello := receiveHello(t, stream)
		if streams.Add(1) == 1 {
			return status.Error(codes.Unavailable, "transient test failure")
		}
		sendRegistered(t, stream, hello.GetHello().WorkerId)
		close(registered)
		<-stream.Context().Done()
		return stream.Context().Err()
	})
	client := newControlTestClient(t, service, store, &executorStub{})
	var waitMu sync.Mutex
	var delays []time.Duration
	client.wait = func(_ context.Context, delay time.Duration) error {
		waitMu.Lock()
		delays = append(delays, delay)
		waitMu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := runClient(t, ctx, client)
	waitForSignal(t, registered)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	waitMu.Lock()
	defer waitMu.Unlock()
	if len(delays) != 1 || delays[0] != time.Millisecond {
		t.Fatalf("retry delays = %v, want [1ms]", delays)
	}
}

func TestClientRejectsMismatchedRegistration(t *testing.T) {
	store := newClientState(t)
	service := newControlTestService(t, func(stream workerv1.ConsoleVideoWorkerService_ControlServer) error {
		receiveHello(t, stream)
		sendRegistered(t, stream, clientWorkerID2)
		return nil
	})
	client := newControlTestClient(t, service, store, &executorStub{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Run(ctx); err == nil {
		t.Fatal("Run() error = nil for mismatched worker registration")
	}
}

type controlTestServer struct {
	workerv1.UnimplementedConsoleVideoWorkerServiceServer
	control func(workerv1.ConsoleVideoWorkerService_ControlServer) error
}

func (s *controlTestServer) Control(stream workerv1.ConsoleVideoWorkerService_ControlServer) error {
	return s.control(stream)
}

func newControlTestService(
	t *testing.T,
	control func(workerv1.ConsoleVideoWorkerService_ControlServer) error,
) workerv1.ConsoleVideoWorkerServiceClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	workerv1.RegisterConsoleVideoWorkerServiceServer(server, &controlTestServer{control: control})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	connection, err := grpc.NewClient(
		"passthrough:///worker-control-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	if err != nil {
		t.Fatalf("create gRPC client: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return workerv1.NewConsoleVideoWorkerServiceClient(connection)
}

func newClientState(t *testing.T) *workerstate.Store {
	t.Helper()
	store, err := workerstate.Open(t.TempDir(), clientWorkerID, clientTestTime)
	if err != nil {
		t.Fatalf("state.Open() error = %v", err)
	}
	return store
}

func newControlTestClient(
	t *testing.T,
	service workerv1.ConsoleVideoWorkerServiceClient,
	store *workerstate.Store,
	executor CommandExecutor,
) *Client {
	t.Helper()
	client, err := NewClient(service, store, executor, Config{
		SessionID:        "session-id",
		CameraIdentityID: "camera-id",
		InitialBackoff:   time.Millisecond,
		MaximumBackoff:   4 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	client.now = func() time.Time { return clientTestTime }
	return client
}

func receiveHello(
	t *testing.T,
	stream workerv1.ConsoleVideoWorkerService_ControlServer,
) *workerv1.WorkerMessage {
	t.Helper()
	message, err := stream.Recv()
	if err != nil {
		t.Errorf("Recv(hello) error = %v", err)
		return &workerv1.WorkerMessage{}
	}
	if message.GetHello() == nil {
		t.Errorf("first worker payload = %T, want hello", message.Payload)
	}
	return message
}

func sendRegistered(
	t *testing.T,
	stream workerv1.ConsoleVideoWorkerService_ControlServer,
	workerID string,
) {
	t.Helper()
	if err := stream.Send(&workerv1.ConsoleMessage{
		Payload: &workerv1.ConsoleMessage_Registered{Registered: &workerv1.WorkerRegistered{
			WorkerId:     workerID,
			RegisteredAt: timestamppb.New(clientTestTime),
		}},
	}); err != nil {
		t.Errorf("Send(registered) error = %v", err)
	}
}

func startCommand() *workerv1.WorkerCommand {
	return &workerv1.WorkerCommand{
		CommandId: clientCommandID,
		IssuedAt:  timestamppb.New(clientTestTime),
		Command: &workerv1.WorkerCommand_StartRecording{StartRecording: &workerv1.StartRecording{
			TakeId:       "take-id",
			RelativePath: "recordings/take-id/video.mp4",
		}},
	}
}

func appliedResult(commandID string) *workerv1.CommandResult {
	return &workerv1.CommandResult{
		CommandId:   commandID,
		Status:      workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_APPLIED,
		CompletedAt: timestamppb.New(clientTestTime),
	}
}

type executorStub struct {
	calls   atomic.Int32
	execute func(*workerv1.WorkerCommand) (*workerv1.CommandResult, error)
}

func (e *executorStub) Execute(_ context.Context, command *workerv1.WorkerCommand) (*workerv1.CommandResult, error) {
	e.calls.Add(1)
	if e.execute != nil {
		return e.execute(command)
	}
	return appliedResult(command.CommandId), nil
}

func runClient(t *testing.T, ctx context.Context, client *Client) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	return done
}

func waitForSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for control stream")
	}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not satisfied")
}
