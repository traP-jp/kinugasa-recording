package main

import (
	"testing"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
)

func TestValidateProbeAcceptsH264ThirtyFPSWithOptionalAudio(t *testing.T) {
	var output probeOutput
	output.Streams = append(output.Streams,
		probeStream{CodecType: "video", CodecName: "h264", AverageRate: "30000/1000"},
		probeStream{CodecType: "audio", CodecName: "aac"},
	)
	status := validateProbe(output)
	if status.State != workerv1.InputState_INPUT_STATE_CONNECTED {
		t.Fatalf("validateProbe() = %+v", status)
	}
}

func TestValidateProbeRejectsUnsupportedInput(t *testing.T) {
	tests := []struct {
		name  string
		codec string
		rate  string
		code  workerv1.ErrorCode
	}{
		{name: "codec", codec: "hevc", rate: "30/1", code: workerv1.ErrorCode_ERROR_CODE_UNSUPPORTED_VIDEO_CODEC},
		{name: "frame rate", codec: "h264", rate: "25/1", code: workerv1.ErrorCode_ERROR_CODE_UNSUPPORTED_FRAME_RATE},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output probeOutput
			output.Streams = append(output.Streams, probeStream{
				CodecType: "video", CodecName: test.codec, AverageRate: test.rate,
			})
			status := validateProbe(output)
			if status.State != workerv1.InputState_INPUT_STATE_ERROR || status.Error.Code != test.code {
				t.Fatalf("validateProbe() = %+v", status)
			}
		})
	}
}
