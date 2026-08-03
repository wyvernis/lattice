package backends

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/lattice-ai/lattice/pkg/types"
)

// MockBackend simulates token generation for local/dev clusters without GPUs.
type MockBackend struct {
	mu     sync.Mutex
	loaded map[string]time.Time
	tps    float64
}

func NewMock(tps float64) *MockBackend {
	if tps <= 0 {
		tps = 80
	}
	return &MockBackend{loaded: map[string]time.Time{}, tps: tps}
}

func (m *MockBackend) Name() string { return "mock" }

func (m *MockBackend) Supports(model string) bool { return true }

func (m *MockBackend) Health(ctx context.Context) error { return nil }

func (m *MockBackend) LoadModel(ctx context.Context, model, quantization string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(50 * time.Millisecond):
	}
	m.loaded[model] = time.Now()
	return nil
}

func (m *MockBackend) UnloadModel(ctx context.Context, model string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.loaded, model)
	return nil
}

func (m *MockBackend) Generate(ctx context.Context, req *types.InferenceRequest) (*types.InferenceResponse, error) {
	start := time.Now()
	model := req.SelectedModel
	if model == "" {
		model = req.Model
	}
	_ = m.LoadModel(ctx, model, req.Quantization)
	prompt := collectPrompt(req)
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 64
	}
	content := synthesize(prompt, model, maxTok)
	tokens := len(strings.Fields(content))
	delay := time.Duration(float64(tokens)/m.tps*1000) * time.Millisecond
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(delay):
	}
	return &types.InferenceResponse{
		ID:           req.ID,
		Model:        model,
		Content:      content,
		Usage:        types.Usage{PromptTokens: len(strings.Fields(prompt)), CompletionTokens: tokens, TotalTokens: len(strings.Fields(prompt)) + tokens},
		FinishReason: "stop",
		NodeID:       req.SelectedNode,
		Backend:      m.Name(),
		LatencyMS:    time.Since(start).Milliseconds(),
		TTFTMS:       20 + rand.Int63n(40),
		CreatedAt:    time.Now().UTC(),
	}, nil
}

func (m *MockBackend) GenerateStream(ctx context.Context, req *types.InferenceRequest, emit func(types.StreamChunk) error) error {
	start := time.Now()
	model := req.SelectedModel
	if model == "" {
		model = req.Model
	}
	_ = m.LoadModel(ctx, model, req.Quantization)
	prompt := collectPrompt(req)
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 64
	}
	content := synthesize(prompt, model, maxTok)
	words := strings.Fields(content)
	interval := time.Second / time.Duration(m.tps)
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	var ttft int64
	for i, w := range words {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		delta := w
		if i < len(words)-1 {
			delta += " "
		}
		chunk := types.StreamChunk{ID: req.ID, Delta: delta, Index: i}
		if i == 0 {
			ttft = time.Since(start).Milliseconds()
			chunk.TTFTMS = ttft
		}
		elapsed := time.Since(start).Seconds()
		if elapsed > 0 {
			chunk.TokensPerSec = float64(i+1) / elapsed
		}
		if err := emit(chunk); err != nil {
			return err
		}
	}
	return emit(types.StreamChunk{ID: req.ID, Done: true, Index: len(words), TTFTMS: ttft, TokensPerSec: float64(len(words)) / math.Max(time.Since(start).Seconds(), 0.001)})
}

func (m *MockBackend) Embed(ctx context.Context, req *types.InferenceRequest) ([]float64, error) {
	text := req.Input
	if text == "" {
		text = collectPrompt(req)
	}
	dim := 384
	vec := make([]float64, dim)
	seed := int64(0)
	for _, c := range text {
		seed += int64(c)
	}
	r := rand.New(rand.NewSource(seed))
	var norm float64
	for i := range vec {
		vec[i] = r.NormFloat64()
		norm += vec[i] * vec[i]
	}
	norm = math.Sqrt(norm)
	for i := range vec {
		vec[i] /= norm
	}
	return vec, nil
}

func collectPrompt(req *types.InferenceRequest) string {
	if req.Prompt != "" {
		return req.Prompt
	}
	var b strings.Builder
	for _, m := range req.Messages {
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

func synthesize(prompt, model string, maxTok int) string {
	snippet := prompt
	if len(snippet) > 120 {
		snippet = snippet[:120] + "…"
	}
	base := fmt.Sprintf("[lattice/%s] Acknowledged. %s → synthesized response covering the request with production-grade routing, scheduling, and streaming semantics.", model, strings.ReplaceAll(snippet, "\n", " "))
	words := strings.Fields(base)
	if len(words) > maxTok {
		words = words[:maxTok]
	}
	return strings.Join(words, " ")
}
