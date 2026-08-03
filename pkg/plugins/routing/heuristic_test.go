package routing_test

import (
	"context"
	"testing"

	"github.com/lattice-ai/lattice/pkg/plugins/routing"
	"github.com/lattice-ai/lattice/pkg/types"
)

func TestClassifyCoding(t *testing.T) {
	r := routing.NewHeuristicRouter(routing.DefaultPolicy())
	req := &types.InferenceRequest{
		Messages: []types.Message{{Role: types.RoleUser, Content: "Fix this Python bug in my function"}},
	}
	c, err := r.Classify(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if c.Category != routing.CatCoding {
		t.Fatalf("expected coding, got %s", c.Category)
	}
	models, err := r.CandidateModels(context.Background(), c, types.PolicyBalanced)
	if err != nil || len(models) == 0 {
		t.Fatalf("expected candidates, err=%v", err)
	}
	if models[0] != "qwen2.5-coder-7b" {
		t.Fatalf("unexpected top model %s", models[0])
	}
}

func TestClassifyReasoning(t *testing.T) {
	r := routing.NewHeuristicRouter(routing.DefaultPolicy())
	req := &types.InferenceRequest{Prompt: "Prove this theorem step by step with logic"}
	c, err := r.Classify(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if c.Category != routing.CatReasoning {
		t.Fatalf("expected reasoning, got %s", c.Category)
	}
}
