package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lattice-ai/lattice/pkg/cluster"
	"github.com/lattice-ai/lattice/pkg/config"
	"github.com/lattice-ai/lattice/pkg/events"
	"github.com/lattice-ai/lattice/pkg/metrics"
	schedulerplugin "github.com/lattice-ai/lattice/pkg/plugins/scheduler"
	"github.com/lattice-ai/lattice/pkg/tracing"
	"github.com/lattice-ai/lattice/pkg/types"
)

func main() {
	cfg := config.Load("scheduler")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdown, _ := tracing.Setup(ctx, "scheduler", cfg.OTLPEndpoint)
	defer shutdown(context.Background())

	bus, _ := events.Connect(cfg.NATSURL)
	defer bus.Close()

	state := cluster.NewState()
	sched := schedulerplugin.New()

	var mu sync.Mutex

	// ingest heartbeats
	go func() {
		ch, err := bus.Subscribe(ctx, events.SubjectNodes)
		if err != nil {
			slog.Warn("subscribe nodes failed", "err", err)
			return
		}
		for ev := range ch {
			if ev.Type != "node.heartbeat" {
				continue
			}
			raw, _ := json.Marshal(ev.Payload["node"])
			var n types.NodeStatus
			if err := json.Unmarshal(raw, &n); err != nil {
				continue
			}
			state.UpsertNode(n)
			for i, g := range n.GPUs {
				metrics.GPUUtilization.WithLabelValues(n.ID, string(rune('0'+i))).Set(g.Utilization)
				metrics.GPUMemory.WithLabelValues(n.ID, string(rune('0'+i))).Set(float64(g.MemoryUsedMB * 1024 * 1024))
			}
			metrics.QueueDepth.WithLabelValues(n.ID).Set(float64(n.QueueDepth))
			metrics.ActiveRequests.WithLabelValues(n.ID).Set(float64(n.ActiveRequests))
			metrics.ActiveModels.WithLabelValues(n.ID).Set(float64(len(n.LoadedModels)))
			metrics.TokensPerSec.WithLabelValues(n.ID, "aggregate").Set(n.TokensPerSec)
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/v1/schedule", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx, span := tracing.Start(r.Context(), "scheduler.select", map[string]string{"stage": tracing.StageScheduler})
		defer span.End()
		tracing.AnnotateStage(span, tracing.StageScheduler)

		var body struct {
			Request *types.InferenceRequest `json:"request"`
			Models  []string                `json:"models"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Request == nil {
			http.Error(w, "request required", http.StatusBadRequest)
			return
		}
		nodes := state.HealthyNodes()
		// bootstrap: if no workers yet, synthesize a local mock node so demos work
		if len(nodes) == 0 {
			nodes = []types.NodeStatus{demoNode(cfg.ClusterName)}
			state.UpsertNode(nodes[0])
		}
		mu.Lock()
		decision, err := sched.Select(ctx, body.Request, nodes, body.Models)
		mu.Unlock()
		if err != nil {
			metrics.FailedRequests.WithLabelValues("scheduler", "no_capacity").Inc()
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		_ = bus.Publish(events.SubjectRequests, types.ClusterEvent{
			Type: "request.scheduled", Source: "scheduler",
			Payload: map[string]interface{}{
				"id": body.Request.ID, "node": decision.NodeID, "model": decision.Model,
				"backend": decision.Backend, "score": decision.Score,
			},
		})
		metrics.RequestsTotal.WithLabelValues("scheduler", "schedule", "ok").Inc()
		writeJSON(w, decision)
	})
	mux.HandleFunc("/v1/nodes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var n types.NodeStatus
			if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			n.LastHeartbeat = time.Now().UTC()
			state.UpsertNode(n)
			writeJSON(w, n)
			return
		}
		writeJSON(w, state.Nodes())
	})
	mux.HandleFunc("/v1/cluster", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, state.Snapshot())
	})
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	mux.HandleFunc("/v1/ws/cluster", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-t.C:
				if err := conn.WriteJSON(state.Snapshot()); err != nil {
					return
				}
			}
		}
	})
	mux.HandleFunc("/v1/nodes/", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[len("/v1/nodes/"):]
		if r.Method == http.MethodDelete {
			state.MarkUnhealthy(id, "manual drain")
			state.RemoveNode(id)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	})

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux}
	go func() {
		slog.Info("scheduler listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("scheduler failed", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func demoNode(cluster string) types.NodeStatus {
	return types.NodeStatus{
		ID: "node-local-1", Cluster: cluster, Address: "http://127.0.0.1:8084",
		Healthy: true, LastHeartbeat: time.Now().UTC(),
		GPUs: []types.GPUInfo{{Index: 0, Name: "Simulated-GPU", Utilization: 0.12, MemoryUsedMB: 2048, MemoryTotalMB: 24576}},
		CPUUtilization: 0.2, MemoryUsedMB: 4096, MemoryTotalMB: 32768,
		ActiveRequests: 0, QueueDepth: 0, TokensPerSec: 90, EstLatencyMS: 80,
		Backends: []string{"mock", "vllm", "ollama"}, CostPerMillion: 0.12,
		LoadedModels: []types.LoadedModel{},
		Labels: map[string]string{"tier": "edge", "provider": "local"},
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
