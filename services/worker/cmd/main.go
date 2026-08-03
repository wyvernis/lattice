package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/lattice-ai/lattice/pkg/batching"
	"github.com/lattice-ai/lattice/pkg/config"
	"github.com/lattice-ai/lattice/pkg/events"
	"github.com/lattice-ai/lattice/pkg/lifecycle"
	"github.com/lattice-ai/lattice/pkg/metrics"
	"github.com/lattice-ai/lattice/pkg/plugins"
	"github.com/lattice-ai/lattice/pkg/plugins/backends"
	"github.com/lattice-ai/lattice/pkg/tracing"
	"github.com/lattice-ai/lattice/pkg/types"
)

type worker struct {
	id       string
	cluster  string
	addr     string
	backends map[string]plugins.BackendPlugin
	life     *lifecycle.Manager
	batcher  *batching.Batcher
	bus      *events.Bus
	schedURL string

	active int64
	queue  int64
	tps    float64
	mu     sync.Mutex
	chaos  chaosFlags
}

type chaosFlags struct {
	crash   bool
	oom     bool
	delayMS int
}

type loaderAdapter struct {
	w *worker
}

func (l loaderAdapter) Load(ctx context.Context, model, quant string) error {
	b := l.w.pickBackend("")
	return b.LoadModel(ctx, model, quant)
}
func (l loaderAdapter) Unload(ctx context.Context, model string) error {
	b := l.w.pickBackend("")
	return b.UnloadModel(ctx, model)
}

