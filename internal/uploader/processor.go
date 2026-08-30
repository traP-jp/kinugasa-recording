package uploader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/traP-jp/kinugasa-recording/internal/shared/uploadqueue"
	"github.com/traP-jp/kinugasa-recording/internal/shared/workerprotocol"
	"github.com/traP-jp/kinugasa-recording/internal/worker/storage"
)

type Queue interface {
	List() ([]uploadqueue.Manifest, error)
	Save(uploadqueue.Manifest) error
}

type ObjectStore interface {
	Put(context.Context, string, io.Reader, int64, string, string) error
}

type Processor struct {
	sharedVolume string
	queue        Queue
	objects      ObjectStore
	maxAttempts  int
}

func NewProcessor(sharedVolume string, queue Queue, objects ObjectStore, maxAttempts int) (*Processor, error) {
	if strings.TrimSpace(sharedVolume) == "" || queue == nil || objects == nil {
		return nil, fmt.Errorf("uploader volume, queue, and object store must be set")
	}
	if maxAttempts <= 0 {
		return nil, fmt.Errorf("uploader maximum attempts must be positive")
	}
	return &Processor{sharedVolume: sharedVolume, queue: queue, objects: objects, maxAttempts: maxAttempts}, nil
}

func (p *Processor) ProcessPending(ctx context.Context) (int, error) {
	manifests, err := p.queue.List()
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, manifest := range manifests {
		if manifest.Terminal() {
			continue
		}
		processed++
		if err := p.process(ctx, manifest); err != nil {
			return processed, err
		}
	}
	return processed, nil
}

func (p *Processor) process(ctx context.Context, manifest uploadqueue.Manifest) error {
	manifest.State = uploadqueue.StateUploading
	manifest.Attempts++
	if err := p.queue.Save(manifest); err != nil {
		return fmt.Errorf("persist upload attempt: %w", err)
	}
	if err := p.upload(ctx, &manifest); err != nil {
		if manifest.Attempts >= p.maxAttempts {
			manifest.State = uploadqueue.StateErrored
			manifest.Error = "object upload failed after the configured retry limit"
			if saveError := p.queue.Save(manifest); saveError != nil {
				return fmt.Errorf("upload failed: %v; persist terminal upload failure: %w", err, saveError)
			}
			return nil
		}
		return err
	}
	manifest.State = uploadqueue.StateCompleted
	manifest.Error = ""
	if err := p.queue.Save(manifest); err != nil {
		return fmt.Errorf("persist completed upload: %w", err)
	}
	return nil
}

func (p *Processor) upload(ctx context.Context, manifest *uploadqueue.Manifest) error {
	filePath, err := storage.ResolvePath(p.sharedVolume, manifest.RelativePath)
	if err != nil {
		return fmt.Errorf("resolve upload file: %w", err)
	}
	file, err := openRegularFile(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return fmt.Errorf("hash upload file: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind upload file: %w", err)
	}
	objectKey, err := objectKey(manifest.RelativePath, digest)
	if err != nil {
		return err
	}
	verificationHash := sha256.New()
	reader := io.TeeReader(io.LimitReader(file, size+1), verificationHash)
	if err := p.objects.Put(ctx, objectKey, reader, size, digest, "video/mp4"); err != nil {
		return fmt.Errorf("put recording object: %w", err)
	}
	if hex.EncodeToString(verificationHash.Sum(nil)) != digest {
		return fmt.Errorf("upload file changed while it was being read")
	}
	manifest.ObjectKey = objectKey
	manifest.SHA256 = digest
	manifest.Size = size
	return nil
}

func objectKey(relativePath, digest string) (string, error) {
	if err := workerprotocol.ValidateRelativePath("relative_path", relativePath); err != nil {
		return "", err
	}
	if path.Base(relativePath) != "video.mp4" {
		return "", fmt.Errorf("recording relative path must end in video.mp4")
	}
	return path.Join(path.Dir(relativePath), digest+"-video.mp4"), nil
}
