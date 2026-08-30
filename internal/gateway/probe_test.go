package gateway

import "testing"

func TestValidateProbeAcceptsH264ThirtyFPSWithOptionalAudio(t *testing.T) {
	var output probeOutput
	output.Streams = append(output.Streams,
		probeStream{CodecType: "video", CodecName: "h264", AverageRate: "30000/1000"},
		probeStream{CodecType: "audio", CodecName: "aac"},
	)
	result, failure := validateProbe(output)
	if failure != nil || !result.HasAudio {
		t.Fatalf("validateProbe() = %+v, %+v", result, failure)
	}
}

func TestValidateProbeRejectsUnsupportedInput(t *testing.T) {
	tests := []struct {
		name  string
		codec string
		rate  string
		code  ErrorCode
	}{
		{name: "codec", codec: "hevc", rate: "30/1", code: ErrorCodeUnsupportedCodec},
		{name: "frame rate", codec: "h264", rate: "25/1", code: ErrorCodeUnsupportedFPS},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output probeOutput
			output.Streams = append(output.Streams, probeStream{
				CodecType: "video", CodecName: test.codec, AverageRate: test.rate,
			})
			_, failure := validateProbe(output)
			if failure == nil || failure.Code != test.code {
				t.Fatalf("validateProbe() failure = %+v", failure)
			}
		})
	}
}
