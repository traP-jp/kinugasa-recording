package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BinaryPath                 string
	RTPAddress                 string
	RTSPAddress                string
	APIAddress                 string
	PathName                   string
	RTPSDP                     string
	WHIPURL                    string
	WHIPToken                  string
	RecordPath                 string
	RecordPartDuration         time.Duration
	RecordSegmentDuration      time.Duration
	RunOnRecordSegmentCreate   string
	RunOnRecordSegmentComplete string
}

type Server struct {
	config     Config
	logger     *slog.Logger
	command    *exec.Cmd
	configPath string
	done       chan struct{}
	waitError  error
	client     *http.Client
}

func Start(ctx context.Context, config Config, logger *slog.Logger) (*Server, error) {
	if config.BinaryPath == "" {
		config.BinaryPath = "mediamtx"
	}
	if config.RTPAddress == "" || config.RTSPAddress == "" || config.APIAddress == "" {
		return nil, fmt.Errorf("MediaMTX RTP, RTSP, and API addresses must be set")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(config.PathName) {
		return nil, fmt.Errorf("MediaMTX path name must be a single non-empty component")
	}
	if strings.TrimSpace(config.RTPSDP) == "" {
		return nil, fmt.Errorf("RTP SDP must be set")
	}
	forwardURL, err := mediaMTXWHIPURL(config.WHIPURL)
	if err != nil {
		return nil, err
	}
	if (forwardURL == "") != (strings.TrimSpace(config.WHIPToken) == "") {
		return nil, fmt.Errorf("MediaMTX WHIP URL and bearer token must be set together")
	}
	config.WHIPURL = forwardURL
	if config.RecordPath != "" {
		if !filepath.IsAbs(config.RecordPath) {
			return nil, fmt.Errorf("MediaMTX record path must be absolute")
		}
		if !strings.Contains(config.RecordPath, "%path") {
			return nil, fmt.Errorf("MediaMTX record path must contain %%path")
		}
		if config.RunOnRecordSegmentCreate == "" || config.RunOnRecordSegmentComplete == "" {
			return nil, fmt.Errorf("MediaMTX recording hooks must be set")
		}
		if config.RecordPartDuration == 0 {
			config.RecordPartDuration = time.Second
		}
		if config.RecordSegmentDuration == 0 {
			config.RecordSegmentDuration = 24 * time.Hour
		}
		if config.RecordPartDuration < 0 || config.RecordSegmentDuration < 0 {
			return nil, fmt.Errorf("MediaMTX recording durations must be positive")
		}
	}
	if logger == nil {
		logger = slog.Default()
	}
	directory, err := os.MkdirTemp("", "kinugasa-mediamtx-*")
	if err != nil {
		return nil, fmt.Errorf("create MediaMTX runtime directory: %w", err)
	}
	configPath := filepath.Join(directory, "mediamtx.yml")
	if err := os.WriteFile(configPath, renderConfig(config), 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return nil, fmt.Errorf("write MediaMTX configuration: %w", err)
	}
	command := exec.CommandContext(ctx, config.BinaryPath, configPath)
	command.Stdout = &logWriter{logger: logger, level: slog.LevelInfo}
	command.Stderr = &logWriter{logger: logger, level: slog.LevelError}
	if err := command.Start(); err != nil {
		_ = os.RemoveAll(directory)
		return nil, fmt.Errorf("start MediaMTX: %w", err)
	}
	server := &Server{
		config:     config,
		logger:     logger,
		command:    command,
		configPath: configPath,
		done:       make(chan struct{}),
		client:     &http.Client{Timeout: time.Second},
	}
	go func() {
		server.waitError = command.Wait()
		close(server.done)
	}()
	return server, nil
}

func (s *Server) WaitReady(ctx context.Context) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiURL("/v3/paths/list"), nil)
		if err != nil {
			return err
		}
		response, err := s.client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-s.done:
			if s.waitError == nil {
				return fmt.Errorf("MediaMTX exited before readiness")
			}
			return fmt.Errorf("MediaMTX exited before readiness: %w", s.waitError)
		case <-ctx.Done():
			return fmt.Errorf("wait for MediaMTX readiness: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Server) Online(ctx context.Context) (bool, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		s.apiURL("/v3/paths/get/"+s.config.PathName),
		nil,
	)
	if err != nil {
		return false, err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return false, fmt.Errorf("query MediaMTX path: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return false, fmt.Errorf("query MediaMTX path: status %d", response.StatusCode)
	}
	var path struct {
		Ready bool `json:"ready"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&path); err != nil {
		return false, fmt.Errorf("decode MediaMTX path: %w", err)
	}
	return path.Ready, nil
}

func (s *Server) Wait() error {
	<-s.done
	_ = os.RemoveAll(filepath.Dir(s.configPath))
	return s.waitError
}

func (s *Server) RTSPURL() string {
	return "rtsp://" + s.config.RTSPAddress + "/" + s.config.PathName
}

// SetRecording toggles MediaMTX's native fMP4 recorder for the configured path.
func (s *Server) SetRecording(ctx context.Context, enabled bool) error {
	if s.config.RecordPath == "" {
		return fmt.Errorf("MediaMTX recording is not configured")
	}
	body, err := json.Marshal(struct {
		Record bool `json:"record"`
	}{Record: enabled})
	if err != nil {
		return fmt.Errorf("encode MediaMTX recording request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPatch,
		s.apiURL("/v3/config/paths/patch/"+url.PathEscape(s.config.PathName)),
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create MediaMTX recording request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("toggle MediaMTX recording: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("toggle MediaMTX recording: status %d", response.StatusCode)
	}
	return nil
}

func (s *Server) apiURL(path string) string {
	return "http://" + s.config.APIAddress + path
}

func renderConfig(config Config) []byte {
	var output bytes.Buffer
	fmt.Fprintf(&output, "logLevel: info\n")
	fmt.Fprintf(&output, "logDestinations: [stdout]\n")
	fmt.Fprintf(&output, "api: true\napiAddress: %s\n", config.APIAddress)
	fmt.Fprintf(&output, "rtsp: true\nrtspTransports: [tcp]\nrtspAddress: %s\n", config.RTSPAddress)
	fmt.Fprintf(&output, "rtmp: false\nhls: false\nwebrtc: false\nsrt: false\nplayback: false\n")
	fmt.Fprintf(&output, "paths:\n  %s:\n    source: udp+rtp://%s\n    rtpSDP: |\n", config.PathName, config.RTPAddress)
	for line := range strings.SplitSeq(strings.TrimSpace(config.RTPSDP), "\n") {
		fmt.Fprintf(&output, "      %s\n", line)
	}
	if config.WHIPURL != "" {
		fmt.Fprintf(&output, "    forward:\n")
		fmt.Fprintf(&output, "      - dest: %s\n", strconv.Quote(config.WHIPURL))
		fmt.Fprintf(&output, "        whipBearerToken: %s\n", strconv.Quote(config.WHIPToken))
	}
	if config.RecordPath != "" {
		fmt.Fprintf(&output, "    record: false\n")
		fmt.Fprintf(&output, "    recordPath: %s\n", strconv.Quote(config.RecordPath))
		fmt.Fprintf(&output, "    recordFormat: fmp4\n")
		fmt.Fprintf(&output, "    recordPartDuration: %s\n", config.RecordPartDuration)
		fmt.Fprintf(&output, "    recordMaxPartSize: 50M\n")
		fmt.Fprintf(&output, "    recordSegmentDuration: %s\n", config.RecordSegmentDuration)
		fmt.Fprintf(&output, "    recordDeleteAfter: 0s\n")
		fmt.Fprintf(&output, "    runOnRecordSegmentCreate: %s\n", strconv.Quote(config.RunOnRecordSegmentCreate))
		fmt.Fprintf(&output, "    runOnRecordSegmentComplete: %s\n", strconv.Quote(config.RunOnRecordSegmentComplete))
	}
	return output.Bytes()
}

func mediaMTXWHIPURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Host == "" {
		return "", fmt.Errorf("LiveKit WHIP URL must be an absolute HTTP or HTTPS URL")
	}
	switch endpoint.Scheme {
	case "http":
		endpoint.Scheme = "whip"
	case "https":
		endpoint.Scheme = "whips"
	default:
		return "", fmt.Errorf("LiveKit WHIP URL must be an absolute HTTP or HTTPS URL")
	}
	return endpoint.String(), nil
}

type logWriter struct {
	logger *slog.Logger
	level  slog.Level
	buffer bytes.Buffer
}

func (w *logWriter) Write(value []byte) (int, error) {
	originalLength := len(value)
	for len(value) > 0 {
		index := bytes.IndexByte(value, '\n')
		if index < 0 {
			_, _ = w.buffer.Write(value)
			break
		}
		_, _ = w.buffer.Write(value[:index])
		message := strings.TrimSpace(w.buffer.String())
		if message != "" {
			w.logger.Log(context.Background(), w.level, "MediaMTX", "message", message)
		}
		w.buffer.Reset()
		value = value[index+1:]
	}
	return originalLength, nil
}
