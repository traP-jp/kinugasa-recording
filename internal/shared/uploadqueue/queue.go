package uploadqueue

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/traP-jp/kinugasa-recording/internal/shared/workerprotocol"
	"github.com/traP-jp/kinugasa-recording/internal/worker/storage"
)

const (
	metadataDirectory = ".kinugasa-worker"
	uploadDirectory   = "uploads"
	maxManifestSize   = 1 << 20
)

type Queue struct {
	mu               sync.Mutex
	sharedVolume     string
	directory        string
	sessionID        string
	cameraIdentityID string
}

func Open(sharedVolume, sessionID, cameraIdentityID string) (*Queue, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(cameraIdentityID) == "" {
		return nil, fmt.Errorf("upload queue session and camera identity IDs must be set")
	}
	root, err := filepath.Abs(sharedVolume)
	if err != nil {
		return nil, fmt.Errorf("resolve upload queue volume: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve upload queue volume symlinks: %w", err)
	}
	directory := filepath.Join(root, metadataDirectory, uploadDirectory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create upload queue: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("upload queue must be a directory and not a symlink")
	}
	return &Queue{
		sharedVolume: root, directory: directory,
		sessionID: sessionID, cameraIdentityID: cameraIdentityID,
	}, nil
}

func (q *Queue) Publish(takeID, relativePath string, startedAt, finishedAt time.Time) error {
	if strings.TrimSpace(takeID) == "" {
		return fmt.Errorf("take ID must be set")
	}
	if err := workerprotocol.ValidateRelativePath("relative_path", relativePath); err != nil {
		return err
	}
	if !finishedAt.After(startedAt) && !finishedAt.Equal(startedAt) {
		return fmt.Errorf("recording finish time must not precede start time")
	}
	resolved, err := storage.ResolvePath(q.sharedVolume, relativePath)
	if err != nil {
		return err
	}
	if info, err := os.Stat(resolved); err != nil {
		return fmt.Errorf("stat finalized recording: %w", err)
	} else if !info.Mode().IsRegular() {
		return fmt.Errorf("finalized recording must be a regular file")
	}
	manifest := Manifest{
		SchemaVersion: schemaVersion, SessionID: q.sessionID, CameraIdentityID: q.cameraIdentityID,
		TakeID: takeID, RelativePath: relativePath, MediaType: "video/mp4",
		StartedAt: startedAt.UTC(), FinishedAt: finishedAt.UTC(), State: StatePending,
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	path := q.manifestPath(takeID)
	if existing, err := readManifest(path); err == nil {
		if immutableEqual(existing, manifest) {
			return nil
		}
		return fmt.Errorf("take %q already has a different upload manifest", takeID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return q.write(path, manifest)
}

func (q *Queue) List() ([]Manifest, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	entries, err := os.ReadDir(q.directory)
	if err != nil {
		return nil, fmt.Errorf("read upload queue: %w", err)
	}
	manifests := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		manifest, err := readManifest(filepath.Join(q.directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		if err := q.validate(manifest); err != nil {
			return nil, fmt.Errorf("validate upload manifest %q: %w", entry.Name(), err)
		}
		manifests = append(manifests, manifest)
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].FinishedAt.Before(manifests[j].FinishedAt) })
	return manifests, nil
}

func (q *Queue) Save(manifest Manifest) error {
	if err := q.validate(manifest); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	path := q.manifestPath(manifest.TakeID)
	existing, err := readManifest(path)
	if err != nil {
		return err
	}
	if !immutableEqual(existing, manifest) {
		return fmt.Errorf("upload manifest identity fields are immutable")
	}
	if existing.Terminal() {
		if !sameTerminalResult(existing, manifest) {
			return fmt.Errorf("terminal upload result is immutable")
		}
		if existing.ReportedAt != nil && (manifest.ReportedAt == nil || !existing.ReportedAt.Equal(*manifest.ReportedAt)) {
			return fmt.Errorf("upload report acknowledgement is immutable")
		}
	}
	return q.write(path, manifest)
}

func (q *Queue) manifestPath(takeID string) string {
	digest := sha256.Sum256([]byte(takeID))
	return filepath.Join(q.directory, hex.EncodeToString(digest[:])+".json")
}

func (q *Queue) validate(manifest Manifest) error {
	if manifest.SchemaVersion != schemaVersion || manifest.SessionID != q.sessionID ||
		manifest.CameraIdentityID != q.cameraIdentityID || strings.TrimSpace(manifest.TakeID) == "" {
		return fmt.Errorf("manifest identity does not match this upload queue")
	}
	if err := workerprotocol.ValidateRelativePath("relative_path", manifest.RelativePath); err != nil {
		return err
	}
	if manifest.MediaType != "video/mp4" || manifest.StartedAt.IsZero() || manifest.FinishedAt.Before(manifest.StartedAt) {
		return fmt.Errorf("manifest media type or timestamps are invalid")
	}
	switch manifest.State {
	case StatePending, StateUploading:
		if manifest.Error != "" {
			return fmt.Errorf("nonterminal manifest must not contain an error")
		}
	case StateCompleted:
		if manifest.ObjectKey == "" || len(manifest.SHA256) != 64 || manifest.Size < 0 || manifest.Error != "" {
			return fmt.Errorf("completed manifest metadata is invalid")
		}
		if _, err := hex.DecodeString(manifest.SHA256); err != nil {
			return fmt.Errorf("completed manifest SHA-256 is invalid")
		}
	case StateErrored:
		if strings.TrimSpace(manifest.Error) == "" {
			return fmt.Errorf("errored manifest must contain an error")
		}
	default:
		return fmt.Errorf("unknown upload state %q", manifest.State)
	}
	return nil
}

func (q *Queue) write(path string, manifest Manifest) error {
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode upload manifest: %w", err)
	}
	temporary, err := os.CreateTemp(q.directory, "manifest-*.tmp")
	if err != nil {
		return err
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
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	committed = true
	return syncDirectory(q.directory)
}

func readManifest(path string) (Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, maxManifestSize+1))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode upload manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, fmt.Errorf("decode upload manifest: trailing data")
	}
	return manifest, nil
}

func immutableEqual(left, right Manifest) bool {
	return left.SchemaVersion == right.SchemaVersion && left.SessionID == right.SessionID &&
		left.CameraIdentityID == right.CameraIdentityID && left.TakeID == right.TakeID &&
		left.RelativePath == right.RelativePath && left.MediaType == right.MediaType &&
		left.StartedAt.Equal(right.StartedAt) && left.FinishedAt.Equal(right.FinishedAt)
}

func sameTerminalResult(left, right Manifest) bool {
	return immutableEqual(left, right) && left.State == right.State && left.Attempts == right.Attempts &&
		left.ObjectKey == right.ObjectKey && left.SHA256 == right.SHA256 && left.Size == right.Size &&
		left.Error == right.Error
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
