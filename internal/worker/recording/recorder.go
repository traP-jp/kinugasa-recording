package recording

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/traP-jp/kinugasa-recording/internal/worker/storage"
)

const (
	defaultFFmpegPath  = "ffmpeg"
	defaultStartWait   = 15 * time.Second
	defaultFinishWait  = 15 * time.Second
	incompleteDirName  = "incomplete"
	workerMetadataName = ".kinugasa-worker"
)

type Config struct {
	SharedVolume    string
	FFmpegPath      string
	InputArguments  []string
	OutputArguments []string
	StartWait       time.Duration
	FinishWait      time.Duration
}

type FinalizedRecording struct {
	RelativePath string
	StartedAt    time.Time
	FinishedAt   time.Time
}

type Recorder struct {
	mu     sync.Mutex
	config Config
	now    func() time.Time
	active *process
}

type process struct {
	command       *exec.Cmd
	standardInput io.WriteCloser
	temporaryPath string
	targetPath    string
	relativePath  string
	startedAt     time.Time
	done          chan error
	stderr        *limitedBuffer
}

func NewRecorder(config Config) (*Recorder, error) {
	if strings.TrimSpace(config.SharedVolume) == "" {
		return nil, fmt.Errorf("shared volume must be set")
	}
	if len(config.InputArguments) == 0 {
		return nil, fmt.Errorf("FFmpeg input arguments must be set")
	}
	if config.FFmpegPath == "" {
		config.FFmpegPath = defaultFFmpegPath
	}
	if config.StartWait == 0 {
		config.StartWait = defaultStartWait
	}
	if config.FinishWait == 0 {
		config.FinishWait = defaultFinishWait
	}
	if config.StartWait < 0 || config.FinishWait < 0 {
		return nil, fmt.Errorf("FFmpeg wait durations must be positive")
	}
	if len(config.OutputArguments) == 0 {
		config.OutputArguments = []string{"-map", "0:v:0", "-map", "0:a?", "-c", "copy"}
	}
	return &Recorder{config: config, now: time.Now}, nil
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
	temporaryPath, err := r.reserveTemporaryPath()
	if err != nil {
		return time.Time{}, err
	}
	started := false
	defer func() {
		if !started {
			_ = os.Remove(temporaryPath)
		}
	}()

	arguments := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-stats_period", "0.05",
		"-progress", "pipe:1",
		"-y",
	}
	arguments = append(arguments, r.config.InputArguments...)
	arguments = append(arguments, r.config.OutputArguments...)
	arguments = append(arguments, "-f", "mp4", temporaryPath)
	command := exec.Command(r.config.FFmpegPath, arguments...)
	standardOutput, err := command.StdoutPipe()
	if err != nil {
		return time.Time{}, fmt.Errorf("open FFmpeg progress pipe: %w", err)
	}
	standardInput, err := command.StdinPipe()
	if err != nil {
		return time.Time{}, fmt.Errorf("open FFmpeg control pipe: %w", err)
	}
	stderr := &limitedBuffer{limit: 64 << 10}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return time.Time{}, fmt.Errorf("start FFmpeg: %w", err)
	}
	current := &process{
		command:       command,
		standardInput: standardInput,
		temporaryPath: temporaryPath,
		targetPath:    targetPath,
		relativePath:  relativePath,
		done:          make(chan error, 1),
		stderr:        stderr,
	}
	go func() {
		current.done <- command.Wait()
		close(current.done)
	}()
	firstFrame := scanFirstFrame(standardOutput, r.now)
	timer := time.NewTimer(r.config.StartWait)
	defer timer.Stop()
	select {
	case observedAt, ok := <-firstFrame:
		if !ok {
			return time.Time{}, r.stopFailedStart(current, "FFmpeg progress ended before the first frame")
		}
		current.startedAt = observedAt
		r.active = current
		started = true
		return observedAt, nil
	case waitError := <-current.done:
		return time.Time{}, processError("FFmpeg exited before the first frame", waitError, stderr.String())
	case <-timer.C:
		return time.Time{}, r.stopFailedStart(current, "timed out waiting for the first FFmpeg frame")
	case <-ctx.Done():
		return time.Time{}, r.stopFailedStart(current, "recording start canceled: "+ctx.Err().Error())
	}
}

