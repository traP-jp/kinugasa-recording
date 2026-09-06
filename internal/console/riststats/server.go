package riststats

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	collectormetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	serviceName          = "kinugasa-video-gateway"
	outputPacketsMetric  = "kinugasa.rist.output.packets"
	lostPacketsMetric    = "kinugasa.rist.lost.packets"
	recoveredMetric      = "kinugasa.rist.recovered.packets"
	discontinuityMetric  = "kinugasa.rist.discontinuities"
	serviceNameAttribute = "service.name"
	instanceAttribute    = "service.instance.id"
	sessionAttribute     = "kinugasa.session.name"
	cameraAttribute      = "kinugasa.camera.name"
	flowAttribute        = "rist.flow.id"
)

type recorder interface {
	Record([]Statistics)
}

type Server struct {
	collectormetricsv1.UnimplementedMetricsServiceServer
	recorder recorder
	logger   *slog.Logger
}

func NewServer(recorder recorder, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{recorder: recorder, logger: logger}
}

func (s *Server) Export(_ context.Context, request *collectormetricsv1.ExportMetricsServiceRequest) (*collectormetricsv1.ExportMetricsServiceResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "metrics request is required")
	}
	statistics, rejected, messages := parseMetrics(request)
	if len(statistics) > 0 {
		s.recorder.Record(statistics)
	}
	if rejected > 0 {
		s.logger.Warn("rejected RIST telemetry data points", "count", rejected, "reason", messages)
		return &collectormetricsv1.ExportMetricsServiceResponse{
			PartialSuccess: &collectormetricsv1.ExportMetricsPartialSuccess{
				RejectedDataPoints: rejected,
				ErrorMessage:       messages,
			},
		}, nil
	}
	return &collectormetricsv1.ExportMetricsServiceResponse{}, nil
}

type pointKey struct {
	sessionName     string
	cameraName      string
	gatewayInstance string
	flowID          uint32
}

func parseMetrics(request *collectormetricsv1.ExportMetricsServiceRequest) ([]Statistics, int64, string) {
	points := make(map[pointKey]*Statistics)
	var rejected int64
	var lastError string
	for _, resourceMetrics := range request.ResourceMetrics {
		attributes := keyValues(resourceMetrics.GetResource().GetAttributes())
		if attributes[serviceNameAttribute] != serviceName || attributes[instanceAttribute] == "" ||
			attributes[sessionAttribute] == "" || attributes[cameraAttribute] == "" {
			rejected += countDataPoints(resourceMetrics)
			lastError = "required video gateway resource attributes are missing"
			continue
		}
		for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
			for _, metric := range scopeMetrics.Metrics {
				field := metricField(metric.Name)
				if field == 0 {
					continue
				}
				sum := metric.GetSum()
				if sum == nil || !sum.IsMonotonic || sum.AggregationTemporality != metricsv1.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA {
					rejected += int64(len(sum.GetDataPoints()))
					lastError = fmt.Sprintf("%s must be a monotonic delta Sum", metric.Name)
					continue
				}
				for _, point := range sum.DataPoints {
					flowID, ok := uint32Attribute(point.Attributes, flowAttribute)
					value, valueOK := countValue(point)
					if !ok || !valueOK || point.TimeUnixNano <= point.StartTimeUnixNano {
						rejected++
						lastError = fmt.Sprintf("%s has invalid flow, value, or interval", metric.Name)
						continue
					}
					key := pointKey{
						sessionName: attributes[sessionAttribute], cameraName: attributes[cameraAttribute],
						gatewayInstance: attributes[instanceAttribute], flowID: flowID,
					}
					intervalStart := unixNano(point.StartTimeUnixNano)
					intervalEnd := unixNano(point.TimeUnixNano)
					statistics := points[key]
					if statistics == nil {
						statistics = &Statistics{
							SessionName: key.sessionName, CameraName: key.cameraName,
							GatewayInstance: key.gatewayInstance, FlowID: key.flowID,
							IntervalStart: intervalStart, IntervalEnd: intervalEnd,
						}
						points[key] = statistics
					} else {
						if intervalStart.Before(statistics.IntervalStart) {
							statistics.IntervalStart = intervalStart
						}
						if intervalEnd.After(statistics.IntervalEnd) {
							statistics.IntervalEnd = intervalEnd
						}
					}
					field.add(statistics, value)
				}
			}
		}
	}
	result := make([]Statistics, 0, len(points))
	for _, statistics := range points {
		result = append(result, *statistics)
	}
	return result, rejected, lastError
}

type statisticsField uint8

const (
	outputField statisticsField = iota + 1
	lostField
	recoveredField
	discontinuityField
)

func metricField(name string) statisticsField {
	switch name {
	case outputPacketsMetric:
		return outputField
	case lostPacketsMetric:
		return lostField
	case recoveredMetric:
		return recoveredField
	case discontinuityMetric:
		return discontinuityField
	default:
		return 0
	}
}

func (f statisticsField) add(statistics *Statistics, value uint64) {
	switch f {
	case outputField:
		statistics.OutputPackets += value
	case lostField:
		statistics.LostPackets += value
	case recoveredField:
		statistics.RecoveredPackets += value
	case discontinuityField:
		statistics.Discontinuities += value
	}
}

func keyValues(attributes []*commonv1.KeyValue) map[string]string {
	values := make(map[string]string, len(attributes))
	for _, attribute := range attributes {
		if value, ok := attribute.GetValue().Value.(*commonv1.AnyValue_StringValue); ok {
			values[attribute.Key] = value.StringValue
		}
	}
	return values
}

func uint32Attribute(attributes []*commonv1.KeyValue, name string) (uint32, bool) {
	for _, attribute := range attributes {
		if attribute.Key != name {
			continue
		}
		value, ok := attribute.GetValue().Value.(*commonv1.AnyValue_IntValue)
		if !ok || value.IntValue < 0 || value.IntValue > math.MaxUint32 {
			return 0, false
		}
		return uint32(value.IntValue), true
	}
	return 0, false
}

func countValue(point *metricsv1.NumberDataPoint) (uint64, bool) {
	switch value := point.Value.(type) {
	case *metricsv1.NumberDataPoint_AsInt:
		return uint64(value.AsInt), value.AsInt >= 0
	case *metricsv1.NumberDataPoint_AsDouble:
		return uint64(value.AsDouble), value.AsDouble >= 0 && value.AsDouble <= math.MaxUint64 && math.Trunc(value.AsDouble) == value.AsDouble
	default:
		return 0, false
	}
}

func countDataPoints(resourceMetrics *metricsv1.ResourceMetrics) int64 {
	var count int64
	for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
		for _, metric := range scopeMetrics.Metrics {
			if sum := metric.GetSum(); sum != nil {
				count += int64(len(sum.DataPoints))
			}
		}
	}
	return count
}

func unixNano(value uint64) time.Time {
	return time.Unix(0, int64(value)).UTC()
}
