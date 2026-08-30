package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientReadsGatewayStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"state":"error","code":"unsupported_frame_rate","error":"must be 30 fps"}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.Status(context.Background())
	if err != nil || status.Code != ErrorCodeUnsupportedFPS {
		t.Fatalf("Status() = %+v, %v", status, err)
	}
}
