package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/shared/workerprotocol"
)

const maxStateFileSize = 64 << 20

type Store struct {
	mu       sync.Mutex
	dir      string
	filePath string
	state    diskState
}

func Open(sharedVolume, workerID string, now time.Time) (*Store, error) {
	if err := workerprotocol.ValidateUUID("worker_id", workerID); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(sharedVolume)
	if err != nil {
		return nil, fmt.Errorf("resolve shared volume: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat shared volume: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("shared volume %q is not a directory", root)
	}

	metadataDir := filepath.Join(root, metadataDirName)
	if err := ensureMetadataDirectory(metadataDir); err != nil {
		return nil, err
	}
	store := &Store{
		dir:      metadataDir,
		filePath: filepath.Join(metadataDir, stateFileName),
	}
	loaded, err := loadDiskState(store.filePath)
	if errors.Is(err, os.ErrNotExist) {
		input, marshalError := proto.Marshal(&workerv1.InputStatus{State: workerv1.InputState_INPUT_STATE_WAITING})
		if marshalError != nil {
			return nil, fmt.Errorf("marshal initial input state: %w", marshalError)
		}
		store.state = diskState{
			Version:        stateVersion,
			WorkerID:       workerID,
			Input:          input,
			Outbox:         make([]diskEvent, 0),
			CommandResults: make(map[string][]byte),
		}
		if err := store.write(store.state); err != nil {
			return nil, err
		}
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	if err := validateDiskState(loaded); err != nil {
		return nil, fmt.Errorf("validate worker state: %w", err)
	}
	store.state = loaded
	if loaded.WorkerID != workerID {
		recovered, err := recoverForNewProcess(loaded, workerID, now)
		if err != nil {
			return nil, err
		}
		if err := store.write(recovered); err != nil {
			return nil, err
		}
		store.state = recovered
	}
	return store, nil
}

func ensureMetadataDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil {
			return fmt.Errorf("create worker metadata directory: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect worker metadata directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("worker metadata path %q must be a directory and not a symlink", path)
	}
	return nil
}

func loadDiskState(path string) (diskState, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return diskState{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return diskState{}, fmt.Errorf("worker state path must be a regular file and not a symlink")
	}
	if info.Size() > maxStateFileSize {
		return diskState{}, fmt.Errorf("worker state exceeds %d bytes", maxStateFileSize)
	}
	file, err := os.Open(path)
	if err != nil {
		return diskState{}, err
	}
	defer func() { _ = file.Close() }()

	decoder := json.NewDecoder(io.LimitReader(file, maxStateFileSize+1))
	decoder.DisallowUnknownFields()
	var state diskState
	if err := decoder.Decode(&state); err != nil {
		return diskState{}, fmt.Errorf("decode worker state: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return diskState{}, fmt.Errorf("decode worker state: trailing data")
	}
	return state, nil
}

func (s *Store) write(state diskState) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode worker state: %w", err)
	}
	if len(encoded) > maxStateFileSize {
		return fmt.Errorf("worker state exceeds %d bytes", maxStateFileSize)
	}
	temporary, err := os.CreateTemp(s.dir, "state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary worker state: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict temporary worker state: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		return fmt.Errorf("write temporary worker state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary worker state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary worker state: %w", err)
	}
	if err := os.Rename(temporaryPath, s.filePath); err != nil {
		return fmt.Errorf("replace worker state: %w", err)
	}
	committed = true
	directory, err := os.Open(s.dir)
	if err != nil {
		return fmt.Errorf("open worker metadata directory: %w", err)
	}
	defer func() { _ = directory.Close() }()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync worker metadata directory: %w", err)
	}
	return nil
}

func (s *Store) WorkerID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.WorkerID
}

func (s *Store) Hello(sessionID, cameraIdentityID string, observedAt time.Time) (*workerv1.WorkerHello, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := &workerv1.InputStatus{}
	if err := proto.Unmarshal(s.state.Input, input); err != nil {
		return nil, fmt.Errorf("unmarshal input snapshot: %w", err)
	}
	snapshot := &workerv1.WorkerSnapshot{Input: input}
	if len(s.state.Recording) != 0 {
		recording := &workerv1.RecordingStatus{}
		if err := proto.Unmarshal(s.state.Recording, recording); err != nil {
			return nil, fmt.Errorf("unmarshal recording snapshot: %w", err)
		}
		snapshot.Recording = recording
	}
	hello := &workerv1.WorkerHello{
		WorkerId:          s.state.WorkerID,
		SessionId:         sessionID,
		CameraIdentityId:  cameraIdentityID,
		ObservedAt:        timestamppb.New(observedAt.UTC()),
		Snapshot:          snapshot,
		LastEventSequence: s.state.LastEventSequence,
	}
	if err := workerprotocol.ValidateWorkerHello(hello); err != nil {
		return nil, err
	}
	return hello, nil
}
