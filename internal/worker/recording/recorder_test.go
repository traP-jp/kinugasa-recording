package recording

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRecorderFinalizesMediaMTXFMP4Atomically(t *testing.T) {
	sharedVolume := t.TempDir()
	segmentPath := testSegmentPath(t, sharedVolume)
	startedAt := time.Date(2026, 8, 31, 5, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(time.Second)
	controller := &controllerStub{
		onStart: func() error {
			if err := os.WriteFile(segmentPath, fragmentedMP4(), 0o600); err != nil {
				return err
			}
			return WriteSegmentHook(sharedVolume, SegmentCreatedHookArgument, segmentPath, startedAt)
		},
		onStop: func() error {
			return WriteSegmentHook(sharedVolume, SegmentCompletedHookArgument, segmentPath, finishedAt)
		},
	}
	recorder := newTestRecorder(t, sharedVolume, controller)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	actualStart, err := recorder.Start(ctx, "recordings/take-1/video.mp4")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if actualStart != startedAt || !recorder.Active() {
		t.Fatalf("Start() = %v, active = %v", actualStart, recorder.Active())
	}
	finalized, err := recorder.Finish(ctx)
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if finalized.RelativePath != "recordings/take-1/video.mp4" ||
		finalized.StartedAt != startedAt || finalized.FinishedAt != finishedAt {
		t.Fatalf("Finish() = %+v", finalized)
	}
	if recorder.Active() {
		t.Fatal("recorder remains active after finalization")
	}
	finalPath := filepath.Join(sharedVolume, "recordings", "take-1", "video.mp4")
	if err := verifyFragmentedMP4(finalPath); err != nil {
		t.Fatalf("finalized file is not fragmented MP4: %v", err)
	}
	if _, err := os.Stat(segmentPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private MediaMTX segment still exists: %v", err)
	}
	if got := controller.calls(); len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("SetRecording() calls = %v, want [true false]", got)
	}
}

func TestRecorderRejectsSecondRecordingAndEscapingPath(t *testing.T) {
	sharedVolume := t.TempDir()
	segmentPath := testSegmentPath(t, sharedVolume)
	controller := &controllerStub{onStart: func() error {
		if err := os.WriteFile(segmentPath, fragmentedMP4(), 0o600); err != nil {
			return err
		}
		return WriteSegmentHook(sharedVolume, SegmentCreatedHookArgument, segmentPath, time.Now())
	}, onStop: func() error {
		return WriteSegmentHook(sharedVolume, SegmentCompletedHookArgument, segmentPath, time.Now())
	}}
	recorder := newTestRecorder(t, sharedVolume, controller)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := recorder.Start(ctx, "../outside.mp4"); err == nil {
		t.Fatal("Start(escaping path) error = nil")
	}
	if _, err := recorder.Start(ctx, "recordings/first.mp4"); err != nil {
		t.Fatalf("Start(first) error = %v", err)
	}
	if _, err := recorder.Start(ctx, "recordings/second.mp4"); err == nil {
		t.Fatal("Start(second) error = nil")
	}
	if _, err := recorder.Finish(ctx); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
}

