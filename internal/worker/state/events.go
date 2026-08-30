package state

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/shared/workerprotocol"
)

func (s *Store) SetRecordingStatus(status *workerv1.RecordingStatus) error {
	if err := workerprotocol.ValidateRecordingStatus("recording", status); err != nil {
		return err
	}
	encoded, err := proto.Marshal(status)
	if err != nil {
		return fmt.Errorf("marshal recording status: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneDiskState(s.state)
	next.Recording = encoded
	if err := s.write(next); err != nil {
		return err
	}
	s.state = next
	return nil
}

func (s *Store) RecordingStatus() (*workerv1.RecordingStatus, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.state.Recording) == 0 {
		return nil, false, nil
	}
	status := &workerv1.RecordingStatus{}
	if err := proto.Unmarshal(s.state.Recording, status); err != nil {
		return nil, false, fmt.Errorf("unmarshal recording status: %w", err)
	}
	return status, true, nil
}

func (s *Store) AppendEvent(event *workerv1.WorkerEvent) (*workerv1.WorkerEvent, error) {
	if event == nil {
		return nil, fmt.Errorf("event must be set")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	next := cloneDiskState(s.state)
	stored := proto.Clone(event).(*workerv1.WorkerEvent)
	stored.Sequence = next.LastEventSequence + 1
	if err := workerprotocol.ValidateWorkerEvent(stored); err != nil {
		return nil, err
	}
	encoded, err := proto.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("marshal worker event: %w", err)
	}
	switch payload := stored.Event.(type) {
	case *workerv1.WorkerEvent_InputStatusChanged:
		next.Input, err = proto.Marshal(payload.InputStatusChanged)
	case *workerv1.WorkerEvent_RecordingStatusChanged:
		next.Recording, err = proto.Marshal(payload.RecordingStatusChanged)
	}
	if err != nil {
		return nil, fmt.Errorf("marshal event snapshot: %w", err)
	}
	next.LastEventSequence = stored.Sequence
	next.Outbox = append(next.Outbox, diskEvent{
		EventID:  stored.EventId,
		Sequence: stored.Sequence,
		Message:  encoded,
	})
	if err := s.write(next); err != nil {
		return nil, err
	}
	s.state = next
	return proto.Clone(stored).(*workerv1.WorkerEvent), nil
}

func (s *Store) PendingEvents() ([]*workerv1.WorkerEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	events := make([]*workerv1.WorkerEvent, 0, len(s.state.Outbox))
	for _, stored := range s.state.Outbox {
		event := &workerv1.WorkerEvent{}
		if err := proto.Unmarshal(stored.Message, event); err != nil {
			return nil, fmt.Errorf("unmarshal pending event: %w", err)
		}
		events = append(events, event)
	}
	return events, nil
}

func (s *Store) AcknowledgeEvents(eventIDs []string) error {
	acknowledged := make(map[string]struct{}, len(eventIDs))
	for _, eventID := range eventIDs {
		if err := workerprotocol.ValidateUUID("event_id", eventID); err != nil {
			return err
		}
		acknowledged[eventID] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	next := cloneDiskState(s.state)
	remaining := make([]diskEvent, 0, len(next.Outbox))
	changed := false
	for _, stored := range next.Outbox {
		if _, ok := acknowledged[stored.EventID]; !ok {
			remaining = append(remaining, stored)
			continue
		}
		changed = true
		s.clearAcknowledgedTerminalRecording(&next, stored)
	}
	if !changed {
		return nil
	}
	next.Outbox = remaining
	if err := s.write(next); err != nil {
		return err
	}
	s.state = next
	return nil
}

func (s *Store) clearAcknowledgedTerminalRecording(state *diskState, stored diskEvent) {
	event := &workerv1.WorkerEvent{}
	if err := proto.Unmarshal(stored.Message, event); err != nil {
		return
	}
	recording := event.GetRecordingStatusChanged()
	if recording == nil || (recording.State != workerv1.RecordingState_RECORDING_STATE_FINISHED &&
		recording.State != workerv1.RecordingState_RECORDING_STATE_ERROR) {
		return
	}
	current := &workerv1.RecordingStatus{}
	if err := proto.Unmarshal(state.Recording, current); err == nil && proto.Equal(current, recording) {
		state.Recording = nil
	}
}
