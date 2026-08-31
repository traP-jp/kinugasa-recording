package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type probeResult struct {
	HasAudio bool
}

type probeOutput struct {
	Streams []probeStream `json:"streams"`
}

type probeStream struct {
	CodecType    string `json:"codec_type"`
	CodecName    string `json:"codec_name"`
	AverageRate  string `json:"avg_frame_rate"`
	ReportedRate string `json:"r_frame_rate"`
}

func probe(ctx context.Context, config Config) (probeResult, *Status) {
	command := exec.CommandContext(ctx, config.FFprobePath,
		"-v", "error", "-show_streams", "-of", "json", config.RISTOutputURL,
	)
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return probeResult{}, nil
		}
		status := Status{State: StateError, Code: ErrorCodeInputUnavailable, Error: "could not inspect the RIST input"}
		return probeResult{}, &status
	}
	var parsed probeOutput
	if err := json.Unmarshal(output, &parsed); err != nil {
		status := Status{State: StateError, Code: ErrorCodeInputUnavailable, Error: "ffprobe returned invalid stream metadata"}
		return probeResult{}, &status
	}
	return validateProbe(parsed)
}

func validateProbe(output probeOutput) (probeResult, *Status) {
	var videoFound, audioFound bool
	for _, stream := range output.Streams {
		switch stream.CodecType {
		case "video":
			if videoFound {
				continue
			}
			videoFound = true
			if stream.CodecName != "h264" {
				status := Status{State: StateError, Code: ErrorCodeUnsupportedCodec, Error: "camera video codec must be H.264"}
				return probeResult{}, &status
			}
			rate := stream.AverageRate
			if rate == "" || rate == "0/0" {
				rate = stream.ReportedRate
			}
			if !isThirtyFPS(rate) {
				status := Status{State: StateError, Code: ErrorCodeUnsupportedFPS, Error: "camera video frame rate must be 30 fps"}
				return probeResult{}, &status
			}
		case "audio":
			audioFound = true
		}
	}
	if !videoFound {
		status := Status{State: StateError, Code: ErrorCodeUnsupportedCodec, Error: "camera input does not contain a video stream"}
		return probeResult{}, &status
	}
	return probeResult{HasAudio: audioFound}, nil
}

func isThirtyFPS(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return false
	}
	numerator, numeratorError := strconv.ParseFloat(parts[0], 64)
	denominator, denominatorError := strconv.ParseFloat(parts[1], 64)
	if numeratorError != nil || denominatorError != nil || denominator == 0 {
		return false
	}
	return fmt.Sprintf("%.6f", numerator/denominator) == "30.000000"
}
