package plugins

import (
	"context"

	"github.com/lattice-ai/lattice/pkg/types"
)

// RouterPlugin classifies and maps requests to candidate models.
type RouterPlugin interface {
	Name() string
	Classify(ctx context.Context, req *types.InferenceRequest) (*types.Classification, error)
	CandidateModels(ctx context.Context, class *types.Classification, policy types.RoutingPolicy) ([]string, error)
}

// SchedulerPlugin ranks nodes for a request.
type SchedulerPlugin interface {
	Name() string
	Select(ctx context.Context, req *types.InferenceRequest, nodes []types.NodeStatus, models []string) (*types.ScheduleDecision, error)
}

// BackendPlugin talks to an inference engine.
type BackendPlugin interface {
	Name() string
	Supports(model string) bool
	Generate(ctx context.Context, req *types.InferenceRequest) (*types.InferenceResponse, error)
	GenerateStream(ctx context.Context, req *types.InferenceRequest, emit func(types.StreamChunk) error) error
	Embed(ctx context.Context, req *types.InferenceRequest) ([]float64, error)
	LoadModel(ctx context.Context, model, quantization string) error
	UnloadModel(ctx context.Context, model string) error
	Health(ctx context.Context) error
}

// AuthPlugin validates credentials and roles.
type AuthPlugin interface {
	Name() string
	Authenticate(ctx context.Context, apiKey, bearer string) (*Identity, error)
	Authorize(ctx context.Context, id *Identity, action string) error
}

// Identity is an authenticated principal.
type Identity struct {
	Subject string
	Tenant  string
	Roles   []string
	QuotaRPM int
}

// MetricsPlugin can emit custom telemetry.
type MetricsPlugin interface {
	Name() string
	Record(ctx context.Context, name string, value float64, labels map[string]string)
}

// StoragePlugin abstracts model artifact storage.
type StoragePlugin interface {
	Name() string
	Exists(ctx context.Context, key string) (bool, error)
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

// BenchmarkPlugin runs model benchmarks.
type BenchmarkPlugin interface {
	Name() string
	Run(ctx context.Context, model string, opts map[string]interface{}) (*BenchmarkResult, error)
}

// BenchmarkResult summarizes a run.
type BenchmarkResult struct {
	Model          string  `json:"model"`
	ThroughputTPS  float64 `json:"throughput_tps"`
	LatencyP50MS   float64 `json:"latency_p50_ms"`
	LatencyP99MS   float64 `json:"latency_p99_ms"`
	TTFTMS         float64 `json:"ttft_ms"`
	TPOTMS         float64 `json:"tpot_ms"`
	MemoryMB       int64   `json:"memory_mb"`
	GPUUtil        float64 `json:"gpu_util"`
	EnergyJoules   float64 `json:"energy_joules,omitempty"`
}

// Registry is the in-process plugin registry.
type Registry struct {
	Routers    map[string]RouterPlugin
	Schedulers map[string]SchedulerPlugin
	Backends   map[string]BackendPlugin
	Auth       map[string]AuthPlugin
	Metrics    map[string]MetricsPlugin
	Storage    map[string]StoragePlugin
	Benchmarks map[string]BenchmarkPlugin
}

// NewRegistry constructs an empty plugin registry.
func NewRegistry() *Registry {
	return &Registry{
		Routers:    map[string]RouterPlugin{},
		Schedulers: map[string]SchedulerPlugin{},
		Backends:   map[string]BackendPlugin{},
		Auth:       map[string]AuthPlugin{},
		Metrics:    map[string]MetricsPlugin{},
		Storage:    map[string]StoragePlugin{},
		Benchmarks: map[string]BenchmarkPlugin{},
	}
}

func (r *Registry) RegisterRouter(p RouterPlugin)       { r.Routers[p.Name()] = p }
func (r *Registry) RegisterScheduler(p SchedulerPlugin) { r.Schedulers[p.Name()] = p }
func (r *Registry) RegisterBackend(p BackendPlugin)     { r.Backends[p.Name()] = p }
func (r *Registry) RegisterAuth(p AuthPlugin)           { r.Auth[p.Name()] = p }
func (r *Registry) RegisterMetrics(p MetricsPlugin)     { r.Metrics[p.Name()] = p }
func (r *Registry) RegisterStorage(p StoragePlugin)     { r.Storage[p.Name()] = p }
func (r *Registry) RegisterBenchmark(p BenchmarkPlugin) { r.Benchmarks[p.Name()] = p }
