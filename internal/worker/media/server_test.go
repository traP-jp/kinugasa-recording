package media

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRenderConfigDefinesRTPSourceAndLoopbackServers(t *testing.T) {
	config := Config{
		RTPAddress:                 "0.0.0.0:8000",
		RTSPAddress:                "127.0.0.1:8554",
		APIAddress:                 "127.0.0.1:9997",
		PathName:                   "camera",
		RTPSDP:                     testSDP(8000),
		WHIPURL:                    "whip://livekit-ingress.example.com/w",
		WHIPToken:                  "stream-key",
		RecordPath:                 "/recordings/incomplete/recording-%s-%f",
		RecordPartDuration:         time.Second,
		RecordSegmentDuration:      24 * time.Hour,
		RunOnRecordSegmentCreate:   `"/usr/local/bin/video-worker" recording-segment-created`,
		RunOnRecordSegmentComplete: `"/usr/local/bin/video-worker" recording-segment-completed`,
	}
	rendered := string(renderConfig(config))
	for _, expected := range []string{
		"rtspTransports: [tcp]",
		"source: udp+rtp://0.0.0.0:8000",
		"apiAddress: 127.0.0.1:9997",
		"a=rtpmap:96 H264/90000",
		`dest: "whip://livekit-ingress.example.com/w"`,
		`whipBearerToken: "stream-key"`,
		`recordFormat: fmp4`,
		`recordPartDuration: 1s`,
		`recordSegmentDuration: 24h0m0s`,
		`runOnRecordSegmentCreate: "\"/usr/local/bin/video-worker\" recording-segment-created"`,
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("rendered config does not contain %q:\n%s", expected, rendered)
		}
	}
}

func TestSetRecordingPatchesConfiguredPath(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPatch || request.URL.Path != "/v3/config/paths/patch/camera" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		buffer := new(strings.Builder)
		_, _ = io.Copy(buffer, request.Body)
		received = buffer.String()
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	mediaServer := &Server{
		config: Config{APIAddress: endpoint.Host, PathName: "camera", RecordPath: "/recordings/recording"},
		client: server.Client(),
	}
	if err := mediaServer.SetRecording(context.Background(), true); err != nil {
		t.Fatalf("SetRecording(true) error = %v", err)
	}
	if received != `{"record":true}` {
		t.Fatalf("SetRecording(true) body = %q", received)
	}
}

func TestStartRejectsRecordPathWithoutMediaPathTemplate(t *testing.T) {
	_, err := Start(context.Background(), Config{
		BinaryPath:                 "mediamtx",
		RTPAddress:                 "127.0.0.1:8000",
		RTSPAddress:                "127.0.0.1:8554",
		APIAddress:                 "127.0.0.1:9997",
		PathName:                   "camera",
		RTPSDP:                     testSDP(8000),
		RecordPath:                 "/recordings/recording-%s-%f",
		RunOnRecordSegmentCreate:   "create-hook",
		RunOnRecordSegmentComplete: "complete-hook",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "%path") {
		t.Fatalf("Start(record path without %%path) error = %v", err)
	}
}

func TestMediaMTXWHIPURL(t *testing.T) {
	tests := map[string]string{
		"http://livekit-ingress.example.com/w":  "whip://livekit-ingress.example.com/w",
		"https://livekit-ingress.example.com/w": "whips://livekit-ingress.example.com/w",
		"":                                      "",
	}
	for input, expected := range tests {
		actual, err := mediaMTXWHIPURL(input)
		if err != nil {
			t.Fatalf("mediaMTXWHIPURL(%q) error = %v", input, err)
		}
		if actual != expected {
			t.Fatalf("mediaMTXWHIPURL(%q) = %q, want %q", input, actual, expected)
		}
	}
	for _, input := range []string{"ws://livekit.example.com/w", "relative/path", "://bad"} {
		if _, err := mediaMTXWHIPURL(input); err == nil {
			t.Fatalf("mediaMTXWHIPURL(%q) error = nil", input)
		}
	}
}

func TestServerStartsAndReportsOfflinePath(t *testing.T) {
	binary, err := exec.LookPath("mediamtx")
	if err != nil {
		t.Skip("mediamtx is not available")
	}
	rtpPort := freeUDPPort(t)
	ctx, cancel := context.WithCancel(context.Background())
	server, err := Start(ctx, Config{
		BinaryPath:  binary,
		RTPAddress:  "127.0.0.1:" + strconv.Itoa(rtpPort),
		RTSPAddress: freeTCPAddress(t),
		APIAddress:  freeTCPAddress(t),
		PathName:    "camera",
		RTPSDP:      testSDP(rtpPort),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		cancel()
		t.Fatalf("Start() error = %v", err)
	}
	readyContext, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readyCancel()
	if err := server.WaitReady(readyContext); err != nil {
		cancel()
		_ = server.Wait()
		t.Fatalf("WaitReady() error = %v", err)
	}
	online, err := server.Online(readyContext)
	if err != nil {
		cancel()
		_ = server.Wait()
		t.Fatalf("Online() error = %v", err)
	}
	if online {
		t.Fatal("Online() = true without an RTP publisher")
	}
	cancel()
	_ = server.Wait()
}

func freeTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free TCP port: %v", err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	connection, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free UDP port: %v", err)
	}
	port := connection.LocalAddr().(*net.UDPAddr).Port
	_ = connection.Close()
	return port
}

func testSDP(port int) string {
	return "v=0\n" +
		"o=- 0 0 IN IP4 127.0.0.1\n" +
		"s=Kinugasa Test\n" +
		"c=IN IP4 127.0.0.1\n" +
		"t=0 0\n" +
		"m=video " + strconv.Itoa(port) + " RTP/AVP 96\n" +
		"a=rtpmap:96 H264/90000\n" +
		"a=recvonly"
}
