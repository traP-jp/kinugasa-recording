package recording

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/traP-jp/kinugasa-recording/internal/worker/storage"
)

const (
	SegmentCreatedHookArgument   = "recording-segment-created"
	SegmentCompletedHookArgument = "recording-segment-completed"

	defaultStartWait    = 15 * time.Second
	defaultFinishWait   = 15 * time.Second
	defaultPollInterval = 50 * time.Millisecond
	incompleteDirName   = "incomplete"
	workerMetadataName  = ".kinugasa-worker"
	segmentDirName      = "segments"
	eventDirName        = "segment-events"
)

type Controller interface {
	SetRecording(context.Context, bool) error
}

type Config struct {
	SharedVolume string
	Controller   Controller
	StartWait    time.Duration
	FinishWait   time.Duration
	PollInterval time.Duration
}

type FinalizedRecording struct {
	RelativePath string
	StartedAt    time.Time
	FinishedAt   time.Time
}

type Recorder struct {
	mu          sync.Mutex
	config      Config
	segmentRoot string
	eventRoot   string
	active      *activeRecording
}

type activeRecording struct {
	targetPath     string
	relativePath   string
	segmentPath    string
	eventID        string
	startedAt      time.Time
	baselineEvents map[string]segmentEvent
}

type segmentEvent struct {
	Path       string    `json:"path"`
	OccurredAt time.Time `json:"occurredAt"`
}

// MediaMTXRecordPath prepares the private recording area and returns a path
// pattern for MediaMTX. MediaMTX appends the .mp4 extension automatically.
func MediaMTXRecordPath(sharedVolume string) (string, error) {
	segmentRoot, _, err := prepareLayout(sharedVolume)
	if err != nil {
		return "", err
	}
	return filepath.Join(segmentRoot, "%path", "recording-%s-%f"), nil
}

// WriteSegmentHook records a MediaMTX segment lifecycle event atomically. It is
// called by the short-lived video-worker hook subprocess.
func WriteSegmentHook(sharedVolume, hookArgument, segmentPath string, occurredAt time.Time) error {
	segmentRoot, eventRoot, err := prepareLayout(sharedVolume)
	if err != nil {
		return err
	}
	kind, err := eventKind(hookArgument)
	if err != nil {
		return err
	}
	segmentPath, err = validateSegmentPath(segmentRoot, segmentPath)
	if err != nil {
		return err
	}
	event := segmentEvent{Path: segmentPath, OccurredAt: occurredAt.UTC()}
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode MediaMTX segment event: %w", err)
	}
	name := eventID(segmentPath) + "." + kind + ".json"
	if err := writeFileAtomically(filepath.Join(eventRoot, name), encoded, 0o600); err != nil {
		return fmt.Errorf("persist MediaMTX segment event: %w", err)
	}
	return nil
}

func NewRecorder(config Config) (*Recorder, error) {
	if strings.TrimSpace(config.SharedVolume) == "" {
		return nil, fmt.Errorf("shared volume must be set")
	}
	if config.Controller == nil {
		return nil, fmt.Errorf("MediaMTX recording controller must be set")
	}
	if config.StartWait == 0 {
		config.StartWait = defaultStartWait
	}
	if config.FinishWait == 0 {
		config.FinishWait = defaultFinishWait
	}
	if config.PollInterval == 0 {
		config.PollInterval = defaultPollInterval
	}
	if config.StartWait < 0 || config.FinishWait < 0 || config.PollInterval < 0 {
		return nil, fmt.Errorf("recording wait durations must be positive")
	}
	segmentRoot, eventRoot, err := prepareLayout(config.SharedVolume)
	if err != nil {
		return nil, err
	}
	return &Recorder{
		config:      config,
		segmentRoot: segmentRoot,
		eventRoot:   eventRoot,
	}, nil
}

func (r *Recorder) Start(ctx context.Context, relativePath string) (time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil {
		return time.Time{}, fmt.Errorf("a recording is already active")
	}
	targetPath, err := r.prepareTarget(relativePath)
	if err != nil {
		return time.Time{}, err
	}
	baseline, err := r.listEvents("created")
	if err != nil {
		return time.Time{}, err
	}
	if err := r.config.Controller.SetRecording(ctx, true); err != nil {
		return time.Time{}, fmt.Errorf("start MediaMTX fMP4 recording: %w", err)
	}

	event, id, err := r.waitForCreatedSegment(ctx, baseline)
	if err != nil {
		stopContext, cancel := context.WithTimeout(context.Background(), r.config.FinishWait)
		defer cancel()
		stopError := r.config.Controller.SetRecording(stopContext, false)
		if stopError != nil {
			return time.Time{}, fmt.Errorf("%w; stop MediaMTX recording: %v", err, stopError)
		}
		return time.Time{}, err
	}
	r.active = &activeRecording{
		targetPath:     targetPath,
		relativePath:   relativePath,
		segmentPath:    event.Path,
		eventID:        id,
		startedAt:      event.OccurredAt,
		baselineEvents: baseline,
	}
	return event.OccurredAt, nil
}

