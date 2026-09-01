package control

import (
	"context"
	"fmt"
	"io"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/shared/workerprotocol"
)

func (c *Client) runStream(ctx context.Context) error {
	streamContext, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	stream, err := c.service.Control(streamContext)
	if err != nil {
		return fmt.Errorf("open worker control stream: %w", err)
	}
	hello, err := c.state.Hello(c.config.SessionID, c.config.CameraIdentityID, c.now().UTC())
	if err != nil {
		return permanent(fmt.Errorf("build worker hello: %w", err))
	}
	if err := stream.Send(&workerv1.WorkerMessage{
		Payload: &workerv1.WorkerMessage_Hello{Hello: hello},
	}); err != nil {
		return fmt.Errorf("send worker hello: %w", err)
	}
	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("receive worker registration: %w", err)
	}
	if err := workerprotocol.ValidateConsoleMessage(first); err != nil {
		return permanent(fmt.Errorf("validate worker registration: %w", err))
	}
	registered := first.GetRegistered()
	if registered == nil {
		return permanent(fmt.Errorf("first console message must be WorkerRegistered"))
	}
	if registered.WorkerId != c.state.WorkerID() {
		return permanent(fmt.Errorf(
			"registered worker ID %q does not match local worker ID %q",
			registered.WorkerId,
			c.state.WorkerID(),
		))
	}

	received := receiveConsoleMessages(stream)
	sentEvents := make(map[string]struct{})
	if err := c.sendPendingEvents(stream, sentEvents); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.wake:
			if err := c.sendPendingEvents(stream, sentEvents); err != nil {
				return err
			}
		case incoming, ok := <-received:
			if !ok {
				return io.EOF
			}
			if incoming.err != nil {
				return incoming.err
			}
			shutdown, err := c.handleConsoleMessage(ctx, stream, sentEvents, incoming.message)
			if err != nil {
				return err
			}
			if shutdown {
				if err := stream.CloseSend(); err != nil {
					return fmt.Errorf("close worker control stream: %w", err)
				}
				return errShutdownAccepted
			}
		}
	}
}

type receivedConsoleMessage struct {
	message *workerv1.ConsoleMessage
	err     error
}

func receiveConsoleMessages(
	stream workerv1.ConsoleVideoWorkerService_ControlClient,
) <-chan receivedConsoleMessage {
	received := make(chan receivedConsoleMessage, 1)
	go func() {
		defer close(received)
		for {
			message, err := stream.Recv()
			select {
			case received <- receivedConsoleMessage{message: message, err: err}:
			case <-stream.Context().Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return received
}

func (c *Client) sendPendingEvents(
	stream workerv1.ConsoleVideoWorkerService_ControlClient,
	sent map[string]struct{},
) error {
	events, err := c.state.PendingEvents()
	if err != nil {
		return permanent(fmt.Errorf("load pending worker events: %w", err))
	}
	for _, event := range events {
		if _, alreadySent := sent[event.EventId]; alreadySent {
			continue
		}
		if err := stream.Send(&workerv1.WorkerMessage{
			Payload: &workerv1.WorkerMessage_Event{Event: event},
		}); err != nil {
			return fmt.Errorf("send worker event %s: %w", event.EventId, err)
		}
		sent[event.EventId] = struct{}{}
	}
	return nil
}

func (c *Client) handleConsoleMessage(
	ctx context.Context,
	stream workerv1.ConsoleVideoWorkerService_ControlClient,
	sentEvents map[string]struct{},
	message *workerv1.ConsoleMessage,
) (bool, error) {
	if err := workerprotocol.ValidateConsoleMessage(message); err != nil {
		return false, permanent(fmt.Errorf("validate console message: %w", err))
	}
	switch payload := message.Payload.(type) {
	case *workerv1.ConsoleMessage_EventsAcknowledged:
		if err := c.state.AcknowledgeEvents(payload.EventsAcknowledged.EventIds); err != nil {
			return false, permanent(fmt.Errorf("acknowledge worker events: %w", err))
		}
		return false, nil
	case *workerv1.ConsoleMessage_Command:
		result, found, err := c.state.CommandResult(payload.Command.CommandId)
		if err != nil {
			return false, permanent(fmt.Errorf("load command result: %w", err))
		}
		if !found {
			result, err = c.executor.Execute(ctx, payload.Command)
			if err != nil {
				return false, permanent(fmt.Errorf("execute worker command: %w", err))
			}
			if err := validateExecutionResult(payload.Command, result); err != nil {
				return false, permanent(err)
			}
			if err := c.state.SaveCommandResult(result); err != nil {
				return false, permanent(fmt.Errorf("persist command result: %w", err))
			}
		}
		if err := c.sendPendingEvents(stream, sentEvents); err != nil {
			return false, err
		}
		if err := stream.Send(&workerv1.WorkerMessage{
			Payload: &workerv1.WorkerMessage_CommandResult{CommandResult: result},
		}); err != nil {
			return false, fmt.Errorf("send command result %s: %w", result.CommandId, err)
		}
		return shutdownAccepted(payload.Command, result), nil
	case *workerv1.ConsoleMessage_Registered:
		return false, permanent(fmt.Errorf("WorkerRegistered is only valid as the first console message"))
	default:
		return false, permanent(fmt.Errorf("unsupported console message payload"))
	}
}

func validateExecutionResult(command *workerv1.WorkerCommand, result *workerv1.CommandResult) error {
	if err := workerprotocol.ValidateCommandResult(result); err != nil {
		return fmt.Errorf("validate command result: %w", err)
	}
	if result.CommandId != command.CommandId {
		return fmt.Errorf(
			"command result ID %q does not match command ID %q",
			result.CommandId,
			command.CommandId,
		)
	}
	return nil
}

func shutdownAccepted(command *workerv1.WorkerCommand, result *workerv1.CommandResult) bool {
	if command.GetShutdown() == nil {
		return false
	}
	return result.Status == workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_APPLIED ||
		result.Status == workerv1.CommandResultStatus_COMMAND_RESULT_STATUS_ALREADY_APPLIED
}
