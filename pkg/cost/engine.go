package cost

import (
	"context"
	"strings"

	"github.com/lattice-ai/lattice/pkg/types"
)

// Engine picks cheaper models when quality thresholds allow.
type Engine struct {
	QualityThreshold float64
	Catalog          map[string]ModelCost
}

// ModelCost describes economic + quality attributes.
type ModelCost struct {
	CostPerMillion float64
	QualityScore   float64
	LatencyFactor  float64
}

// DefaultCatalog seeds known model economics.
func DefaultCatalog() map[string]ModelCost {
	return map[string]ModelCost{
		"qwen2.5-coder-7b":  {0.10, 0.82, 1.0},
		"deepseek-coder-6.7b": {0.08, 0.80, 1.0},
		"codellama-7b":      {0.09, 0.75, 1.1},
		"deepseek-r1-7b":    {0.20, 0.90, 1.2},
		"qwen2.5-32b":       {0.50, 0.95, 1.8},
		"llama3.1-8b":       {0.12, 0.85, 1.0},
		"mistral-7b":        {0.10, 0.80, 0.95},
		"phi-3-mini":        {0.04, 0.65, 0.6},
		"aya-23-8b":         {0.11, 0.78, 1.0},
		"nllb-200":          {0.06, 0.70, 0.8},
		"qwen2-vl-7b":       {0.18, 0.85, 1.3},
		"llava-1.6-7b":      {0.15, 0.80, 1.2},
		"nomic-embed-text":  {0.02, 0.90, 0.3},
		"bge-small-en":      {0.01, 0.85, 0.2},
	}
}

// New creates a cost engine.
func New(threshold float64) *Engine {
	if threshold <= 0 {
		threshold = 0.75
	}
	return &Engine{QualityThreshold: threshold, Catalog: DefaultCatalog()}
}

// Optimize reorders or substitutes candidates based on policy.
func (e *Engine) Optimize(ctx context.Context, candidates []string, policy types.RoutingPolicy, minQuality float64) []string {
	if minQuality <= 0 {
		minQuality = e.QualityThreshold
	}
	if len(candidates) == 0 {
		return candidates
	}
	type scored struct {
		name  string
		score float64
	}
	items := make([]scored, 0, len(candidates))
	for _, c := range candidates {
		mc, ok := e.Catalog[c]
		if !ok {
			mc = ModelCost{CostPerMillion: 0.15, QualityScore: 0.75, LatencyFactor: 1}
		}
		if policy == types.PolicyCost && mc.QualityScore < minQuality {
			continue
		}
		var score float64
		switch policy {
		case types.PolicyCost:
			score = mc.QualityScore / (mc.CostPerMillion + 0.001)
		case types.PolicyQuality:
			score = mc.QualityScore*2 - mc.CostPerMillion*0.1
		case types.PolicyLatency:
			score = 1.0/mc.LatencyFactor + mc.QualityScore*0.2
		default: // balanced
			score = mc.QualityScore*0.5 + (1.0/(mc.CostPerMillion+0.01))*0.3 + (1.0/mc.LatencyFactor)*0.2
		}
		// prefer smaller when cost-first and quality ok
		if policy == types.PolicyCost && (strings.Contains(c, "mini") || strings.Contains(c, "3b") || strings.Contains(c, "7b")) {
			score += 0.1
		}
		items = append(items, scored{c, score})
	}
	if len(items) == 0 {
		return candidates
	}
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].score > items[i].score {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.name
	}
	return out
}

// Estimate returns USD cost for a token budget.
func (e *Engine) Estimate(model string, tokens int) float64 {
	mc, ok := e.Catalog[model]
	if !ok {
		mc = ModelCost{CostPerMillion: 0.15}
	}
	return (float64(tokens) / 1_000_000.0) * mc.CostPerMillion
}

// Compare reports large vs small model tradeoff.
func (e *Engine) Compare(large, small string, tokens int) map[string]interface{} {
	return map[string]interface{}{
		"large":          large,
		"small":          small,
		"large_cost_usd": e.Estimate(large, tokens),
		"small_cost_usd": e.Estimate(small, tokens),
		"savings_usd":    e.Estimate(large, tokens) - e.Estimate(small, tokens),
		"large_quality":  e.Catalog[large].QualityScore,
		"small_quality":  e.Catalog[small].QualityScore,
		"meets_threshold": e.Catalog[small].QualityScore >= e.QualityThreshold,
	}
}