func (r *Recorder) Finish(ctx context.Context) (FinalizedRecording, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return FinalizedRecording{}, fmt.Errorf("no recording is active")
	}
	current := r.active
	if err := r.config.Controller.SetRecording(ctx, false); err != nil {
		return FinalizedRecording{}, fmt.Errorf("stop MediaMTX fMP4 recording: %w", err)
	}
	r.active = nil
	completed, err := r.waitForCompletedSegment(ctx, current)
	if err != nil {
		return FinalizedRecording{}, err
	}
	if completed.Path != current.segmentPath {
		return FinalizedRecording{}, fmt.Errorf("MediaMTX completed an unexpected recording segment")
	}
	created, err := r.newCreatedEvents(current.baselineEvents)
	if err != nil {
		return FinalizedRecording{}, err
	}
	if len(created) != 1 {
		return FinalizedRecording{}, fmt.Errorf("MediaMTX created %d recording segments; exactly one is required per take", len(created))
	}
	if err := verifyFragmentedMP4(current.segmentPath); err != nil {
		return FinalizedRecording{}, err
	}
	if err := syncFile(current.segmentPath); err != nil {
		return FinalizedRecording{}, err
	}
	resolvedTarget, err := storage.ResolvePath(r.config.SharedVolume, current.relativePath)
	if err != nil {
		return FinalizedRecording{}, fmt.Errorf("revalidate finalized recording path: %w", err)
	}
	if resolvedTarget != current.targetPath {
		return FinalizedRecording{}, fmt.Errorf("finalized recording path changed while recording")
	}
	if _, err := os.Lstat(current.targetPath); err == nil {
		return FinalizedRecording{}, fmt.Errorf("finalized recording already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return FinalizedRecording{}, fmt.Errorf("inspect finalized recording target: %w", err)
	}
	if err := os.Rename(current.segmentPath, current.targetPath); err != nil {
		return FinalizedRecording{}, fmt.Errorf("publish finalized recording: %w", err)
	}
	if err := syncDirectory(filepath.Dir(current.targetPath)); err != nil {
		return FinalizedRecording{}, err
	}
	return FinalizedRecording{
		RelativePath: current.relativePath,
		StartedAt:    current.startedAt,
		FinishedAt:   completed.OccurredAt,
	}, nil
}

func (r *Recorder) Active() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active != nil
}

func (r *Recorder) Abort() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.config.FinishWait)
	defer cancel()
	if err := r.config.Controller.SetRecording(ctx, false); err != nil {
		return fmt.Errorf("abort MediaMTX fMP4 recording: %w", err)
	}
	current := r.active
	r.active = nil
	_, _ = r.waitForCompletedSegment(ctx, current)
	if err := os.Remove(current.segmentPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove aborted recording: %w", err)
	}
	return nil
}

func (r *Recorder) prepareTarget(relativePath string) (string, error) {
	targetPath, err := storage.ResolvePath(r.config.SharedVolume, relativePath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		return "", fmt.Errorf("create finalized recording directory: %w", err)
	}
	targetPath, err = storage.ResolvePath(r.config.SharedVolume, relativePath)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(targetPath); err == nil {
		return "", fmt.Errorf("finalized recording already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect finalized recording target: %w", err)
	}
	return targetPath, nil
}

func (r *Recorder) waitForCreatedSegment(ctx context.Context, baseline map[string]segmentEvent) (segmentEvent, string, error) {
	ctx, cancel := context.WithTimeout(ctx, r.config.StartWait)
	defer cancel()
	for {
		created, err := r.newCreatedEvents(baseline)
		if err != nil {
			return segmentEvent{}, "", err
		}
		if len(created) > 1 {
			return segmentEvent{}, "", fmt.Errorf("MediaMTX created multiple recording segments while starting")
		}
		for id, event := range created {
			return event, id, nil
		}
		if err := waitPoll(ctx, r.config.PollInterval); err != nil {
			return segmentEvent{}, "", fmt.Errorf("wait for first MediaMTX recording segment: %w", err)
		}
	}
}

func (r *Recorder) waitForCompletedSegment(ctx context.Context, current *activeRecording) (segmentEvent, error) {
	ctx, cancel := context.WithTimeout(ctx, r.config.FinishWait)
	defer cancel()
	for {
		events, err := r.listEvents("completed")
		if err != nil {
			return segmentEvent{}, err
		}
		if event, found := events[current.eventID]; found {
			return event, nil
		}
		if err := waitPoll(ctx, r.config.PollInterval); err != nil {
			return segmentEvent{}, fmt.Errorf("wait for MediaMTX fMP4 finalization: %w", err)
		}
	}
}

func (r *Recorder) newCreatedEvents(baseline map[string]segmentEvent) (map[string]segmentEvent, error) {
	events, err := r.listEvents("created")
	if err != nil {
		return nil, err
	}
	for id := range baseline {
		delete(events, id)
	}
	return events, nil
}