func main() {
	cfg := config.Load("worker")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdown, _ := tracing.Setup(ctx, "worker", cfg.OTLPEndpoint)
	defer shutdown(context.Background())

	bus, _ := events.Connect(cfg.NATSURL)
	defer bus.Close()

	nodeID := os.Getenv("NODE_ID")
	if nodeID == "" {
		nodeID = "node-" + uuid.NewString()[:8]
	}
	advertise := os.Getenv("ADVERTISE_URL")
	if advertise == "" {
		if ip := os.Getenv("POD_IP"); ip != "" {
			advertise = "http://" + ip + cfg.HTTPAddr
		} else {
			advertise = "http://127.0.0.1" + cfg.HTTPAddr
		}
	}
	if !strings.HasPrefix(advertise, "http") {
		advertise = "http://" + advertise
		if !strings.Contains(advertise[len("http://"):], ":") {
			advertise += cfg.HTTPAddr
		}
	}

	w := &worker{
		id:       nodeID,
		cluster:  cfg.ClusterName,
		addr:     advertise,
		backends: map[string]plugins.BackendPlugin{},
		bus:      bus,
		schedURL: cfg.SchedulerURL,
		tps:      85,
	}
	w.backends["mock"] = backends.NewMock(85)
	if u := os.Getenv("VLLM_URL"); u != "" {
		w.backends["vllm"] = backends.NewOpenAICompat("vllm", u, os.Getenv("VLLM_API_KEY"))
	}
	if u := os.Getenv("OLLAMA_URL"); u != "" {
		w.backends["ollama"] = backends.NewOpenAICompat("ollama", u, "")
	}
	if u := os.Getenv("SGLANG_URL"); u != "" {
		w.backends["sglang"] = backends.NewOpenAICompat("sglang", u, "")
	}
	if u := os.Getenv("LLAMACPP_URL"); u != "" {
		w.backends["llamacpp"] = backends.NewOpenAICompat("llamacpp", u, "")
	}

	w.life = lifecycle.NewManager(loaderAdapter{w}, config.GetInt("MAX_LOADED_MODELS", 4))
	w.batcher = batching.New(batching.Config{Strategy: batching.StrategyAdaptive, MaxBatchSize: 8, MaxWait: 20 * time.Millisecond})

	go w.batcher.Run(ctx, w.handleBatch)
	go w.life.RunPredictiveLoop(ctx, "auto")
	go w.heartbeatLoop(ctx)

	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	mux.HandleFunc("/healthz", func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		crash := w.chaos.crash
		w.mu.Unlock()
		if crash {
			http.Error(rw, `{"status":"crashed"}`, http.StatusServiceUnavailable)
			return
		}
		_, _ = rw.Write([]byte(`{"status":"ok","node":"` + w.id + `"}`))
	})
	mux.HandleFunc("/v1/infer", w.handleInfer)
	mux.HandleFunc("/v1/infer/stream", w.handleInferStream)
	mux.HandleFunc("/v1/embed", w.handleEmbed)
	mux.HandleFunc("/v1/models/load", func(rw http.ResponseWriter, r *http.Request) {
		var body struct{ Model, Quantization string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if err := w.life.EnsureLoaded(r.Context(), body.Model, body.Quantization, "auto", 8000); err != nil {
			http.Error(rw, err.Error(), 500)
			return
		}
		writeJSON(rw, map[string]string{"status": "loaded", "model": body.Model})
	})
	mux.HandleFunc("/v1/models/unload", func(rw http.ResponseWriter, r *http.Request) {
		var body struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = w.life.Unload(r.Context(), body.Model)
		writeJSON(rw, map[string]string{"status": "unloaded", "model": body.Model})
	})
	mux.HandleFunc("/v1/chaos", func(rw http.ResponseWriter, r *http.Request) {
		var body struct {
			Action string `json:"action"`
			DelayMS int   `json:"delay_ms"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.mu.Lock()
		defer w.mu.Unlock()
		switch body.Action {
		case "crash":
			w.chaos.crash = true
		case "recover":
			w.chaos.crash = false
			w.chaos.oom = false
			w.chaos.delayMS = 0
		case "oom":
			w.chaos.oom = true
		case "delay":
			w.chaos.delayMS = body.DelayMS
		}
		writeJSON(rw, w.chaos)
	})
	mux.HandleFunc("/v1/status", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, w.status())
	})

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux}
	go func() {
		slog.Info("worker listening", "addr", cfg.HTTPAddr, "node", w.id)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("worker failed", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func (w *worker) pickBackend(name string) plugins.BackendPlugin {
	if name != "" {
		if b, ok := w.backends[name]; ok {
			return b
		}
	}
	if b, ok := w.backends["mock"]; ok {
		return b
	}
	for _, b := range w.backends {
		return b
	}
	return backends.NewMock(80)
}

func (w *worker) handleInfer(rw http.ResponseWriter, r *http.Request) {
	ctx, span := tracing.Start(r.Context(), "worker.infer", map[string]string{"stage": tracing.StageBackend})
	defer span.End()
	tracing.AnnotateStage(span, tracing.StageBackend)

	if !w.ready(rw) {
		return
	}
	var req types.InferenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, err.Error(), 400)
		return
	}
	atomic.AddInt64(&w.active, 1)
	atomic.AddInt64(&w.queue, 1)
	defer func() {
		atomic.AddInt64(&w.active, -1)
		atomic.AddInt64(&w.queue, -1)
	}()

	if req.Stream {
		http.Error(rw, "use /v1/infer/stream", 400)
		return
	}
	resp, err := w.batcher.Submit(ctx, &req)
	if err != nil {
		// fallback direct
		b := w.pickBackend(req.SelectedBackend)
		resp, err = b.Generate(ctx, &req)
	}
	if err != nil {
		metrics.FailedRequests.WithLabelValues("worker", "infer").Inc()
		http.Error(rw, err.Error(), 500)
		return
	}
	resp.NodeID = w.id
	cat := ""
	if req.Classification != nil {
		cat = req.Classification.Category
	}
	w.life.Touch(resp.Model, cat)
	metrics.RequestLatency.WithLabelValues("worker", resp.Model, resp.Backend).Observe(float64(resp.LatencyMS) / 1000)
	writeJSON(rw, resp)
}

func (w *worker) handleInferStream(rw http.ResponseWriter, r *http.Request) {
	ctx, span := tracing.Start(r.Context(), "worker.stream", map[string]string{"stage": tracing.StageStream})
	defer span.End()
	tracing.AnnotateStage(span, tracing.StageStream)

	if !w.ready(rw) {
		return
	}
	var req types.InferenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, err.Error(), 400)
		return
	}
	atomic.AddInt64(&w.active, 1)
	metrics.StreamingSessions.Inc()
	defer func() {
		atomic.AddInt64(&w.active, -1)
		metrics.StreamingSessions.Dec()
	}()

	flusher, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", 500)
		return
	}
	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")

	b := w.pickBackend(req.SelectedBackend)
	_ = w.life.EnsureLoaded(ctx, first(req.SelectedModel, req.Model), req.Quantization, b.Name(), 8000)
	err := b.GenerateStream(ctx, &req, func(chunk types.StreamChunk) error {
		data, _ := json.Marshal(chunk)
		if _, err := fmt.Fprintf(rw, "data: %s\n\n", data); err != nil {
			return err
		}
		flusher.Flush()
		if chunk.TokensPerSec > 0 {
			w.tps = chunk.TokensPerSec
			metrics.TokensPerSec.WithLabelValues(w.id, first(req.SelectedModel, req.Model)).Set(chunk.TokensPerSec)
		}
		if chunk.TTFTMS > 0 {
			metrics.TTFT.WithLabelValues(first(req.SelectedModel, req.Model), b.Name()).Observe(float64(chunk.TTFTMS) / 1000)
		}
		return nil
	})
	if err != nil {
		metrics.FailedRequests.WithLabelValues("worker", "stream").Inc()
		fmt.Fprintf(rw, "data: {\"error\":%q}\n\n", err.Error())
		flusher.Flush()
	}
}

func (w *worker) handleEmbed(rw http.ResponseWriter, r *http.Request) {
	var req types.InferenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, err.Error(), 400)
		return
	}
	b := w.pickBackend(req.SelectedBackend)
	vec, err := b.Embed(r.Context(), &req)
	if err != nil {
		http.Error(rw, err.Error(), 500)
		return
	}
	writeJSON(rw, map[string]interface{}{"embedding": vec, "model": first(req.SelectedModel, req.Model), "node": w.id})
}

func (w *worker) handleBatch(ctx context.Context, batch *types.BatchRequest) ([]batching.Result, error) {
	metrics.BatchEfficiency.WithLabelValues(w.id).Set(float64(len(batch.Requests)) / 8.0)
	results := make([]batching.Result, len(batch.Requests))
	var wg sync.WaitGroup
	for i, req := range batch.Requests {
		wg.Add(1)
		go func(i int, req *types.InferenceRequest) {
			defer wg.Done()
			b := w.pickBackend(req.SelectedBackend)
			_ = w.life.EnsureLoaded(ctx, first(req.SelectedModel, req.Model), req.Quantization, b.Name(), 8000)
			resp, err := b.Generate(ctx, req)
			if resp != nil {
				resp.NodeID = w.id
			}
			results[i] = batching.Result{Resp: resp, Err: err}
		}(i, req)
	}
	wg.Wait()
	return results, nil
}

func (w *worker) ready(rw http.ResponseWriter) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.chaos.crash {
		http.Error(rw, `{"error":"node_crashed"}`, http.StatusServiceUnavailable)
		return false
	}
	if w.chaos.oom {
		http.Error(rw, `{"error":"gpu_oom"}`, http.StatusInsufficientStorage)
		return false
	}
	if w.chaos.delayMS > 0 {
		time.Sleep(time.Duration(w.chaos.delayMS) * time.Millisecond)
	}
	return true
}

func (w *worker) status() types.NodeStatus {
	gpus := []types.GPUInfo{{
		Index: 0, Name: envOr("GPU_NAME", "Simulated-A100"),
		Utilization: 0.1 + rand.Float64()*0.4 + float64(atomic.LoadInt64(&w.active))*0.05,
		MemoryUsedMB: 2048 + atomic.LoadInt64(&w.active)*512,
		MemoryTotalMB: 40960, Temperature: 45 + rand.Float64()*20, PowerWatts: 120 + float64(atomic.LoadInt64(&w.active))*15,
	}}
	backends := make([]string, 0, len(w.backends))
	for k := range w.backends {
		backends = append(backends, k)
	}
	return types.NodeStatus{
		ID: w.id, Cluster: w.cluster, Address: w.addr, Healthy: !w.chaos.crash,
		LastHeartbeat: time.Now().UTC(), GPUs: gpus,
		CPUUtilization: 0.15 + float64(atomic.LoadInt64(&w.active))*0.05,
		MemoryUsedMB: 4096, MemoryTotalMB: 65536,
		ActiveRequests: int(atomic.LoadInt64(&w.active)),
		QueueDepth: int(atomic.LoadInt64(&w.queue)),
		TokensPerSec: w.tps, EstLatencyMS: 60 + float64(atomic.LoadInt64(&w.queue))*15,
		LoadedModels: w.life.Snapshot(), Backends: backends,
		CostPerMillion: 0.12, EnergyWatts: gpus[0].PowerWatts,
		Labels: map[string]string{"provider": envOr("PROVIDER", "local")},
	}
}

func (w *worker) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			st := w.status()
			_ = w.bus.Publish(events.SubjectNodes, types.ClusterEvent{
				Type: "node.heartbeat", Source: w.id,
				Payload: map[string]interface{}{"node": st},
			})
			// also HTTP register for environments without NATS
			body, _ := json.Marshal(st)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(w.schedURL, "/")+"/v1/nodes", strings.NewReader(string(body)))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}
		}
	}
}

func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
