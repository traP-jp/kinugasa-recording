package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	url    string
	client *http.Client
}

func NewClient(statusURL string) (*Client, error) {
	parsed, err := url.Parse(statusURL)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		return nil, fmt.Errorf("gateway status URL must be an absolute http URL")
	}
	return &Client{url: statusURL, client: &http.Client{Timeout: time.Second}}, nil
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return Status{}, err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return Status{}, fmt.Errorf("query gateway status: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return Status{}, fmt.Errorf("query gateway status: status %d", response.StatusCode)
	}
	var status Status
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status); err != nil {
		return Status{}, fmt.Errorf("decode gateway status: %w", err)
	}
	if status.State != StateWaiting && status.State != StateConnected && status.State != StateError {
		return Status{}, fmt.Errorf("decode gateway status: invalid state %q", status.State)
	}
	if status.State == StateError && (status.Code == "" || status.Error == "") {
		return Status{}, fmt.Errorf("decode gateway status: error state requires code and message")
	}
	return status, nil
}
