package operator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPFanoutActivityReaderSelectsLatestActiveProtocol(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 14, 1, 2, 3, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"processes":{"ingest-rist":{"frame":12,"lastProgressAt":"2026-07-14T01:01:40Z"},"ingest-srt":{"frame":42,"lastProgressAt":"2026-07-14T01:02:01Z"}}}`))
	}))
	defer server.Close()

	reader := &HTTPFanoutActivityReader{Client: server.Client(), Now: func() time.Time { return now }}
	activity, err := reader.Read(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if activity.Protocol != "srt" || !activity.Active || !activity.LastFrameAt.Equal(now.Add(-2*time.Second)) {
		t.Fatalf("activity = %#v", activity)
	}
}

func TestHTTPFanoutActivityReaderReportsStaleActivity(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 14, 1, 2, 3, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"processes":{"ingest-rist":{"frame":12,"lastProgressAt":"2026-07-14T01:01:00Z"}}}`))
	}))
	defer server.Close()

	activity, err := (&HTTPFanoutActivityReader{Client: server.Client(), Now: func() time.Time { return now }}).Read(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if activity.Protocol != "rist" || activity.Active {
		t.Fatalf("activity = %#v", activity)
	}
}
