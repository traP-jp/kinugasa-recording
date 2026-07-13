package ffmpeg

import (
	"slices"
	"strings"
	"testing"
)

func TestFanoutCommandsSupportRISTAndSRT(t *testing.T) {
	t.Parallel()
	commands, err := FanoutCommands(FanoutConfig{
		RISTListenPort: 31000, SRTListenPort: 31001,
		RecordingListenPort: 12000, PreviewRTMPURL: "rtmp://ingress/live/front",
	})
	if err != nil {
		t.Fatalf("FanoutCommands() returned %v", err)
	}
	for name, expected := range map[string]string{"ingest-rist": "rist://0.0.0.0:31000", "ingest-srt": "srt://0.0.0.0:31001"} {
		ingest := strings.Join(commands[name].Args, " ")
		if !strings.Contains(ingest, expected) {
			t.Errorf("%s does not contain %s: %s", name, expected, ingest)
		}
		if !strings.Contains(ingest, "-c:v copy") || !strings.Contains(ingest, "-f tee") {
			t.Errorf("%s does not stream-copy to tee: %s", name, ingest)
		}
	}
	rist := strings.Join(commands["ingest-rist"].Args, " ")
	if !strings.Contains(rist, "rist_profile=1") {
		t.Fatalf("RIST listener does not select main profile: %s", rist)
	}
	preview := strings.Join(commands["preview"].Args, " ")
	recording := strings.Join(commands["recording"].Args, " ")
	if !strings.Contains(preview, "127.0.0.1:11000") || !strings.Contains(recording, "127.0.0.1:11001") {
		t.Fatalf("loopback ports overlap external listeners: preview=%s recording=%s", preview, recording)
	}
}

func TestIngressCommandSupportsCopyAndPreviewTranscode(t *testing.T) {
	t.Parallel()

	copyCommand, err := IngressCommand(IngressConfig{
		RTMPListenURL: "rtmp://0.0.0.0:1935/live/front",
		WHIPURL:       "http://livekit-ingress/whip/id",
		WHIPToken:     "secret",
	})
	if err != nil {
		t.Fatalf("IngressCommand() returned %v", err)
	}
	if !slices.Contains(copyCommand.Args, "copy") || !slices.Contains(copyCommand.Args, "-authorization") {
		t.Fatalf("copy command = %#v", copyCommand.Args)
	}

	transcodeCommand, err := IngressCommand(IngressConfig{
		RTMPListenURL: "rtmp://0.0.0.0:1935/live/front",
		WHIPURL:       "http://livekit-ingress/whip/id",
		Transcode:     true,
	})
	if err != nil {
		t.Fatalf("IngressCommand() returned %v", err)
	}
	joined := strings.Join(transcodeCommand.Args, " ")
	if !strings.Contains(joined, "-profile:v baseline") || !strings.Contains(joined, "-bf 0") {
		t.Fatalf("transcode command is not WHIP-compatible: %s", joined)
	}
}

func TestRecorderCommandUsesStreamCopyAndSegmentContract(t *testing.T) {
	t.Parallel()

	command, err := RecorderCommand(RecorderConfig{
		InputURL:      "srt://video-fanout:12000?mode=caller&transtype=live",
		RecordingRoot: "/recording",
		SegmentLength: 10,
	})
	if err != nil {
		t.Fatalf("RecorderCommand() returned %v", err)
	}
	joined := strings.Join(command.Args, " ")
	for _, expected := range []string{
		"-c:v copy",
		"-f segment",
		"-segment_format mpegts",
		"-segment_time 10",
		"-segment_list /recording/state/segments.list",
		"/recording/staging/segment-%020d.ts.part",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("command does not contain %q: %s", expected, joined)
		}
	}
}
