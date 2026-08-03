package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	RequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "lattice",
		Name:      "requests_total",
		Help:      "Total inference requests",
	}, []string{"service", "type", "status"})

	RequestLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "lattice",
		Name:      "request_latency_seconds",
		Help:      "End-to-end request latency",
		Buckets:   []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"service", "model", "backend"})

	TTFT = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "lattice",
		Name:      "ttft_seconds",
		Help:      "Time to first token",
		Buckets:   []float64{.01, .025, .05, .1, .25, .5, 1, 2, 5},
	}, []string{"model", "backend"})

	TokensPerSec = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "lattice",
		Name:      "tokens_per_second",
		Help:      "Observed streaming tokens/sec",
	}, []string{"node", "model"})

	GPUUtilization = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "lattice",
		Name:      "gpu_utilization",
		Help:      "GPU utilization ratio 0-1",
	}, []string{"node", "gpu"})

	GPUMemory = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "lattice",
		Name:      "gpu_memory_bytes",
		Help:      "GPU memory used bytes",
	}, []string{"node", "gpu"})

	QueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "lattice",
		Name:      "queue_depth",
		Help:      "Per-node queue depth",
	}, []string{"node"})

	CacheHitRatio = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "lattice",
		Name:      "cache_hit_ratio",
		Help:      "Model/semantic cache hit ratio",
	}, []string{"cache"})

	BatchEfficiency = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "lattice",
		Name:      "batch_efficiency",
		Help:      "Average batch fill ratio",
	}, []string{"node"})

	ActiveModels = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "lattice",
		Name:      "active_models",
		Help:      "Loaded models per node",
	}, []string{"node"})

	FailedRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "lattice",
		Name:      "failed_requests_total",
		Help:      "Failed inference requests",
	}, []string{"service", "reason"})

	ActiveRequests = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "lattice",
		Name:      "active_requests",
		Help:      "In-flight requests",
	}, []string{"node"})

	StreamingSessions = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "lattice",
		Name:      "streaming_sessions",
		Help:      "Active streaming sessions",
	})

	CostUSD = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "lattice",
		Name:      "estimated_cost_usd_total",
		Help:      "Estimated inference spend",
	}, []string{"model", "tenant"})
)

// Handler exposes Prometheus scrape endpoint.
func Handler() http.Handler {
	return promhttp.Handler()
}
