//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/traP-jp/kinugasa-recording/internal/worker/media"
	"github.com/traP-jp/kinugasa-recording/internal/worker/recording"
)

func TestSyntheticRISTReachesWorkerRTSP(t *testing.T) {
	ffmpeg := requireBinary(t, "ffmpeg")
	ffprobe := requireBinary(t, "ffprobe")
	ristreceiver := requireBinary(t, "ristreceiver")
	mediamtx := requireBinary(t, "mediamtx")
	udpPorts := freeUDPPorts(t, 3)
	tcpAddresses := freeTCPAddresses(t, 2)
	sharedVolume := t.TempDir()
	t.Setenv("KINUGASA_SHARED_VOLUME", sharedVolume)
	t.Setenv("KINUGASA_HOOK_HELPER", "1")
	recordPath, err := recording.MediaMTXRecordPath(sharedVolume)
	if err != nil {
		t.Fatalf("prepare MediaMTX recording path: %v", err)
	}
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve integration test executable: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var serviceLog bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&serviceLog, nil))
	bridgeDone := make(chan error, 1)
	go func() {
		bridgeDone <- media.RunRTPMPEGTSBridge(
			ctx,
			"127.0.0.1:"+strconv.Itoa(udpPorts[1]),
			"127.0.0.1:"+strconv.Itoa(udpPorts[2]),
			logger,
		)
	}()
	mediaServer, err := media.Start(ctx, media.Config{
		BinaryPath:                 mediamtx,
		MPEGTSAddress:              "127.0.0.1:" + strconv.Itoa(udpPorts[2]),
		RTSPAddress:                tcpAddresses[0],
		APIAddress:                 tcpAddresses[1],
		PathName:                   "camera",
		RecordPath:                 recordPath,
		RecordPartDuration:         500 * time.Millisecond,
		RecordSegmentDuration:      24 * time.Hour,
		RunOnRecordSegmentCreate:   testHookCommand(testExecutable, recording.SegmentCreatedHookArgument),
		RunOnRecordSegmentComplete: testHookCommand(testExecutable, recording.SegmentCompletedHookArgument),
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
		t.Fatalf("wait for MediaMTX: %v; service log: %s", err, serviceLog.String())
	}

	gateway := exec.CommandContext(ctx, ristreceiver,
		"-i", "rist://@127.0.0.1:"+strconv.Itoa(udpPorts[0]),
		"-o", "rtp://127.0.0.1:"+strconv.Itoa(udpPorts[1]),
		"-p", "1",
		"-S", "1000",
	)
	gateway.Stdout = &serviceLog
	gateway.Stderr = &serviceLog
	if err := gateway.Start(); err != nil {
		t.Fatalf("start ristreceiver gateway: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		if err := gateway.Wait(); err != nil && ctx.Err() == nil {
			t.Errorf("stop ristreceiver gateway: %v; log: %s", err, serviceLog.String())
		}
	})

	var senderLog bytes.Buffer
	sender := exec.CommandContext(ctx, ffmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-re",
		"-f", "lavfi", "-i", "testsrc=size=320x180:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=48000",
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-pix_fmt", "yuv420p", "-g", "30", "-c:a", "aac", "-b:a", "128k",
		"-f", "mpegts", "-rist_profile", "main",
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
		online, onlineError := mediaServer.Online(waitContext)
		if onlineError == nil && online {
			break
		}
		select {
		case <-waitContext.Done():
			t.Fatalf("MediaMTX input did not become ready: %v; sender log: %s; service log: %s", onlineError, senderLog.String(), serviceLog.String())
		case <-time.After(100 * time.Millisecond):
		}
	}
	liveProbe := exec.CommandContext(waitContext, ffprobe,
		"-v", "error",
		"-rtsp_transport", "tcp",
		"-analyzeduration", "3000000",
		"-probesize", "5000000",
		"-show_streams", "-of", "json", mediaServer.RTSPURL(),
	)
	liveMetadata, err := liveProbe.Output()
	if err != nil {
		t.Fatalf("probe MediaMTX RTSP input: %v; service log: %s", err, serviceLog.String())
	}
	var liveStreams struct {
		Streams []struct {
			CodecType    string `json:"codec_type"`
			CodecName    string `json:"codec_name"`
			AverageRate  string `json:"avg_frame_rate"`
			ReportedRate string `json:"r_frame_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(liveMetadata, &liveStreams); err != nil {
		t.Fatalf("decode MediaMTX RTSP metadata: %v", err)
	}
	videoValid := false
	for _, stream := range liveStreams.Streams {
		if stream.CodecType != "video" || stream.CodecName != "h264" {
			continue
		}
		rate := stream.AverageRate
		if rate == "" || rate == "0/0" {
			rate = stream.ReportedRate
		}
		videoValid = rateIsThirty(rate)
		break
	}
	if !videoValid {
		t.Fatalf("MediaMTX RTSP metadata does not contain H.264/30 fps: %s", liveMetadata)
	}

	var receiverLog bytes.Buffer
	receiver := exec.CommandContext(waitContext, ffmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-rtsp_transport", "tcp",
		"-i", mediaServer.RTSPURL(), "-map", "0:v:0", "-map", "0:a:0",
		"-frames:v", "30", "-frames:a", "50", "-f", "null", "-",
	)
	receiver.Stderr = &receiverLog
	if err := receiver.Run(); err != nil {
		t.Fatalf("decode 30 synthetic frames from worker RTSP: %v; log: %s", err, receiverLog.String())
	}

	recorder, err := recording.NewRecorder(recording.Config{
		SharedVolume: sharedVolume,
		Controller:   mediaServer,
		StartWait:    10 * time.Second,
		FinishWait:   10 * time.Second,
	})
	if err != nil {
		t.Fatalf("create MediaMTX recorder: %v", err)
	}
	recordContext, recordCancel := context.WithTimeout(ctx, 20*time.Second)
	defer recordCancel()
	if _, err := recorder.Start(recordContext, "recordings/integration/video.mp4"); err != nil {
		t.Fatalf("start MediaMTX fMP4 recording: %v; service log: %s", err, serviceLog.String())
	}
	select {
	case <-recordContext.Done():
		t.Fatalf("wait while recording: %v", recordContext.Err())
	case <-time.After(2200 * time.Millisecond):
	}
	if _, err := recorder.Finish(recordContext); err != nil {
		t.Fatalf("finish MediaMTX fMP4 recording: %v; service log: %s", err, serviceLog.String())
	}
	finalPath := filepath.Join(sharedVolume, "recordings", "integration", "video.mp4")
	boxCounts, err := topLevelMP4Boxes(finalPath)
	if err != nil {
		t.Fatalf("inspect recorded fMP4 boxes: %v", err)
	}
	if boxCounts["moof"] < 2 || boxCounts["mdat"] < 2 {
		t.Fatalf("recorded MP4 boxes = %v, want multiple moof/mdat fragments", boxCounts)
	}
	verify := exec.CommandContext(recordContext, ffprobe, "-v", "error", "-show_entries", "stream=codec_name", "-of", "csv=p=0", finalPath)
	if output, err := verify.CombinedOutput(); err != nil || !bytes.Contains(output, []byte("h264")) {
		t.Fatalf("probe finalized MediaMTX recording: %v, output: %s", err, output)
	}
}

func rateIsThirty(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return false
	}
	numerator, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return false
	}
	denominator, err := strconv.ParseFloat(parts[1], 64)
	return err == nil && denominator != 0 && numerator/denominator == 30
}

func TestMediaMTXRecordingHook(t *testing.T) {
	if os.Getenv("KINUGASA_HOOK_HELPER") != "1" {
		return
	}
	argument := os.Args[len(os.Args)-1]
	if err := recording.WriteSegmentHook(
		os.Getenv("KINUGASA_SHARED_VOLUME"),
		argument,
		os.Getenv("MTX_SEGMENT_PATH"),
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("write MediaMTX recording hook: %v", err)
	}
}

func testHookCommand(executable, argument string) string {
	return strconv.Quote(executable) + " -test.run=^TestMediaMTXRecordingHook$ -- " + argument
}

func topLevelMP4Boxes(path string) (map[string]int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	var offset int64
	for offset < info.Size() {
		var header [16]byte
		if _, err := io.ReadFull(file, header[:8]); err != nil {
			return nil, err
		}
		size := uint64(binary.BigEndian.Uint32(header[:4]))
		headerSize := uint64(8)
		if size == 1 {
			if _, err := io.ReadFull(file, header[8:16]); err != nil {
				return nil, err
			}
			size = binary.BigEndian.Uint64(header[8:16])
			headerSize = 16
		} else if size == 0 {
			size = uint64(info.Size() - offset)
		}
		if size < headerSize || size > uint64(info.Size()-offset) {
			return nil, fmt.Errorf("invalid MP4 box size at byte %d", offset)
		}
		counts[string(header[4:8])]++
		offset += int64(size)
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return counts, nil
}

func requireBinary(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("%s is required for integration tests: %v", name, err)
	}
	return path
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
