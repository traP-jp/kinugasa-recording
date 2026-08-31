//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/traP-jp/kinugasa-recording/internal/gateway"
	"github.com/traP-jp/kinugasa-recording/internal/worker/media"
)

func TestSyntheticRISTReachesWorkerRTSP(t *testing.T) {
	ffmpeg := requireBinary(t, "ffmpeg")
	ffprobe := requireBinary(t, "ffprobe")
	ristreceiver := requireBinary(t, "ristreceiver")
	mediamtx := requireBinary(t, "mediamtx")
	udpPorts := freeUDPPorts(t, 6)
	tcpAddresses := freeTCPAddresses(t, 3)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var serviceLog bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&serviceLog, nil))
	rtpSDP := fmt.Sprintf(`v=0
o=- 0 0 IN IP4 127.0.0.1
s=Kinugasa synthetic integration input
c=IN IP4 127.0.0.1
t=0 0
m=video %d RTP/AVP 96
a=rtpmap:96 H264/90000
a=rtcp:%d
a=recvonly`, udpPorts[2], udpPorts[3])
	mediaServer, err := media.Start(ctx, media.Config{
		BinaryPath:  mediamtx,
		RTPAddress:  "127.0.0.1:" + strconv.Itoa(udpPorts[2]),
		RTSPAddress: tcpAddresses[0],
		APIAddress:  tcpAddresses[1],
		PathName:    "camera",
		RTPSDP:      rtpSDP,
	}, logger)
	if err != nil {
		t.Fatalf("start MediaMTX: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = mediaServer.Wait()
	})
	readyContext, readyCancel := context.WithTimeout(ctx, 10*time.Second)
	defer readyCancel()
	if err := mediaServer.WaitReady(readyContext); err != nil {
		t.Fatalf("wait for MediaMTX: %v", err)
	}

	gatewayDone := make(chan error, 1)
	go func() {
		gatewayDone <- gateway.Run(ctx, gateway.Config{
			FFmpegPath:       ffmpeg,
			FFprobePath:      ffprobe,
			RISTReceiverPath: ristreceiver,
			RISTAddress:      "127.0.0.1:" + strconv.Itoa(udpPorts[0]),
			RISTOutputURL:    "udp://127.0.0.1:" + strconv.Itoa(udpPorts[1]),
			VideoRTPURL:      fmt.Sprintf("rtp://127.0.0.1:%d?rtcpport=%d", udpPorts[2], udpPorts[3]),
			AudioRTPURL:      fmt.Sprintf("rtp://127.0.0.1:%d?rtcpport=%d", udpPorts[4], udpPorts[5]),
			StatusAddress:    tcpAddresses[2],
			RetryInterval:    100 * time.Millisecond,
		}, logger)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-gatewayDone:
			if err != nil {
				t.Errorf("stop gateway: %v; log: %s", err, serviceLog.String())
			}
		case <-time.After(5 * time.Second):
			t.Error("gateway did not stop")
		}
	})

	statusClient := waitForGateway(t, ctx, "http://"+tcpAddresses[2]+"/status")
	var senderLog bytes.Buffer
	sender := exec.CommandContext(ctx, ffmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-re",
		"-f", "lavfi", "-i", "testsrc=size=320x180:rate=30",
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-pix_fmt", "yuv420p", "-g", "30", "-f", "mpegts", "-rist_profile", "main",
		"rist://127.0.0.1:"+strconv.Itoa(udpPorts[0]),
	)
	sender.Stderr = &senderLog
	if err := sender.Start(); err != nil {
		t.Fatalf("start synthetic RIST sender: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = sender.Wait()
	})

	waitContext, waitCancel := context.WithTimeout(ctx, 20*time.Second)
	defer waitCancel()
	for {
		status, statusError := statusClient.Status(waitContext)
		if statusError == nil && status.State == gateway.StateConnected {
			break
		}
		select {
		case <-waitContext.Done():
			t.Fatalf("gateway did not connect: %v; sender log: %s; service log: %s", statusError, senderLog.String(), serviceLog.String())
		case <-time.After(100 * time.Millisecond):
		}
	}
	for {
		online, onlineError := mediaServer.Online(waitContext)
		if onlineError == nil && online {
			break
		}
		select {
		case <-waitContext.Done():
			t.Fatalf("MediaMTX input did not become ready: %v", onlineError)
		case <-time.After(100 * time.Millisecond):
		}
	}

	var receiverLog bytes.Buffer
	receiver := exec.CommandContext(waitContext, ffmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-rtsp_transport", "tcp",
		"-i", mediaServer.RTSPURL(), "-map", "0:v:0", "-frames:v", "30", "-f", "null", "-",
	)
	receiver.Stderr = &receiverLog
	if err := receiver.Run(); err != nil {
		t.Fatalf("decode 30 synthetic frames from worker RTSP: %v; log: %s", err, receiverLog.String())
	}
}

func requireBinary(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("%s is required for integration tests: %v", name, err)
	}
	return path
}

func waitForGateway(t *testing.T, ctx context.Context, statusURL string) *gateway.Client {
	t.Helper()
	client, err := gateway.NewClient(statusURL)
	if err != nil {
		t.Fatalf("create gateway client: %v", err)
	}
	waitContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		if _, err := client.Status(waitContext); err == nil {
			return client
		}
		select {
		case <-waitContext.Done():
			t.Fatalf("gateway status server did not start: %v", waitContext.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func freeUDPPorts(t *testing.T, count int) []int {
	t.Helper()
	connections := make([]net.PacketConn, 0, count)
	ports := make([]int, 0, count)
	for range count {
		connection, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("reserve UDP port: %v", err)
		}
		connections = append(connections, connection)
		ports = append(ports, connection.LocalAddr().(*net.UDPAddr).Port)
	}
	for _, connection := range connections {
		_ = connection.Close()
	}
	return ports
}

func freeTCPAddresses(t *testing.T, count int) []string {
	t.Helper()
	listeners := make([]net.Listener, 0, count)
	addresses := make([]string, 0, count)
	for range count {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("reserve TCP port: %v", err)
		}
		listeners = append(listeners, listener)
		addresses = append(addresses, listener.Addr().String())
	}
	for _, listener := range listeners {
		_ = listener.Close()
	}
	return addresses
}
