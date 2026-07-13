package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

var ErrObjectConflict = errors.New("object exists with a different digest")

type s3UploadAPI interface {
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

type UploadConfig struct {
	Root, Bucket, Session, Take, Camera string
	PollInterval, RetryMax              time.Duration
}

type UploadState struct {
	Phase     string            `json:"phase"`
	Uploaded  map[string]string `json:"uploaded"`
	LastError string            `json:"lastError,omitempty"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

type Uploader struct {
	client s3UploadAPI
	config UploadConfig
	state  UploadState
}

func NewUploader(client s3UploadAPI, config UploadConfig) (*Uploader, error) {
	if client == nil || config.Root == "" || config.Bucket == "" {
		return nil, fmt.Errorf("S3 client, recording root, and bucket are required")
	}
	for field, value := range map[string]string{"session": config.Session, "take": config.Take, "camera": config.Camera} {
		if value == "" || strings.ContainsAny(value, `/\\`) {
			return nil, fmt.Errorf("invalid %s name", field)
		}
	}
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.RetryMax <= 0 {
		config.RetryMax = 30 * time.Second
	}
	uploader := &Uploader{client: client, config: config}
	uploader.state = UploadState{Phase: "Uploading", Uploaded: map[string]string{}, UpdatedAt: time.Now().UTC()}
	if err := uploader.loadState(); err != nil {
		return nil, fmt.Errorf("load uploader state: %w", err)
	}
	if uploader.state.Uploaded == nil {
		uploader.state.Uploaded = map[string]string{}
	}
	return uploader, nil
}

func (uploader *Uploader) Run(ctx context.Context) error {
	delay := uploader.config.PollInterval
	for {
		complete, err := uploader.Sync(ctx)
		if err != nil {
			uploader.state.Phase = "Retrying"
			uploader.state.LastError = err.Error()
			uploader.state.UpdatedAt = time.Now().UTC()
			_ = uploader.writeState("uploader.json")
			if errors.Is(err, ErrObjectConflict) || isPermanentS3Error(err) {
				uploader.state.Phase = "Failed"
				_ = uploader.writeState("uploader.json")
				return err
			}
		} else if complete {
			return nil
		} else {
			delay = uploader.config.PollInterval
		}
		wait := delay + time.Duration(rand.Int64N(max(1, int64(delay/2))))
		if err != nil {
			delay = min(delay*2, uploader.config.RetryMax)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Sync uploads every currently ready segment and reports the strict completion condition.
func (uploader *Uploader) Sync(ctx context.Context) (bool, error) {
	files, err := filepath.Glob(filepath.Join(uploader.config.Root, "ready", "segment-*.ts"))
	if err != nil {
		return false, err
	}
	sort.Strings(files)
	for _, file := range files {
		name := filepath.Base(file)
		digest, err := fileDigest(file)
		if err != nil {
			return false, err
		}
		if uploader.state.Uploaded[name] == digest {
			continue
		}
		key := strings.Join([]string{uploader.config.Session, uploader.config.Take, uploader.config.Camera, name}, "/")
		head, err := uploader.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(uploader.config.Bucket), Key: aws.String(key)})
		if err == nil {
			if head.Metadata["sha256"] != digest {
				return false, fmt.Errorf("%w: %s", ErrObjectConflict, key)
			}
		} else if isObjectNotFound(err) {
			body, openErr := os.Open(file)
			if openErr != nil {
				return false, openErr
			}
			_, putErr := uploader.client.PutObject(ctx, &s3.PutObjectInput{
				Bucket: aws.String(uploader.config.Bucket), Key: aws.String(key), Body: body,
				ContentType: aws.String("video/mp2t"), Metadata: map[string]string{"sha256": digest}, IfNoneMatch: aws.String("*"),
			})
			closeErr := body.Close()
			if putErr != nil {
				return false, fmt.Errorf("upload %s: %w", name, putErr)
			}
			if closeErr != nil {
				return false, closeErr
			}
		} else {
			return false, fmt.Errorf("inspect %s: %w", name, err)
		}
		uploader.state.Uploaded[name] = digest
		uploader.state.Phase = "Uploading"
		uploader.state.LastError = ""
		uploader.state.UpdatedAt = time.Now().UTC()
		if err := uploader.writeState("uploader.json"); err != nil {
			return false, err
		}
	}

	if _, err := os.Stat(filepath.Join(uploader.config.Root, "state", "recorder.done")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
	}
	parts, err := filepath.Glob(filepath.Join(uploader.config.Root, "staging", "*.part"))
	if err != nil || len(parts) > 0 || len(uploader.state.Uploaded) != len(files) {
		return false, err
	}
	uploader.state.Phase = "Completed"
	uploader.state.UpdatedAt = time.Now().UTC()
	if err := uploader.writeState("uploader.json"); err != nil {
		return false, err
	}
	return true, uploader.writeState("uploader.done")
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func isObjectNotFound(err error) bool {
	var apiError smithy.APIError
	return errors.As(err, &apiError) && (apiError.ErrorCode() == "NotFound" || apiError.ErrorCode() == "NoSuchKey")
}

func isPermanentS3Error(err error) bool {
	var apiError smithy.APIError
	if !errors.As(err, &apiError) {
		return false
	}
	switch apiError.ErrorCode() {
	case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch", "NoSuchBucket":
		return true
	default:
		return false
	}
}

func (uploader *Uploader) statePath(name string) string {
	return filepath.Join(uploader.config.Root, "state", name)
}

func (uploader *Uploader) loadState() error {
	contents, err := os.ReadFile(uploader.statePath("uploader.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(contents, &uploader.state)
}

func (uploader *Uploader) writeState(name string) error {
	if err := os.MkdirAll(filepath.Join(uploader.config.Root, "state"), 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Join(uploader.config.Root, "state"), ".uploader-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := json.NewEncoder(temporary).Encode(uploader.state); err != nil {
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
	return os.Rename(temporaryName, uploader.statePath(name))
}
