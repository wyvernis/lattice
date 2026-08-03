package backends

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lattice-ai/lattice/pkg/types"
)

// OpenAICompat talks to any OpenAI-compatible inference server
// (vLLM, TensorRT-LLM openai frontend, SGLang, Ollama, llama.cpp server).
type OpenAICompat struct {
	name       string
	baseURL    string
	apiKey     string
	httpClient *http.Client
	loaded     map[string]bool
}

// NewOpenAICompat constructs a pluggable backend.
func NewOpenAICompat(name, baseURL, apiKey string) *OpenAICompat {
	return &OpenAICompat{
		name:       name,
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
		loaded:     map[string]bool{},
	}
}

func (o *OpenAICompat) Name() string { return o.name }

func (o *OpenAICompat) Supports(model string) bool { return true }

func (o *OpenAICompat) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := o.httpClient.Do(req)
	if err != nil {
		// try models endpoint as fallback
		req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"/v1/models", nil)
		o.setAuth(req2)
		resp2, err2 := o.httpClient.Do(req2)
		if err2 != nil {
			return err
		}
		defer resp2.Body.Close()
		if resp2.StatusCode >= 400 {
			return fmt.Errorf("%s unhealthy: %s", o.name, resp2.Status)
		}
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s unhealthy: %s", o.name, resp.Status)
	}
	return nil
}

func (o *OpenAICompat) LoadModel(ctx context.Context, model, quantization string) error {
	o.loaded[model] = true
	return nil
}

func (o *OpenAICompat) UnloadModel(ctx context.Context, model string) error {
	delete(o.loaded, model)
	return nil
}

func (o *OpenAICompat) Generate(ctx context.Context, req *types.InferenceRequest) (*types.InferenceResponse, error) {
	start := time.Now()
	model := firstNonEmpty(req.SelectedModel, req.Model)
	body := map[string]interface{}{
		"model":       model,
		"messages":    toOpenAIMessages(req),
		"max_tokens":  defaultInt(req.MaxTokens, 256),
		"temperature": req.Temperature,
		"stream":      false,
	}
	if len(req.Messages) == 0 && req.Prompt != "" {
		delete(body, "messages")
		body["prompt"] = req.Prompt
		var raw map[string]interface{}
		if err := o.postJSON(ctx, "/v1/completions", body, &raw); err != nil {
			return nil, err
		}
		content := ""
		if choices, ok := raw["choices"].([]interface{}); ok && len(choices) > 0 {
			if c, ok := choices[0].(map[string]interface{}); ok {
				content, _ = c["text"].(string)
			}
		}
		return &types.InferenceResponse{
			ID: req.ID, Model: model, Content: content, Backend: o.name,
			NodeID: req.SelectedNode, LatencyMS: time.Since(start).Milliseconds(),
			FinishReason: "stop", CreatedAt: time.Now().UTC(),
			Usage: extractUsage(raw),
		}, nil
	}
	var raw map[string]interface{}
	if err := o.postJSON(ctx, "/v1/chat/completions", body, &raw); err != nil {
		return nil, err
	}
	content := ""
	if choices, ok := raw["choices"].([]interface{}); ok && len(choices) > 0 {
		if c, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := c["message"].(map[string]interface{}); ok {
				content, _ = msg["content"].(string)
			}
		}
	}
	return &types.InferenceResponse{
		ID: req.ID, Model: model, Content: content, Backend: o.name,
		NodeID: req.SelectedNode, LatencyMS: time.Since(start).Milliseconds(),
		FinishReason: "stop", CreatedAt: time.Now().UTC(),
		Usage: extractUsage(raw),
	}, nil
}

func (o *OpenAICompat) GenerateStream(ctx context.Context, req *types.InferenceRequest, emit func(types.StreamChunk) error) error {
	start := time.Now()
	model := firstNonEmpty(req.SelectedModel, req.Model)
	body := map[string]interface{}{
		"model":       model,
		"messages":    toOpenAIMessages(req),
		"max_tokens":  defaultInt(req.MaxTokens, 256),
		"temperature": req.Temperature,
		"stream":      true,
	}
	payload, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	o.setAuth(httpReq)
	resp, err := o.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s stream error: %s", o.name, string(b))
	}
	reader := bufio.NewReader(resp.Body)
	idx := 0
	var ttft int64
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		delta := ""
		if choices, ok := chunk["choices"].([]interface{}); ok && len(choices) > 0 {
			if c, ok := choices[0].(map[string]interface{}); ok {
				if d, ok := c["delta"].(map[string]interface{}); ok {
					delta, _ = d["content"].(string)
				}
			}
		}
		if delta == "" {
			continue
		}
		sc := types.StreamChunk{ID: req.ID, Delta: delta, Index: idx}
		if idx == 0 {
			ttft = time.Since(start).Milliseconds()
			sc.TTFTMS = ttft
		}
		elapsed := time.Since(start).Seconds()
		if elapsed > 0 {
			sc.TokensPerSec = float64(idx+1) / elapsed
		}
		if err := emit(sc); err != nil {
			return err
		}
		idx++
	}
	return emit(types.StreamChunk{ID: req.ID, Done: true, Index: idx, TTFTMS: ttft})
}

func (o *OpenAICompat) Embed(ctx context.Context, req *types.InferenceRequest) ([]float64, error) {
	model := firstNonEmpty(req.SelectedModel, req.Model)
	body := map[string]interface{}{
		"model": model,
		"input": firstNonEmpty(req.Input, req.Prompt),
	}
	var raw map[string]interface{}
	if err := o.postJSON(ctx, "/v1/embeddings", body, &raw); err != nil {
		return nil, err
	}
	if data, ok := raw["data"].([]interface{}); ok && len(data) > 0 {
		if d, ok := data[0].(map[string]interface{}); ok {
			if emb, ok := d["embedding"].([]interface{}); ok {
				out := make([]float64, len(emb))
				for i, v := range emb {
					out[i], _ = v.(float64)
				}
				return out, nil
			}
		}
	}
	return nil, fmt.Errorf("empty embedding response")
}

func (o *OpenAICompat) postJSON(ctx context.Context, path string, body interface{}, out interface{}) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	o.setAuth(req)
	resp, err := o.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s error %s: %s", o.name, resp.Status, string(b))
	}
	return json.Unmarshal(b, out)
}

func (o *OpenAICompat) setAuth(req *http.Request) {
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
}

func toOpenAIMessages(req *types.InferenceRequest) []map[string]string {
	if len(req.Messages) == 0 {
		return []map[string]string{{"role": "user", "content": req.Prompt}}
	}
	out := make([]map[string]string, len(req.Messages))
	for i, m := range req.Messages {
		out[i] = map[string]string{"role": string(m.Role), "content": m.Content}
	}
	return out
}

func extractUsage(raw map[string]interface{}) types.Usage {
	u, _ := raw["usage"].(map[string]interface{})
	if u == nil {
		return types.Usage{}
	}
	pt, _ := u["prompt_tokens"].(float64)
	ct, _ := u["completion_tokens"].(float64)
	tt, _ := u["total_tokens"].(float64)
	return types.Usage{PromptTokens: int(pt), CompletionTokens: int(ct), TotalTokens: int(tt)}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func defaultInt(v, d int) int {
	if v <= 0 {
		return d
	}
	return v
}

// Factory builds backends from config name.
func Factory(name, baseURL, apiKey string) interface {
	Name() string
} {
	switch strings.ToLower(name) {
	case "vllm", "tensorrt-llm", "sglang", "llamacpp", "ollama":
		return NewOpenAICompat(name, baseURL, apiKey)
	default:
		return NewMock(80)
	}
}
