package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
)

type probeOutput struct {
	Streams []probeStream `json:"streams"`
}

type probeStream struct {
	CodecType    string `json:"codec_type"`
	CodecName    string `json:"codec_name"`
	AverageRate  string `json:"avg_frame_rate"`
	ReportedRate string `json:"r_frame_rate"`
}

func probeMedia(ctx context.Context, ffprobeBinary, rtspURL string) *workerv1.InputStatus {
	command := exec.CommandContext(ctx, ffprobeBinary,
		"-v", "error",
		"-rtsp_transport", "tcp",
		"-analyzeduration", "3000000",
		"-probesize", "5000000",
		"-show_streams", "-of", "json", rtspURL,
	)
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return inputError(
			workerv1.ErrorCode_ERROR_CODE_INPUT_UNAVAILABLE,
			"could not inspect the MediaMTX input",
			true,
		)
	}
	var parsed probeOutput
	if err := json.Unmarshal(output, &parsed); err != nil {
		return inputError(
			workerv1.ErrorCode_ERROR_CODE_MEDIA_PIPELINE_FAILURE,
			"ffprobe returned invalid MediaMTX stream metadata",
			true,
		)
	}
	return validateProbe(parsed)
}

func validateProbe(output probeOutput) *workerv1.InputStatus {
	videoFound := false
	for _, stream := range output.Streams {
		if stream.CodecType != "video" || videoFound {
			continue
		}
		videoFound = true
		if stream.CodecName != "h264" {
			return inputError(
				workerv1.ErrorCode_ERROR_CODE_UNSUPPORTED_VIDEO_CODEC,
				"camera video codec must be H.264",
				false,
			)
		}
		rate := stream.AverageRate
		if rate == "" || rate == "0/0" {
			rate = stream.ReportedRate
		}
		if !isThirtyFPS(rate) {
			return inputError(
				workerv1.ErrorCode_ERROR_CODE_UNSUPPORTED_FRAME_RATE,
				"camera video frame rate must be 30 fps",
				false,
			)
		}
	}
	if !videoFound {
		return inputError(
			workerv1.ErrorCode_ERROR_CODE_UNSUPPORTED_VIDEO_CODEC,
			"camera input does not contain a video stream",
			false,
		)
	}
	return &workerv1.InputStatus{State: workerv1.InputState_INPUT_STATE_CONNECTED}
}

func inputError(code workerv1.ErrorCode, message string, retryable bool) *workerv1.InputStatus {
	return &workerv1.InputStatus{
		State: workerv1.InputState_INPUT_STATE_ERROR,
		Error: &workerv1.WorkerError{Code: code, Message: message, Retryable: retryable},
	}
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
