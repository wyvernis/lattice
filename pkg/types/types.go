package types

import (
	"time"
)

// Role represents a chat message role.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a single chat turn.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// InferenceRequest is the backend-agnostic request shape.
type InferenceRequest struct {
	ID              string            `json:"id"`
	Type            RequestType       `json:"type"`
	Messages        []Message         `json:"messages,omitempty"`
	Prompt          string            `json:"prompt,omitempty"`
	Input           string            `json:"input,omitempty"` // embeddings
	Model           string            `json:"model,omitempty"` // optional override
	MaxTokens       int               `json:"max_tokens,omitempty"`
	Temperature     float64           `json:"temperature,omitempty"`
	Stream          bool              `json:"stream,omitempty"`
	Policy          RoutingPolicy     `json:"policy,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	TraceID         string            `json:"trace_id,omitempty"`
	TenantID        string            `json:"tenant_id,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	Classification  *Classification   `json:"classification,omitempty"`
	SelectedModel   string            `json:"selected_model,omitempty"`
	SelectedNode    string            `json:"selected_node,omitempty"`
	SelectedBackend string            `json:"selected_backend,omitempty"`
	Quantization    string            `json:"quantization,omitempty"`
}

// RequestType categorizes API surface.
type RequestType string

const (
	RequestChat       RequestType = "chat"
	RequestCompletion RequestType = "completion"
	RequestEmbedding  RequestType = "embedding"
	RequestBatch      RequestType = "batch"
)

// RoutingPolicy controls optimization objective.
type RoutingPolicy string

const (
	PolicyBalanced  RoutingPolicy = "balanced"
	PolicyLatency   RoutingPolicy = "latency_first"
	PolicyCost      RoutingPolicy = "cost_first"
	PolicyQuality   RoutingPolicy = "quality_first"
	PolicyThroughput RoutingPolicy = "throughput"
	PolicyEnergy    RoutingPolicy = "energy_efficient"
	PolicyLeastLoad RoutingPolicy = "least_loaded"
)

// Classification is the router's intent label.
type Classification struct {
	Category   string             `json:"category"`
	Confidence float64            `json:"confidence"`
	Scores     map[string]float64 `json:"scores,omitempty"`
}

// InferenceResponse is a non-streaming completion.
type InferenceResponse struct {
	ID           string    `json:"id"`
	Model        string    `json:"model"`
	Content      string    `json:"content"`
	Usage        Usage     `json:"usage"`
	FinishReason string    `json:"finish_reason"`
	NodeID       string    `json:"node_id"`
	Backend      string    `json:"backend"`
	LatencyMS    int64     `json:"latency_ms"`
	TTFTMS       int64     `json:"ttft_ms,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// Usage tracks token accounting.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamChunk is one SSE/WebSocket token frame.
type StreamChunk struct {
	ID        string `json:"id"`
	Delta     string `json:"delta"`
	Index     int    `json:"index"`
	Done      bool   `json:"done"`
	TTFTMS    int64  `json:"ttft_ms,omitempty"`
	TokensPerSec float64 `json:"tokens_per_sec,omitempty"`
	Error     string `json:"error,omitempty"`
}

// NodeStatus describes a worker's live capacity.
type NodeStatus struct {
	ID              string             `json:"id"`
	Cluster         string             `json:"cluster"`
	Address         string             `json:"address"`
	Healthy         bool               `json:"healthy"`
	LastHeartbeat   time.Time          `json:"last_heartbeat"`
	GPUs            []GPUInfo          `json:"gpus"`
	CPUUtilization  float64            `json:"cpu_utilization"`
	MemoryUsedMB    int64              `json:"memory_used_mb"`
	MemoryTotalMB   int64              `json:"memory_total_mb"`
	ActiveRequests  int                `json:"active_requests"`
	QueueDepth      int                `json:"queue_depth"`
	TokensPerSec    float64            `json:"tokens_per_sec"`
	EstLatencyMS    float64            `json:"est_latency_ms"`
	NetworkLatencyMS float64           `json:"network_latency_ms"`
	LoadedModels    []LoadedModel      `json:"loaded_models"`
	Backends        []string           `json:"backends"`
	Labels          map[string]string  `json:"labels,omitempty"`
	CostPerMillion  float64            `json:"cost_per_million"` // USD per 1M tokens
	EnergyWatts     float64            `json:"energy_watts,omitempty"`
}

// GPUInfo is per-device telemetry.
type GPUInfo struct {
	Index       int     `json:"index"`
	Name        string  `json:"name"`
	Utilization float64 `json:"utilization"`
	MemoryUsedMB  int64 `json:"memory_used_mb"`
	MemoryTotalMB int64 `json:"memory_total_mb"`
	Temperature float64 `json:"temperature,omitempty"`
	PowerWatts  float64 `json:"power_watts,omitempty"`
}

// LoadedModel is a model resident in worker memory.
type LoadedModel struct {
	Name         string    `json:"name"`
	Quantization string    `json:"quantization"`
	Backend      string    `json:"backend"`
	VRAMMB       int64     `json:"vram_mb"`
	LoadedAt     time.Time `json:"loaded_at"`
	LastUsedAt   time.Time `json:"last_used_at"`
	Warm         bool      `json:"warm"`
}

// ModelRecord is registry metadata.
type ModelRecord struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Version          string            `json:"version"`
	Provider         string            `json:"provider"`
	Quantizations    []string          `json:"quantizations"`
	Capabilities     []string          `json:"capabilities"` // chat, code, vision, embed, ...
	MinVRAMMB        int64             `json:"min_vram_mb"`
	PreferredBackend string            `json:"preferred_backend"`
	DownloadURL      string            `json:"download_url,omitempty"`
	Checksum         string            `json:"checksum,omitempty"`
	DownloadStatus   string            `json:"download_status"`
	SizeBytes        int64             `json:"size_bytes,omitempty"`
	CostPerMillion   float64           `json:"cost_per_million"`
	QualityScore     float64           `json:"quality_score"` // 0-1
	Tags             []string          `json:"tags,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// ScheduleDecision is the scheduler's pick.
type ScheduleDecision struct {
	NodeID       string  `json:"node_id"`
	Model        string  `json:"model"`
	Backend      string  `json:"backend"`
	Quantization string  `json:"quantization"`
	Score        float64 `json:"score"`
	Reason       string  `json:"reason"`
	NeedLoad     bool    `json:"need_load"`
	EstLatencyMS float64 `json:"est_latency_ms"`
	EstCostUSD   float64 `json:"est_cost_usd"`
}

// ClusterEvent is published on the event bus.
type ClusterEvent struct {
	Type      string                 `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	Source    string                 `json:"source"`
	Payload   map[string]interface{} `json:"payload"`
}

// BatchRequest groups compatible inference jobs.
type BatchRequest struct {
	ID       string             `json:"id"`
	Requests []*InferenceRequest `json:"requests"`
	Model    string             `json:"model"`
	CreatedAt time.Time         `json:"created_at"`
}

// HealthReport is used by chaos / fault metrics.
type HealthReport struct {
	Availability   float64 `json:"availability"`
	RecoveryTimeMS int64   `json:"recovery_time_ms"`
	DroppedReqs    int64   `json:"dropped_requests"`
	FailedReqs     int64   `json:"failed_requests"`
	ActiveNodes    int     `json:"active_nodes"`
	TotalNodes     int     `json:"total_nodes"`
}
