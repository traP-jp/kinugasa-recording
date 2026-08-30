package main

import (
	"testing"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/gateway"
)

func TestWorkerInputStatusCombinesGatewayAndMediaState(t *testing.T) {
	connected := workerInputStatus(gateway.Status{State: gateway.StateConnected}, true)
	if connected.State != workerv1.InputState_INPUT_STATE_CONNECTED {
		t.Fatalf("connected status = %+v", connected)
	}
	waiting := workerInputStatus(gateway.Status{State: gateway.StateConnected}, false)
	if waiting.State != workerv1.InputState_INPUT_STATE_WAITING {
		t.Fatalf("waiting status = %+v", waiting)
	}
	failure := workerInputStatus(gateway.Status{
		State: gateway.StateError, Code: gateway.ErrorCodeUnsupportedFPS, Error: "must be 30 fps",
	}, false)
	if failure.State != workerv1.InputState_INPUT_STATE_ERROR ||
		failure.Error.Code != workerv1.ErrorCode_ERROR_CODE_UNSUPPORTED_FRAME_RATE {
		t.Fatalf("error status = %+v", failure)
	}
}
