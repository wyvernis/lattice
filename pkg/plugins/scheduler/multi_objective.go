package scheduler

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/lattice-ai/lattice/pkg/types"
)

// MultiObjectiveScheduler scores nodes across latency, cost, load, throughput, energy.
type MultiObjectiveScheduler struct {
	Weights map[types.RoutingPolicy]weights
}

type weights struct {
	latency    float64
	cost       float64
	load       float64
	throughput float64
	energy     float64
	quality    float64
}

// New creates a scheduler with sensible default weight tables.
func New() *MultiObjectiveScheduler {
	return &MultiObjectiveScheduler{
		Weights: map[types.RoutingPolicy]weights{
			types.PolicyLatency:    {latency: 0.7, cost: 0.05, load: 0.15, throughput: 0.05, energy: 0.0, quality: 0.05},
			types.PolicyCost:       {latency: 0.1, cost: 0.6, load: 0.1, throughput: 0.05, energy: 0.05, quality: 0.1},
			types.PolicyQuality:    {latency: 0.15, cost: 0.05, load: 0.1, throughput: 0.05, energy: 0.0, quality: 0.65},
			types.PolicyThroughput: {latency: 0.1, cost: 0.05, load: 0.25, throughput: 0.55, energy: 0.05, quality: 0.0},
			types.PolicyEnergy:     {latency: 0.15, cost: 0.15, load: 0.1, throughput: 0.1, energy: 0.45, quality: 0.05},
			types.PolicyLeastLoad:  {latency: 0.15, cost: 0.05, load: 0.7, throughput: 0.05, energy: 0.05, quality: 0.0},
			types.PolicyBalanced:   {latency: 0.3, cost: 0.2, load: 0.2, throughput: 0.1, energy: 0.05, quality: 0.15},
		},
	}
}

func (s *MultiObjectiveScheduler) Name() string { return "multi_objective" }

// Select picks the best node/model/backend combination.
func (s *MultiObjectiveScheduler) Select(ctx context.Context, req *types.InferenceRequest, nodes []types.NodeStatus, models []string) (*types.ScheduleDecision, error) {
	policy := req.Policy
	if policy == "" {
		policy = types.PolicyBalanced
	}
	w := s.Weights[policy]
	if w == (weights{}) {
		w = s.Weights[types.PolicyBalanced]
	}

	var best *types.ScheduleDecision
	bestScore := math.Inf(-1)

	for _, node := range nodes {
		if !node.Healthy {
			continue
		}
		for _, model := range models {
			loaded, quant, backend := findLoaded(node, model)
			needLoad := !loaded
			if backend == "" {
				backend = pickBackend(node)
			}
			if quant == "" {
				quant = "auto"
			}
			score, reason, lat, cost := s.score(node, model, needLoad, w, req)
			if score > bestScore {
				bestScore = score
				best = &types.ScheduleDecision{
					NodeID:       node.ID,
					Model:        model,
					Backend:      backend,
					Quantization: quant,
					Score:        score,
					Reason:       reason,
					NeedLoad:     needLoad,
					EstLatencyMS: lat,
					EstCostUSD:   cost,
				}
			}
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no healthy nodes available for models %v", models)
	}
	return best, nil
}

func (s *MultiObjectiveScheduler) score(node types.NodeStatus, model string, needLoad bool, w weights, req *types.InferenceRequest) (float64, string, float64, float64) {
	estLat := node.EstLatencyMS + node.NetworkLatencyMS
	if estLat <= 0 {
		estLat = 100 + float64(node.QueueDepth)*20 + float64(node.ActiveRequests)*15
	}
	if needLoad {
		estLat += 800 // cold-start penalty
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 256
	}
	costPerM := node.CostPerMillion
	if costPerM <= 0 {
		costPerM = 0.2
	}
	estCost := (float64(maxTokens) / 1_000_000.0) * costPerM

	latScore := 1.0 / (1.0 + estLat/200.0)
	costScore := 1.0 / (1.0 + estCost*1000)
	loadScore := 1.0 - clamp(float64(node.ActiveRequests)/50.0+float64(node.QueueDepth)/30.0+avgGPU(node), 0, 1)
	thruScore := clamp(node.TokensPerSec/100.0, 0, 1)
	energyScore := 1.0
	if node.EnergyWatts > 0 {
		energyScore = 1.0 / (1.0 + node.EnergyWatts/300.0)
	}
	qualityScore := 0.7
	if strings.Contains(model, "32b") || strings.Contains(model, "70b") {
		qualityScore = 0.95
	} else if strings.Contains(model, "mini") || strings.Contains(model, "3b") {
		qualityScore = 0.55
	}

	total := w.latency*latScore + w.cost*costScore + w.load*loadScore +
		w.throughput*thruScore + w.energy*energyScore + w.quality*qualityScore

	reason := fmt.Sprintf("policy=%s lat=%.0fms cost=$%.6f load=%.2f model=%s node=%s",
		req.Policy, estLat, estCost, loadScore, model, node.ID)
	_ = time.Now()
	return total, reason, estLat, estCost
}

func findLoaded(n types.NodeStatus, model string) (bool, string, string) {
	for _, m := range n.LoadedModels {
		if m.Name == model || strings.HasPrefix(model, m.Name) || strings.HasPrefix(m.Name, model) {
			return true, m.Quantization, m.Backend
		}
	}
	return false, "", ""
}

func pickBackend(n types.NodeStatus) string {
	if len(n.Backends) > 0 {
		return n.Backends[0]
	}
	return "mock"
}

func avgGPU(n types.NodeStatus) float64 {
	if len(n.GPUs) == 0 {
		return n.CPUUtilization
	}
	var s float64
	for _, g := range n.GPUs {
		s += g.Utilization
	}
	return s / float64(len(n.GPUs))
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