func (r *Recorder) Finish(ctx context.Context) (FinalizedRecording, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return FinalizedRecording{}, fmt.Errorf("no recording is active")
	}
	current := r.active
	if _, err := io.WriteString(current.standardInput, "q\n"); err != nil {
		_ = current.command.Process.Kill()
		<-current.done
		r.active = nil
		return FinalizedRecording{}, fmt.Errorf("request FFmpeg finalization: %w", err)
	}
	_ = current.standardInput.Close()
	timer := time.NewTimer(r.config.FinishWait)
	defer timer.Stop()
	select {
	case waitError := <-current.done:
		r.active = nil
		if waitError != nil {
			return FinalizedRecording{}, processError("finalize FFmpeg output", waitError, current.stderr.String())
		}
	case <-timer.C:
		_ = current.command.Process.Kill()
		<-current.done
		r.active = nil
		return FinalizedRecording{}, fmt.Errorf("timed out waiting for FFmpeg finalization")
	case <-ctx.Done():
		_ = current.command.Process.Kill()
		<-current.done
		r.active = nil
		return FinalizedRecording{}, fmt.Errorf("recording finalization canceled: %w", ctx.Err())
	}
	if err := syncFile(current.temporaryPath); err != nil {
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
	if err := os.Rename(current.temporaryPath, current.targetPath); err != nil {
		return FinalizedRecording{}, fmt.Errorf("publish finalized recording: %w", err)
	}
	if err := syncDirectory(filepath.Dir(current.targetPath)); err != nil {
		return FinalizedRecording{}, err
	}
	finishedAt := r.now().UTC()
	return FinalizedRecording{
		RelativePath: current.relativePath,
		StartedAt:    current.startedAt,
		FinishedAt:   finishedAt,
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
	current := r.active
	_ = current.command.Process.Kill()
	waitError := <-current.done
	r.active = nil
	removeError := os.Remove(current.temporaryPath)
	if removeError != nil && !errors.Is(removeError, os.ErrNotExist) {
		return fmt.Errorf("remove aborted recording: %w", removeError)
	}
	if waitError != nil {
		var exitError *exec.ExitError
		if !errors.As(waitError, &exitError) {
			return fmt.Errorf("abort FFmpeg recording: %w", waitError)
		}
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

func (r *Recorder) reserveTemporaryPath() (string, error) {
	incompleteDirectory := filepath.Join(r.config.SharedVolume, workerMetadataName, incompleteDirName)
	if err := os.MkdirAll(incompleteDirectory, 0o700); err != nil {
		return "", fmt.Errorf("create incomplete recording directory: %w", err)
	}
	info, err := os.Lstat(incompleteDirectory)
	if err != nil {
		return "", fmt.Errorf("inspect incomplete recording directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("incomplete recording path must be a directory and not a symlink")
	}
	temporary, err := os.CreateTemp(incompleteDirectory, "recording-*.partial")
	if err != nil {
		return "", fmt.Errorf("reserve incomplete recording path: %w", err)
	}
	path := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close incomplete recording file: %w", err)
	}
	return path, nil
}

func (r *Recorder) stopFailedStart(current *process, reason string) error {
	_ = current.command.Process.Kill()
	waitError := <-current.done
	return processError(reason, waitError, current.stderr.String())
}

func scanFirstFrame(progress io.Reader, now func() time.Time) <-chan time.Time {
	firstFrame := make(chan time.Time, 1)
	go func() {
		defer close(firstFrame)
		scanner := bufio.NewScanner(progress)
		reported := false
		for scanner.Scan() {
			if reported {
				continue
			}
			key, value, found := strings.Cut(scanner.Text(), "=")
			if !found || key != "frame" {
				continue
			}
			frames, err := strconv.ParseUint(value, 10, 64)
			if err == nil && frames > 0 {
				firstFrame <- now().UTC()
				reported = true
			}
		}
	}()
	return firstFrame
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

func processError(operation string, waitError error, stderr string) error {
	detail := strings.TrimSpace(stderr)
	if waitError == nil {
		if detail == "" {
			return fmt.Errorf("%s", operation)
		}
		return fmt.Errorf("%s: %s", operation, detail)
	}
	if detail == "" {
		return fmt.Errorf("%s: %w", operation, waitError)
	}
	return fmt.Errorf("%s: %w: %s", operation, waitError, detail)
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	originalLength := len(value)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.buffer.Write(value)
	}
	return originalLength, nil
}

func (b *limitedBuffer) String() string {
	return b.buffer.String()
}
