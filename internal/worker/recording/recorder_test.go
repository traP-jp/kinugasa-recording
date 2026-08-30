package recording

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRecorderFinalizesFFmpegOutputAtomically(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not available")
	}
	sharedVolume := t.TempDir()
	recorder, err := NewRecorder(Config{
		SharedVolume: sharedVolume,
		FFmpegPath:   ffmpegPath,
		InputArguments: []string{
			"-re", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=30",
		},
		OutputArguments: []string{
			"-map", "0:v:0", "-c:v", "mpeg4", "-q:v", "5",
		},
		StartWait:  5 * time.Second,
		FinishWait: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	observedStart := time.Date(2026, 8, 31, 5, 0, 0, 0, time.UTC)
	observedFinish := observedStart.Add(time.Second)
	clock := []time.Time{observedStart, observedFinish}
	recorder.now = func() time.Time {
		value := clock[0]
		clock = clock[1:]
		return value
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	startedAt, err := recorder.Start(ctx, "recordings/take-1/video.mp4")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if startedAt != observedStart || !recorder.Active() {
		t.Fatalf("Start() = %v, active = %v", startedAt, recorder.Active())
	}
	finalized, err := recorder.Finish(ctx)
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if finalized.RelativePath != "recordings/take-1/video.mp4" ||
		finalized.StartedAt != observedStart || finalized.FinishedAt != observedFinish {
		t.Fatalf("Finish() = %+v", finalized)
	}
	if recorder.Active() {
		t.Fatal("recorder remains active after finalization")
	}
	finalPath := filepath.Join(sharedVolume, "recordings", "take-1", "video.mp4")
	info, err := os.Stat(finalPath)
	if err != nil {
		t.Fatalf("stat finalized recording: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("finalized recording is empty")
	}
	entries, err := os.ReadDir(filepath.Join(sharedVolume, workerMetadataName, incompleteDirName))
	if err != nil {
		t.Fatalf("read incomplete recording directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("incomplete recordings remain: %v", entries)
	}
	verify := exec.CommandContext(ctx, ffmpegPath, "-v", "error", "-i", finalPath, "-f", "null", "-")
	if output, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("decode finalized recording: %v: %s", err, output)
	}
}

func TestRecorderRejectsSecondRecordingAndEscapingPath(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not available")
	}
	recorder, err := NewRecorder(Config{
		SharedVolume:   t.TempDir(),
		FFmpegPath:     ffmpegPath,
		InputArguments: []string{"-re", "-f", "lavfi", "-i", "testsrc=size=64x64:rate=30"},
		OutputArguments: []string{
			"-map", "0:v:0", "-c:v", "mpeg4",
		},
		StartWait:  5 * time.Second,
		FinishWait: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

func TestRecorderReportsFFmpegFailureBeforeFirstFrame(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not available")
	}
	recorder, err := NewRecorder(Config{
		SharedVolume:   t.TempDir(),
		FFmpegPath:     ffmpegPath,
		InputArguments: []string{"-f", "lavfi", "-i", "no_such_filter"},
		StartWait:      5 * time.Second,
		FinishWait:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := recorder.Start(ctx, "recordings/failure.mp4"); err == nil {
		t.Fatal("Start(invalid input) error = nil")
	}
	if recorder.Active() {
		t.Fatal("recorder is active after FFmpeg startup failure")
	}
}
