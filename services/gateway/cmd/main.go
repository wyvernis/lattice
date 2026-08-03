package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/lattice-ai/lattice/pkg/auth"
	"github.com/lattice-ai/lattice/pkg/config"
	"github.com/lattice-ai/lattice/pkg/events"
	"github.com/lattice-ai/lattice/pkg/metrics"
	"github.com/lattice-ai/lattice/pkg/tracing"
	"github.com/lattice-ai/lattice/pkg/types"
)

type gateway struct {
	cfg    config.Config
	auth   *auth.APIKeyAuth
	bus    *events.Bus
	client *http.Client
	upgrader websocket.Upgrader
}

func main() {
	cfg := config.Load("gateway")
	cfg.HTTPAddr = envOr("HTTP_ADDR", ":8080")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdown, _ := tracing.Setup(ctx, "gateway", cfg.OTLPEndpoint)
	defer shutdown(context.Background())

	bus, _ := events.Connect(cfg.NATSURL)
	defer bus.Close()

	a := auth.NewAPIKeyAuth(cfg.APIKeys, cfg.JWTSecret)
	g := &gateway{
		cfg:  cfg,
		auth: a,
		bus:  bus,
		client: &http.Client{Timeout: cfg.RequestTimeout},
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","service":"gateway"}`))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ready":true}`))
	})

	secured := auth.Middleware(a)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/chat/completions" && r.Method == http.MethodPost:
			g.handleChat(w, r)
		case r.URL.Path == "/v1/completions" && r.Method == http.MethodPost:
			g.handleCompletion(w, r)
		case r.URL.Path == "/v1/embeddings" && r.Method == http.MethodPost:
			g.handleEmbeddings(w, r)
		case r.URL.Path == "/v1/batch" && r.Method == http.MethodPost:
			g.handleBatch(w, r)
		case r.URL.Path == "/v1/auth/token" && r.Method == http.MethodPost:
			g.handleToken(w, r)
		case r.URL.Path == "/v1/audit":
			writeJSON(w, a.AuditLog())
		case r.URL.Path == "/v1/ws/stream":
			g.handleWS(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	mux.Handle("/v1/", secured)

	// CORS for dashboard
	handler := cors(mux)

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: handler}
	go func() {
		slog.Info("gateway listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("gateway failed", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

type chatBody struct {
	Model       string          `json:"model"`
	Messages    []types.Message `json:"messages"`
	MaxTokens   int             `json:"max_tokens"`
	Temperature float64         `json:"temperature"`
	Stream      bool            `json:"stream"`
	Policy      string          `json:"policy"`
}

func (g *gateway) handleChat(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx, span := tracing.Start(r.Context(), "gateway.chat", map[string]string{"stage": tracing.StageGateway})
	defer span.End()
	tracing.AnnotateStage(span, tracing.StageGateway)

	var body chatBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	id := auth.IdentityFrom(ctx)
	tenant := "default"
	if id != nil {
		tenant = id.Tenant
	}
	req := &types.InferenceRequest{
		ID: uuid.NewString(), Type: types.RequestChat, Messages: body.Messages,
		Model: body.Model, MaxTokens: body.MaxTokens, Temperature: body.Temperature,
		Stream: body.Stream, Policy: types.RoutingPolicy(body.Policy),
		TenantID: tenant, CreatedAt: time.Now().UTC(), TraceID: tracing.TraceIDString(ctx),
	}
	if req.Policy == "" {
		req.Policy = types.PolicyBalanced
	}

	decision, err := g.routeAndSchedule(ctx, req)
	if err != nil {
		metrics.FailedRequests.WithLabelValues("gateway", "schedule").Inc()
		http.Error(w, err.Error(), 503)
		return
	}
	req.SelectedModel = decision.Model
	req.SelectedNode = decision.NodeID
	req.SelectedBackend = decision.Backend
	req.Quantization = decision.Quantization

	if body.Stream {
		g.proxyStream(ctx, w, req)
		metrics.RequestsTotal.WithLabelValues("gateway", "chat_stream", "ok").Inc()
		return
	}

	resp, err := g.infer(ctx, req)
	if err != nil {
		// fault tolerance: one retry via reschedule
		slog.Warn("infer failed, retrying", "err", err)
		decision2, err2 := g.routeAndSchedule(ctx, req)
		if err2 == nil {
			req.SelectedNode = decision2.NodeID
			req.SelectedBackend = decision2.Backend
			resp, err = g.infer(ctx, req)
		}
		if err != nil {
			metrics.FailedRequests.WithLabelValues("gateway", "infer").Inc()
			http.Error(w, err.Error(), 502)
			return
		}
	}
	metrics.RequestLatency.WithLabelValues("gateway", resp.Model, resp.Backend).Observe(time.Since(start).Seconds())
	metrics.RequestsTotal.WithLabelValues("gateway", "chat", "ok").Inc()
	metrics.CostUSD.WithLabelValues(resp.Model, tenant).Add(decision.EstCostUSD)

	writeJSON(w, map[string]interface{}{
		"id": resp.ID,
		"object": "chat.completion",
		"model": resp.Model,
		"choices": []map[string]interface{}{{
			"index": 0,
			"message": map[string]string{"role": "assistant", "content": resp.Content},
			"finish_reason": resp.FinishReason,
		}},
		"usage": resp.Usage,
		"lattice": map[string]interface{}{
			"node_id": resp.NodeID, "backend": resp.Backend, "latency_ms": resp.LatencyMS,
			"ttft_ms": resp.TTFTMS, "policy": req.Policy, "category": categoryOf(req),
			"trace_id": req.TraceID, "decision": decision,
		},
	})
}

func (g *gateway) handleCompletion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model       string  `json:"model"`
		Prompt      string  `json:"prompt"`
		MaxTokens   int     `json:"max_tokens"`
		Temperature float64 `json:"temperature"`
		Stream      bool    `json:"stream"`
		Policy      string  `json:"policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	req := &types.InferenceRequest{
		ID: uuid.NewString(), Type: types.RequestCompletion, Prompt: body.Prompt,
		Model: body.Model, MaxTokens: body.MaxTokens, Temperature: body.Temperature,
		Stream: body.Stream, Policy: types.RoutingPolicy(body.Policy), CreatedAt: time.Now().UTC(),
	}
	decision, err := g.routeAndSchedule(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	req.SelectedModel, req.SelectedNode, req.SelectedBackend, req.Quantization =
		decision.Model, decision.NodeID, decision.Backend, decision.Quantization
	if body.Stream {
		g.proxyStream(r.Context(), w, req)
		return
	}
	resp, err := g.infer(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, map[string]interface{}{
		"id": resp.ID, "object": "text_completion", "model": resp.Model,
		"choices": []map[string]interface{}{{"text": resp.Content, "finish_reason": resp.FinishReason}},
		"usage": resp.Usage,
		"lattice": map[string]interface{}{"node_id": resp.NodeID, "backend": resp.Backend},
	})
}

