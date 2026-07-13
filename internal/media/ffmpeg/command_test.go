package ffmpeg

import (
	"slices"
	"strings"
	"testing"
)

func TestFanoutCommandsSupportRISTAndSRT(t *testing.T) {
	t.Parallel()

	for _, protocol := range []InputProtocol{InputProtocolRIST, InputProtocolSRT} {
		protocol := protocol
		t.Run(string(protocol), func(t *testing.T) {
			t.Parallel()

			commands, err := FanoutCommands(FanoutConfig{
				Protocol:            protocol,
				ListenPort:          31000,
				RecordingListenPort: 12000,
				PreviewRTMPURL:      "rtmp://ingress/live/front",
			})
			if err != nil {
				t.Fatalf("FanoutCommands() returned %v", err)
			}
			ingest := strings.Join(commands["ingest"].Args, " ")
			if !strings.Contains(ingest, string(protocol)+"://0.0.0.0:31000") {
				t.Fatalf("ingest command does not contain %s listener: %s", protocol, ingest)
			}
			if !strings.Contains(ingest, "-c:v copy") || !strings.Contains(ingest, "-f tee") {
				t.Fatalf("ingest command does not stream-copy to tee: %s", ingest)
			}
		})
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
