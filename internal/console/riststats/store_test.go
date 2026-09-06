package riststats

import (
	"testing"
	"time"
)

func TestStoreKeepsLatestIntervalAndMarksItStale(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 10, 0, time.UTC)
	store := newStore(func() time.Time { return now })
	latest := Statistics{
		SessionName: "session-1", CameraName: "camera-1", FlowID: 42,
		IntervalStart: now.Add(-5 * time.Second), IntervalEnd: now,
		OutputPackets: 100, LostPackets: 2,
	}
	store.Record([]Statistics{latest})
	store.Record([]Statistics{{
		SessionName: "session-1", CameraName: "camera-1", FlowID: 42,
		IntervalStart: now.Add(-10 * time.Second), IntervalEnd: now.Add(-5 * time.Second),
		OutputPackets: 90,
	}})

	items := store.List("session-1")
	if len(items) != 1 || items[0].OutputPackets != 100 || items[0].Stale {
		t.Fatalf("statistics = %+v", items)
	}
	now = now.Add(StaleAfter + time.Nanosecond)
	if items = store.List("session-1"); len(items) != 1 || !items[0].Stale {
		t.Fatalf("stale statistics = %+v", items)
	}
}

func TestStoreSeparatesSessionsCamerasAndFlows(t *testing.T) {
	now := time.Now()
	store := newStore(func() time.Time { return now })
	store.Record([]Statistics{
		{SessionName: "b", CameraName: "camera", FlowID: 1, IntervalEnd: now},
		{SessionName: "a", CameraName: "z", FlowID: 2, IntervalEnd: now},
		{SessionName: "a", CameraName: "a", FlowID: 3, IntervalEnd: now},
	})
	items := store.List("a")
	if len(items) != 2 || items[0].CameraName != "a" || items[1].CameraName != "z" {
		t.Fatalf("statistics = %+v", items)
	}
}
