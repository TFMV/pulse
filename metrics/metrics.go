package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all the Prometheus metrics for the application
type Metrics struct {
	RequestCount       *prometheus.CounterVec
	ResponseLatency    *prometheus.HistogramVec
	ErrorCount         *prometheus.CounterVec
	RegionHealthStatus *prometheus.GaugeVec
}

// NewMetrics creates and registers all metrics
func NewMetrics() *Metrics {
	m := &Metrics{
		// Track request count by region, transaction type (MTI), and response code
		RequestCount: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pulse_requests_total",
				Help: "The total number of processed ISO8583 requests",
			},
			[]string{"region", "mti", "response_code"},
		),

		// Track response latency by region
		ResponseLatency: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "pulse_response_latency_seconds",
				Help:    "Response latency distribution in seconds",
				Buckets: prometheus.ExponentialBuckets(0.001, 2, 10), // From 1ms to ~1s
			},
			[]string{"region", "mti"},
		),

		// Track error count by region and type
		ErrorCount: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pulse_errors_total",
				Help: "The total number of errors encountered",
			},
			[]string{"region", "error_type"},
		),

		// Track region health status (1 = healthy, 0 = unhealthy)
		RegionHealthStatus: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "pulse_region_health",
				Help: "Health status of each region (1 = healthy, 0 = unhealthy)",
			},
			[]string{"region"},
		),
	}

	return m
}
