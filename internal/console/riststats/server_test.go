package riststats

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	collectormetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
)

type recorderStub struct {
	statistics []Statistics
}

func (r *recorderStub) Record(statistics []Statistics) {
	r.statistics = append(r.statistics, statistics...)
}

func TestServerAcceptsAndGroupsRISTDeltaMetrics(t *testing.T) {
	recorder := &recorderStub{}
	server := NewServer(recorder, slog.New(slog.NewTextHandler(io.Discard, nil)))
	start := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	request := exportRequest(start, map[string]int64{
		outputPacketsMetric: 100,
		lostPacketsMetric:   2,
		recoveredMetric:     7,
		discontinuityMetric: 1,
	})

	response, err := server.Export(context.Background(), request)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if response.PartialSuccess != nil {
		t.Fatalf("partial success = %+v", response.PartialSuccess)
	}
	if len(recorder.statistics) != 1 {
		t.Fatalf("statistics = %+v", recorder.statistics)
	}
	got := recorder.statistics[0]
	if got.SessionName != "session-1" || got.CameraName != "camera-1" || got.GatewayInstance != "pod-uid" || got.FlowID != 42 ||
		got.OutputPackets != 100 || got.LostPackets != 2 || got.RecoveredPackets != 7 || got.Discontinuities != 1 ||
		!got.IntervalStart.Equal(start) || !got.IntervalEnd.Equal(start.Add(5*time.Second)) {
		t.Fatalf("statistics = %+v", got)
	}
}

func TestServerRejectsCumulativeRISTMetrics(t *testing.T) {
	recorder := &recorderStub{}
	server := NewServer(recorder, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := exportRequest(time.Now(), map[string]int64{outputPacketsMetric: 100})
	request.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].GetSum().AggregationTemporality = metricsv1.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE

	response, err := server.Export(context.Background(), request)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if response.GetPartialSuccess().GetRejectedDataPoints() != 1 || len(recorder.statistics) != 0 {
		t.Fatalf("response = %+v, statistics = %+v", response, recorder.statistics)
	}
}

func exportRequest(start time.Time, values map[string]int64) *collectormetricsv1.ExportMetricsServiceRequest {
	metrics := make([]*metricsv1.Metric, 0, len(values))
	for name, value := range values {
		metrics = append(metrics, &metricsv1.Metric{
			Name: name,
			Data: &metricsv1.Metric_Sum{Sum: &metricsv1.Sum{
				AggregationTemporality: metricsv1.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
				IsMonotonic:            true,
				DataPoints: []*metricsv1.NumberDataPoint{{
					StartTimeUnixNano: uint64(start.UnixNano()),
					TimeUnixNano:      uint64(start.Add(5 * time.Second).UnixNano()),
					Attributes:        []*commonv1.KeyValue{intAttribute(flowAttribute, 42)},
					Value:             &metricsv1.NumberDataPoint_AsInt{AsInt: value},
				}},
			}},
		})
	}
	return &collectormetricsv1.ExportMetricsServiceRequest{ResourceMetrics: []*metricsv1.ResourceMetrics{{
		Resource: &resourcev1.Resource{Attributes: []*commonv1.KeyValue{
			stringAttribute(serviceNameAttribute, serviceName),
			stringAttribute(instanceAttribute, "pod-uid"),
			stringAttribute(sessionAttribute, "session-1"),
			stringAttribute(cameraAttribute, "camera-1"),
		}},
		ScopeMetrics: []*metricsv1.ScopeMetrics{{Metrics: metrics}},
	}}}
}

func stringAttribute(key, value string) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: value}}}
}

func intAttribute(key string, value int64) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_IntValue{IntValue: value}}}
}