func (r *Recorder) listEvents(kind string) (map[string]segmentEvent, error) {
	entries, err := os.ReadDir(r.eventRoot)
	if err != nil {
		return nil, fmt.Errorf("read MediaMTX segment events: %w", err)
	}
	suffix := "." + kind + ".json"
	events := make(map[string]segmentEvent)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		encoded, err := os.ReadFile(filepath.Join(r.eventRoot, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read MediaMTX segment event: %w", err)
		}
		var event segmentEvent
		if err := json.Unmarshal(encoded, &event); err != nil {
			return nil, fmt.Errorf("decode MediaMTX segment event: %w", err)
		}
		event.Path, err = validateSegmentPath(r.segmentRoot, event.Path)
		if err != nil {
			return nil, err
		}
		if event.OccurredAt.IsZero() {
			return nil, fmt.Errorf("MediaMTX segment event has no timestamp")
		}
		id := strings.TrimSuffix(entry.Name(), suffix)
		if id != eventID(event.Path) {
			return nil, fmt.Errorf("MediaMTX segment event identity does not match its path")
		}
		events[id] = event
	}
	return events, nil
}

func prepareLayout(sharedVolume string) (string, string, error) {
	if strings.TrimSpace(sharedVolume) == "" {
		return "", "", fmt.Errorf("shared volume must be set")
	}
	segmentRoot, err := storage.ResolvePath(sharedVolume, filepath.ToSlash(filepath.Join(workerMetadataName, incompleteDirName, segmentDirName)))
	if err != nil {
		return "", "", err
	}
	eventRoot, err := storage.ResolvePath(sharedVolume, filepath.ToSlash(filepath.Join(workerMetadataName, eventDirName)))
	if err != nil {
		return "", "", err
	}
	for _, directory := range []string{segmentRoot, eventRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return "", "", fmt.Errorf("create recording metadata directory: %w", err)
		}
		info, err := os.Lstat(directory)
		if err != nil {
			return "", "", fmt.Errorf("inspect recording metadata directory: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("recording metadata path must be a directory and not a symlink")
		}
	}
	return segmentRoot, eventRoot, nil
}

func validateSegmentPath(segmentRoot, segmentPath string) (string, error) {
	if !filepath.IsAbs(segmentPath) || filepath.Ext(segmentPath) != ".mp4" {
		return "", fmt.Errorf("MediaMTX segment path must be an absolute MP4 path")
	}
	cleaned := filepath.Clean(segmentPath)
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(cleaned))
	if err != nil {
		return "", fmt.Errorf("resolve MediaMTX segment parent directory: %w", err)
	}
	resolvedPath := filepath.Join(resolvedParent, filepath.Base(cleaned))
	resolvedRoot, err := filepath.EvalSymlinks(segmentRoot)
	if err != nil {
		return "", fmt.Errorf("resolve private recording directory: %w", err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("MediaMTX segment path is outside the private recording directory")
	}
	return resolvedPath, nil
}

func eventKind(hookArgument string) (string, error) {
	switch hookArgument {
	case SegmentCreatedHookArgument:
		return "created", nil
	case SegmentCompletedHookArgument:
		return "completed", nil
	default:
		return "", fmt.Errorf("unknown MediaMTX segment hook %q", hookArgument)
	}
}

func eventID(path string) string {
	digest := sha256.Sum256([]byte(path))
	return hex.EncodeToString(digest[:])
}

func waitPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func verifyFragmentedMP4(path string) error {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect finalized fMP4 recording path: %w", err)
	}
	if !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("finalized fMP4 recording must be a regular file and not a symlink")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open finalized fMP4 recording: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect finalized fMP4 recording: %w", err)
	}
	var hasMOOV, hasMOOF, hasMDAT bool
	var offset int64
	for offset < info.Size() {
		var header [16]byte
		if _, err := io.ReadFull(file, header[:8]); err != nil {
			return fmt.Errorf("read fMP4 box header: %w", err)
		}
		size := uint64(binary.BigEndian.Uint32(header[:4]))
		headerSize := uint64(8)
		if size == 1 {
			if _, err := io.ReadFull(file, header[8:16]); err != nil {
				return fmt.Errorf("read extended fMP4 box header: %w", err)
			}
			size = binary.BigEndian.Uint64(header[8:16])
			headerSize = 16
		} else if size == 0 {
			size = uint64(info.Size() - offset)
		}
		if size < headerSize || size > uint64(info.Size()-offset) {
			return fmt.Errorf("invalid fMP4 box size at byte %d", offset)
		}
		switch string(header[4:8]) {
		case "moov":
			hasMOOV = true
		case "moof":
			hasMOOF = true
		case "mdat":
			hasMDAT = true
		}
		offset += int64(size)
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return fmt.Errorf("seek over fMP4 box: %w", err)
		}
	}
	if !hasMOOV || !hasMOOF || !hasMDAT {
		return fmt.Errorf("recording is not fragmented MP4: moov=%t moof=%t mdat=%t", hasMOOV, hasMOOF, hasMDAT)
	}
	return nil
}

func writeFileAtomically(path string, content []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".event-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func syncFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("open finalized recording for sync: %w", err)
	}
	defer func() { _ = file.Close() }()
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync finalized recording: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open finalized recording directory: %w", err)
	}
	defer func() { _ = directory.Close() }()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync finalized recording directory: %w", err)
	}
	return nil
}
