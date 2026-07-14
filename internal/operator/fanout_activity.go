package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/comavius/kinugasa-recording/internal/media"
)

const defaultMediaActivityWindow = 15 * time.Second

type CameraMediaActivity struct {
	Protocol    string
	LastFrameAt time.Time
	Active      bool
}

type CameraMediaActivityReader interface {
	Read(context.Context, string) (CameraMediaActivity, error)
}

type HTTPFanoutActivityReader struct {
	Client       *http.Client
	Now          func() time.Time
	ActiveWindow time.Duration
}

func (reader *HTTPFanoutActivityReader) Read(ctx context.Context, endpoint string) (CameraMediaActivity, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return CameraMediaActivity{}, fmt.Errorf("create fanout status request: %w", err)
	}
	client := reader.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return CameraMediaActivity{}, fmt.Errorf("read fanout status: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return CameraMediaActivity{}, fmt.Errorf("read fanout status: HTTP %d", response.StatusCode)
	}
	var status struct {
		Processes map[string]media.ProcessSnapshot `json:"processes"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status); err != nil {
		return CameraMediaActivity{}, fmt.Errorf("decode fanout status: %w", err)
	}

	activity := CameraMediaActivity{}
	for _, candidate := range []struct {
		name, protocol string
	}{{"ingest-rist", "rist"}, {"ingest-srt", "srt"}} {
		snapshot := status.Processes[candidate.name]
		if snapshot.Frame > 0 && snapshot.LastProgressAt.After(activity.LastFrameAt) {
			activity.Protocol = candidate.protocol
			activity.LastFrameAt = snapshot.LastProgressAt
		}
	}
	if activity.LastFrameAt.IsZero() {
		return activity, nil
	}
	window := reader.ActiveWindow
	if window <= 0 {
		window = defaultMediaActivityWindow
	}
	now := time.Now().UTC()
	if reader.Now != nil {
		now = reader.Now().UTC()
	}
	activity.Active = !activity.LastFrameAt.Before(now.Add(-window)) && !activity.LastFrameAt.After(now.Add(window))
	return activity, nil
}
