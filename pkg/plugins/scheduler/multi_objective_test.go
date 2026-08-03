package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/lattice-ai/lattice/pkg/plugins/scheduler"
	"github.com/lattice-ai/lattice/pkg/types"
)

func TestSelectPrefersLoadedHealthy(t *testing.T) {
	s := scheduler.New()
	nodes := []types.NodeStatus{
		{
			ID: "busy", Healthy: true, LastHeartbeat: time.Now(),
			ActiveRequests: 40, QueueDepth: 20, EstLatencyMS: 400, CostPerMillion: 0.5,
			Backends: []string{"mock"},
		},
		{
			ID: "free", Healthy: true, LastHeartbeat: time.Now(),
			ActiveRequests: 1, QueueDepth: 0, EstLatencyMS: 50, CostPerMillion: 0.1,
			Backends: []string{"mock"},
			LoadedModels: []types.LoadedModel{{Name: "llama3.1-8b", Backend: "mock", Quantization: "fp16"}},
		},
	}
	req := &types.InferenceRequest{ID: "1", Policy: types.PolicyLatency, MaxTokens: 64}
	d, err := s.Select(context.Background(), req, nodes, []string{"llama3.1-8b"})
	if err != nil {
		t.Fatal(err)
	}
	if d.NodeID != "free" {
		t.Fatalf("expected free node, got %s (%s)", d.NodeID, d.Reason)
	}
	if d.NeedLoad {
		t.Fatal("expected warm model")
	}
}
