package workercontrol

import (
	"errors"
	"io"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
	"github.com/traP-jp/kinugasa-recording/internal/shared/workerprotocol"
)

type Server struct {
	workerv1.UnimplementedConsoleVideoWorkerServiceServer
	repository repository.WorkerControlRepository
	registry   *Registry
	now        func() time.Time
}

func NewServer(repository repository.WorkerControlRepository, registry *Registry) *Server {
	if registry == nil {
		registry = NewRegistry()
	}
	return &Server{repository: repository, registry: registry, now: time.Now}
}

func (s *Server) Control(stream workerv1.ConsoleVideoWorkerService_ControlServer) error {
	first, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		return status.Error(codes.InvalidArgument, "first message must be WorkerHello")
	}
	if err != nil {
		return err
	}
	if err := workerprotocol.ValidateWorkerMessage(first); err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid first message: %v", err)
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "first message must be WorkerHello")
	}
	registeredAt := s.now().UTC()
	if err := s.repository.RegisterWorker(stream.Context(), hello, registeredAt); err != nil {
		return workerRepositoryStatus("register worker", err)
	}
	lease := s.registry.Register(hello.WorkerId, hello.CameraIdentityId)
	defer lease.Close()
	if err := stream.Send(&workerv1.ConsoleMessage{
		Payload: &workerv1.ConsoleMessage_Registered{Registered: &workerv1.WorkerRegistered{
			WorkerId:     hello.WorkerId,
			RegisteredAt: timestamp(registeredAt),
		}},
	}); err != nil {
		return err
	}

	incoming := receiveWorkerMessages(stream)
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-lease.Aborted():
			return status.Error(codes.Aborted, "worker stream was superseded")
		case command := <-lease.Commands():
			if err := stream.Send(&workerv1.ConsoleMessage{
				Payload: &workerv1.ConsoleMessage_Command{Command: command},
			}); err != nil {
				return err
			}
		case received := <-incoming:
			if received.err != nil {
				if errors.Is(received.err, io.EOF) {
					return nil
				}
				return received.err
			}
			if err := s.handleWorkerMessage(stream, hello.WorkerId, received.message); err != nil {
				return err
			}
		}
	}
}

type receivedWorkerMessage struct {
	message *workerv1.WorkerMessage
	err     error
}

func receiveWorkerMessages(stream workerv1.ConsoleVideoWorkerService_ControlServer) <-chan receivedWorkerMessage {
	result := make(chan receivedWorkerMessage, 1)
	go func() {
		defer close(result)
		for {
			message, err := stream.Recv()
			if err != nil {
				select {
				case result <- receivedWorkerMessage{err: err}:
				case <-stream.Context().Done():
				}
				return
			}
			select {
			case result <- receivedWorkerMessage{message: message}:
			case <-stream.Context().Done():
				return
			}
		}
	}()
	return result
}

func (s *Server) handleWorkerMessage(
	stream workerv1.ConsoleVideoWorkerService_ControlServer,
	workerID string,
	message *workerv1.WorkerMessage,
) error {
	if err := workerprotocol.ValidateWorkerMessage(message); err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid worker message: %v", err)
	}
	switch payload := message.Payload.(type) {
	case *workerv1.WorkerMessage_Event:
		if err := s.repository.ApplyWorkerEvent(stream.Context(), workerID, payload.Event); err != nil {
			return workerRepositoryStatus("apply worker event", err)
		}
		return stream.Send(&workerv1.ConsoleMessage{
			Payload: &workerv1.ConsoleMessage_EventsAcknowledged{
				EventsAcknowledged: &workerv1.WorkerEventsAcknowledged{EventIds: []string{payload.Event.EventId}},
			},
		})
	case *workerv1.WorkerMessage_CommandResult:
		if err := s.repository.SaveCommandResult(stream.Context(), workerID, payload.CommandResult); err != nil {
			return workerRepositoryStatus("save command result", err)
		}
		return nil
	default:
		return status.Error(codes.InvalidArgument, "WorkerHello is only valid as the first message")
	}
}

func workerRepositoryStatus(operation string, err error) error {
	switch {
	case errors.Is(err, repository.ErrWorkerIdentityMismatch),
		errors.Is(err, repository.ErrWorkerEventSequence),
		errors.Is(err, repository.ErrWorkerStateMismatch),
		errors.Is(err, repository.ErrWorkerCommandMismatch):
		return status.Errorf(codes.FailedPrecondition, "%s: %v", operation, err)
	default:
		return status.Errorf(codes.Internal, "%s failed", operation)
	}
}

func timestamp(value time.Time) *timestamppb.Timestamp {
	return timestamppb.New(value)
}
