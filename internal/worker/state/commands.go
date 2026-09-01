package state

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/shared/workerprotocol"
)

func (s *Store) SaveCommandResult(result *workerv1.CommandResult) error {
	if err := workerprotocol.ValidateCommandResult(result); err != nil {
		return err
	}
	encoded, err := proto.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal command result: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneDiskState(s.state)
	if previous, ok := next.CommandResults[result.CommandId]; ok {
		stored := &workerv1.CommandResult{}
		if err := proto.Unmarshal(previous, stored); err != nil {
			return fmt.Errorf("unmarshal existing command result: %w", err)
		}
		if proto.Equal(stored, result) {
			return nil
		}
		return fmt.Errorf("command %s already has a different terminal result", result.CommandId)
	}
	next.CommandResults[result.CommandId] = encoded
	if err := s.write(next); err != nil {
		return err
	}
	s.state = next
	return nil
}

func (s *Store) CommandResult(commandID string) (*workerv1.CommandResult, bool, error) {
	if err := workerprotocol.ValidateUUID("command_id", commandID); err != nil {
		return nil, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	encoded, ok := s.state.CommandResults[commandID]
	if !ok {
		return nil, false, nil
	}
	result := &workerv1.CommandResult{}
	if err := proto.Unmarshal(encoded, result); err != nil {
		return nil, false, fmt.Errorf("unmarshal command result: %w", err)
	}
	return result, true, nil
}
