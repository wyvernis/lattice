package cost_test

import (
	"context"
	"testing"

	"github.com/lattice-ai/lattice/pkg/cost"
	"github.com/lattice-ai/lattice/pkg/types"
)

func TestCostFirstPrefersCheaper(t *testing.T) {
	e := cost.New(0.60)
	out := e.Optimize(context.Background(), []string{"qwen2.5-32b", "phi-3-mini", "llama3.1-8b"}, types.PolicyCost, 0.60)
	if len(out) == 0 {
		t.Fatal("empty")
	}
	if out[0] != "phi-3-mini" {
		t.Fatalf("expected phi-3-mini first, got %v", out)
	}
}
