package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type UploaderStatus struct {
	Phase         string
	UploadedFiles int32
}

type UploaderStatusReader interface {
	Read(context.Context, string) (UploaderStatus, error)
}

type HTTPUploaderStatusReader struct {
	Client *http.Client
}

func (reader *HTTPUploaderStatusReader) Read(ctx context.Context, endpoint string) (UploaderStatus, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return UploaderStatus{}, fmt.Errorf("create uploader status request: %w", err)
	}
	client := reader.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return UploaderStatus{}, fmt.Errorf("read uploader status: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return UploaderStatus{}, fmt.Errorf("read uploader status: HTTP %d", response.StatusCode)
	}
	var payload struct {
		Uploader struct {
			Phase    string            `json:"phase"`
			Uploaded map[string]string `json:"uploaded"`
		} `json:"uploader"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return UploaderStatus{}, fmt.Errorf("decode uploader status: %w", err)
	}
	return UploaderStatus{Phase: payload.Uploader.Phase, UploadedFiles: int32(len(payload.Uploader.Uploaded))}, nil
}
