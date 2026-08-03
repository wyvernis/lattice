package cluster

import (
	"sync"
	"time"

	"github.com/lattice-ai/lattice/pkg/types"
)

// State is the cluster-wide view of nodes, models, and live requests.
type State struct {
	mu       sync.RWMutex
	nodes    map[string]types.NodeStatus
	models   map[string]types.ModelRecord
	requests map[string]LiveRequest
	streams  map[string]LiveStream
	failures []FailureEvent
}

// LiveRequest is an in-flight inference job.
type LiveRequest struct {
	ID        string    `json:"id"`
	Model     string    `json:"model"`
	NodeID    string    `json:"node_id"`
	Tenant    string    `json:"tenant"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	Category  string    `json:"category,omitempty"`
}

// LiveStream tracks an active streaming session.
type LiveStream struct {
	ID        string    `json:"id"`
	NodeID    string    `json:"node_id"`
	Model     string    `json:"model"`
	TTFTMS    int64     `json:"ttft_ms"`
	TPS       float64   `json:"tokens_per_sec"`
	StartedAt time.Time `json:"started_at"`
}

// FailureEvent records fault events for the dashboard.
type FailureEvent struct {
	ID        string    `json:"id"`
	NodeID    string    `json:"node_id"`
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// NewState creates empty cluster state.
func NewState() *State {
	return &State{
		nodes:    map[string]types.NodeStatus{},
		models:   map[string]types.ModelRecord{},
		requests: map[string]LiveRequest{},
		streams:  map[string]LiveStream{},
	}
}

func (s *State) UpsertNode(n types.NodeStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes[n.ID] = n
}

func (s *State) RemoveNode(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.nodes, id)
}

func (s *State) Nodes() []types.NodeStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]types.NodeStatus, 0, len(s.nodes))
	for _, n := range s.nodes {
		out = append(out, n)
	}
	return out
}

func (s *State) HealthyNodes() []types.NodeStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]types.NodeStatus, 0, len(s.nodes))
	now := time.Now()
	for _, n := range s.nodes {
		if n.Healthy && now.Sub(n.LastHeartbeat) < 30*time.Second {
			out = append(out, n)
		}
	}
	return out
}

func (s *State) MarkUnhealthy(id, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.nodes[id]; ok {
		n.Healthy = false
		s.nodes[id] = n
		s.failures = append(s.failures, FailureEvent{
			ID: id + "-" + time.Now().Format("150405"), NodeID: id, Type: "node_unhealthy",
			Message: reason, Timestamp: time.Now().UTC(),
		})
		if len(s.failures) > 200 {
			s.failures = s.failures[len(s.failures)-200:]
		}
	}
}

func (s *State) UpsertModel(m types.ModelRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.models[m.ID] = m
}

func (s *State) Models() []types.ModelRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]types.ModelRecord, 0, len(s.models))
	for _, m := range s.models {
		out = append(out, m)
	}
	return out
}

func (s *State) GetModel(id string) (types.ModelRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.models[id]
	return m, ok
}

func (s *State) TrackRequest(r LiveRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[r.ID] = r
}

func (s *State) CompleteRequest(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.requests, id)
	delete(s.streams, id)
}

func (s *State) TrackStream(st LiveStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.streams[st.ID] = st
}

func (s *State) LiveRequests() []LiveRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]LiveRequest, 0, len(s.requests))
	for _, r := range s.requests {
		out = append(out, r)
	}
	return out
}

func (s *State) LiveStreams() []LiveStream {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]LiveStream, 0, len(s.streams))
	for _, st := range s.streams {
		out = append(out, st)
	}
	return out
}

func (s *State) Failures() []FailureEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]FailureEvent, len(s.failures))
	copy(out, s.failures)
	return out
}

// Snapshot is the dashboard payload.
type Snapshot struct {
	Nodes         []types.NodeStatus  `json:"nodes"`
	Models        []types.ModelRecord `json:"models"`
	LiveRequests  []LiveRequest       `json:"live_requests"`
	LiveStreams   []LiveStream        `json:"live_streams"`
	Failures      []FailureEvent      `json:"failures"`
	ActiveNodes   int                 `json:"active_nodes"`
	TotalNodes    int                 `json:"total_nodes"`
	QueueDepth    int                 `json:"queue_depth"`
	ActiveModels  int                 `json:"active_models"`
	Timestamp     time.Time           `json:"timestamp"`
}

func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	nodes := make([]types.NodeStatus, 0, len(s.nodes))
	active, queue, models := 0, 0, 0
	for _, n := range s.nodes {
		nodes = append(nodes, n)
		if n.Healthy {
			active++
		}
		queue += n.QueueDepth
		models += len(n.LoadedModels)
	}
	modelList := make([]types.ModelRecord, 0, len(s.models))
	for _, m := range s.models {
		modelList = append(modelList, m)
	}
	reqs := make([]LiveRequest, 0, len(s.requests))
	for _, r := range s.requests {
		reqs = append(reqs, r)
	}
	streams := make([]LiveStream, 0, len(s.streams))
	for _, st := range s.streams {
		streams = append(streams, st)
	}
	fails := make([]FailureEvent, len(s.failures))
	copy(fails, s.failures)
	return Snapshot{
		Nodes: nodes, Models: modelList, LiveRequests: reqs, LiveStreams: streams,
		Failures: fails, ActiveNodes: active, TotalNodes: len(nodes),
		QueueDepth: queue, ActiveModels: models, Timestamp: time.Now().UTC(),
	}
}