func TestRecorderRejectsNonFragmentedMP4(t *testing.T) {
	sharedVolume := t.TempDir()
	segmentPath := testSegmentPath(t, sharedVolume)
	controller := &controllerStub{onStart: func() error {
		if err := os.WriteFile(segmentPath, mp4Boxes("ftyp", "moov", "mdat"), 0o600); err != nil {
			return err
		}
		return WriteSegmentHook(sharedVolume, SegmentCreatedHookArgument, segmentPath, time.Now())
	}, onStop: func() error {
		return WriteSegmentHook(sharedVolume, SegmentCompletedHookArgument, segmentPath, time.Now())
	}}
	recorder := newTestRecorder(t, sharedVolume, controller)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := recorder.Start(ctx, "recordings/not-fragmented.mp4"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := recorder.Finish(ctx); err == nil {
		t.Fatal("Finish(non-fragmented MP4) error = nil")
	}
	if _, err := os.Stat(filepath.Join(sharedVolume, "recordings", "not-fragmented.mp4")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid recording was published: %v", err)
	}
}

func TestRecorderStopsMediaMTXWhenNoSegmentAppears(t *testing.T) {
	sharedVolume := t.TempDir()
	controller := &controllerStub{}
	recorder, err := NewRecorder(Config{
		SharedVolume: sharedVolume,
		Controller:   controller,
		StartWait:    20 * time.Millisecond,
		FinishWait:   time.Second,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if _, err := recorder.Start(context.Background(), "recordings/failure.mp4"); err == nil {
		t.Fatal("Start(without MediaMTX segment) error = nil")
	}
	if got := controller.calls(); len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("SetRecording() calls = %v, want [true false]", got)
	}
}

func TestWriteSegmentHookRejectsPathOutsidePrivateDirectory(t *testing.T) {
	if err := WriteSegmentHook(t.TempDir(), SegmentCreatedHookArgument, "/tmp/outside.mp4", time.Now()); err == nil {
		t.Fatal("WriteSegmentHook(outside path) error = nil")
	}
}

func TestWriteSegmentHookAcceptsCreateEventBeforeSegmentFileExists(t *testing.T) {
	sharedVolume := t.TempDir()
	segmentPath := testSegmentPath(t, sharedVolume)
	if err := WriteSegmentHook(sharedVolume, SegmentCreatedHookArgument, segmentPath, time.Now()); err != nil {
		t.Fatalf("WriteSegmentHook(before file creation) error = %v", err)
	}
	_, eventRoot, err := prepareLayout(sharedVolume)
	if err != nil {
		t.Fatalf("prepareLayout() error = %v", err)
	}
	events, err := os.ReadDir(eventRoot)
	if err != nil || len(events) != 1 {
		t.Fatalf("segment events = %v, error = %v", events, err)
	}
}

func TestRecorderRejectsSymlinkSegment(t *testing.T) {
	sharedVolume := t.TempDir()
	segmentPath := testSegmentPath(t, sharedVolume)
	outsidePath := filepath.Join(t.TempDir(), "outside.mp4")
	if err := os.WriteFile(outsidePath, fragmentedMP4(), 0o600); err != nil {
		t.Fatalf("write outside recording: %v", err)
	}
	controller := &controllerStub{
		onStart: func() error {
			if err := os.Symlink(outsidePath, segmentPath); err != nil {
				return err
			}
			return WriteSegmentHook(sharedVolume, SegmentCreatedHookArgument, segmentPath, time.Now())
		},
		onStop: func() error {
			return WriteSegmentHook(sharedVolume, SegmentCompletedHookArgument, segmentPath, time.Now())
		},
	}
	recorder := newTestRecorder(t, sharedVolume, controller)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := recorder.Start(ctx, "recordings/symlink.mp4"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := recorder.Finish(ctx); err == nil {
		t.Fatal("Finish(symlink segment) error = nil")
	}
}

type controllerStub struct {
	mu      sync.Mutex
	values  []bool
	onStart func() error
	onStop  func() error
}

func (c *controllerStub) SetRecording(_ context.Context, enabled bool) error {
	c.mu.Lock()
	c.values = append(c.values, enabled)
	c.mu.Unlock()
	if enabled && c.onStart != nil {
		return c.onStart()
	}
	if !enabled && c.onStop != nil {
		return c.onStop()
	}
	return nil
}

func (c *controllerStub) calls() []bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]bool(nil), c.values...)
}

func newTestRecorder(t *testing.T, sharedVolume string, controller Controller) *Recorder {
	t.Helper()
	recorder, err := NewRecorder(Config{
		SharedVolume: sharedVolume,
		Controller:   controller,
		StartWait:    time.Second,
		FinishWait:   time.Second,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	return recorder
}

func testSegmentPath(t *testing.T, sharedVolume string) string {
	t.Helper()
	pattern, err := MediaMTXRecordPath(sharedVolume)
	if err != nil {
		t.Fatalf("MediaMTXRecordPath() error = %v", err)
	}
	path := filepath.Join(filepath.Dir(filepath.Dir(pattern)), "camera", "recording-1-000001.mp4")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create test MediaMTX path directory: %v", err)
	}
	return path
}

func fragmentedMP4() []byte {
	return mp4Boxes("ftyp", "moov", "moof", "mdat", "moof", "mdat")
}

func mp4Boxes(types ...string) []byte {
	content := make([]byte, 0, len(types)*8)
	for _, boxType := range types {
		header := make([]byte, 8)
		binary.BigEndian.PutUint32(header[:4], 8)
		copy(header[4:], boxType)
		content = append(content, header...)
	}
	return content
}