func (g *gateway) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	req := &types.InferenceRequest{
		ID: uuid.NewString(), Type: types.RequestEmbedding, Input: body.Input, Model: body.Model,
		Policy: types.PolicyLatency, CreatedAt: time.Now().UTC(),
	}
	decision, err := g.routeAndSchedule(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	req.SelectedModel, req.SelectedNode, req.SelectedBackend = decision.Model, decision.NodeID, decision.Backend
	nodeURL := g.nodeURL(decision.NodeID)
	payload, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, nodeURL+"/v1/embed", bytes.NewReader(payload))
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(httpReq)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (g *gateway) handleBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Requests []chatBody `json:"requests"`
		Policy   string     `json:"policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	results := make([]interface{}, 0, len(body.Requests))
	for _, b := range body.Requests {
		req := &types.InferenceRequest{
			ID: uuid.NewString(), Type: types.RequestBatch, Messages: b.Messages,
			Model: b.Model, MaxTokens: b.MaxTokens, Temperature: b.Temperature,
			Policy: types.RoutingPolicy(first(body.Policy, b.Policy, string(types.PolicyThroughput))),
			CreatedAt: time.Now().UTC(),
		}
		decision, err := g.routeAndSchedule(r.Context(), req)
		if err != nil {
			results = append(results, map[string]string{"error": err.Error()})
			continue
		}
		req.SelectedModel, req.SelectedNode, req.SelectedBackend, req.Quantization =
			decision.Model, decision.NodeID, decision.Backend, decision.Quantization
		resp, err := g.infer(r.Context(), req)
		if err != nil {
			results = append(results, map[string]string{"error": err.Error()})
			continue
		}
		results = append(results, resp)
	}
	writeJSON(w, map[string]interface{}{"object": "list", "data": results})
}

func (g *gateway) handleToken(w http.ResponseWriter, r *http.Request) {
	id := auth.IdentityFrom(r.Context())
	if id == nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	tok, err := g.auth.IssueJWT(id.Subject, id.Tenant, id.Roles, 24*time.Hour)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"token": tok, "token_type": "Bearer", "expires_in": "86400"})
}

func (g *gateway) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := g.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	metrics.StreamingSessions.Inc()
	defer metrics.StreamingSessions.Dec()

	_, data, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var body chatBody
	if err := json.Unmarshal(data, &body); err != nil {
		_ = conn.WriteJSON(map[string]string{"error": err.Error()})
		return
	}
	req := &types.InferenceRequest{
		ID: uuid.NewString(), Type: types.RequestChat, Messages: body.Messages,
		Model: body.Model, MaxTokens: body.MaxTokens, Temperature: body.Temperature,
		Stream: true, Policy: types.RoutingPolicy(body.Policy), CreatedAt: time.Now().UTC(),
	}
	decision, err := g.routeAndSchedule(r.Context(), req)
	if err != nil {
		_ = conn.WriteJSON(map[string]string{"error": err.Error()})
		return
	}
	req.SelectedModel, req.SelectedNode, req.SelectedBackend, req.Quantization =
		decision.Model, decision.NodeID, decision.Backend, decision.Quantization

	nodeURL := g.nodeURL(decision.NodeID)
	payload, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, nodeURL+"/v1/infer/stream", bytes.NewReader(payload))
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(httpReq)
	if err != nil {
		_ = conn.WriteJSON(map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var chunk types.StreamChunk
		if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
			continue
		}
		if err := conn.WriteJSON(chunk); err != nil {
			return
		}
		if chunk.Done {
			return
		}
	}
}

func (g *gateway) routeAndSchedule(ctx context.Context, req *types.InferenceRequest) (*types.ScheduleDecision, error) {
	ctx, span := tracing.Start(ctx, "gateway.route_schedule", nil)
	defer span.End()

	// classify
	classBody, _ := json.Marshal(req)
	classReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.RouterURL+"/v1/classify", bytes.NewReader(classBody))
	classReq.Header.Set("Content-Type", "application/json")
	classResp, err := g.client.Do(classReq)
	if err != nil {
		return nil, fmt.Errorf("router: %w", err)
	}
	defer classResp.Body.Close()
	var classified struct {
		Classification *types.Classification `json:"classification"`
		Candidates     []string              `json:"candidates"`
		SelectedModel  string                `json:"selected_model"`
	}
	if err := json.NewDecoder(classResp.Body).Decode(&classified); err != nil {
		return nil, err
	}
	req.Classification = classified.Classification
	if req.Model != "" {
		classified.Candidates = []string{req.Model}
		classified.SelectedModel = req.Model
	}
	models := classified.Candidates
	if len(models) == 0 && classified.SelectedModel != "" {
		models = []string{classified.SelectedModel}
	}

	schedBody, _ := json.Marshal(map[string]interface{}{"request": req, "models": models})
	schedReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.SchedulerURL+"/v1/schedule", bytes.NewReader(schedBody))
	schedReq.Header.Set("Content-Type", "application/json")
	schedResp, err := g.client.Do(schedReq)
	if err != nil {
		return nil, fmt.Errorf("scheduler: %w", err)
	}
	defer schedResp.Body.Close()
	if schedResp.StatusCode >= 400 {
		b, _ := io.ReadAll(schedResp.Body)
		return nil, fmt.Errorf("scheduler: %s", string(b))
	}
	var decision types.ScheduleDecision
	if err := json.NewDecoder(schedResp.Body).Decode(&decision); err != nil {
		return nil, err
	}
	_ = g.bus.Publish(events.SubjectRequests, types.ClusterEvent{
		Type: "request.accepted", Source: "gateway",
		Payload: map[string]interface{}{"id": req.ID, "model": decision.Model, "node": decision.NodeID},
	})
	return &decision, nil
}

func (g *gateway) infer(ctx context.Context, req *types.InferenceRequest) (*types.InferenceResponse, error) {
	nodeURL := g.nodeURL(req.SelectedNode)
	payload, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, nodeURL+"/v1/infer", bytes.NewReader(payload))
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("worker %s: %s", req.SelectedNode, string(b))
	}
	var out types.InferenceResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (g *gateway) proxyStream(ctx context.Context, w http.ResponseWriter, req *types.InferenceRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	metrics.StreamingSessions.Inc()
	defer metrics.StreamingSessions.Dec()

	nodeURL := g.nodeURL(req.SelectedNode)
	payload, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, nodeURL+"/v1/infer/stream", bytes.NewReader(payload))
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(httpReq)
	if err != nil {
		fmt.Fprintf(w, "data: {\"error\":%q}\n\n", err.Error())
		flusher.Flush()
		return
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	idx := 0
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var chunk types.StreamChunk
		_ = json.Unmarshal([]byte(raw), &chunk)
		// OpenAI-compatible SSE framing
		frame := map[string]interface{}{
			"id": req.ID, "object": "chat.completion.chunk", "model": req.SelectedModel,
			"choices": []map[string]interface{}{{
				"index": 0,
				"delta": map[string]string{"content": chunk.Delta},
				"finish_reason": nil,
			}},
			"lattice": map[string]interface{}{
				"ttft_ms": chunk.TTFTMS, "tokens_per_sec": chunk.TokensPerSec,
				"node_id": req.SelectedNode, "done": chunk.Done,
			},
		}
		if chunk.Done {
			frame["choices"].([]map[string]interface{})[0]["finish_reason"] = "stop"
			frame["choices"].([]map[string]interface{})[0]["delta"] = map[string]string{}
		}
		b, _ := json.Marshal(frame)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
		idx++
		if chunk.Done {
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
	}
	_ = idx
}

func (g *gateway) nodeURL(nodeID string) string {
	// resolve via scheduler cluster view
	resp, err := g.client.Get(g.cfg.SchedulerURL + "/v1/cluster")
	if err == nil {
		defer resp.Body.Close()
		var snap struct {
			Nodes []types.NodeStatus `json:"nodes"`
		}
		if json.NewDecoder(resp.Body).Decode(&snap) == nil {
			for _, n := range snap.Nodes {
				if n.ID == nodeID && n.Address != "" {
					return strings.TrimRight(n.Address, "/")
				}
			}
			if len(snap.Nodes) > 0 && snap.Nodes[0].Address != "" {
				return strings.TrimRight(snap.Nodes[0].Address, "/")
			}
		}
	}
	return envOr("WORKER_URL", "http://localhost:8084")
}

func categoryOf(req *types.InferenceRequest) string {
	if req.Classification != nil {
		return req.Classification.Category
	}
	return ""
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-API-Key")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
