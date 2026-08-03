package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/lattice-ai/lattice/pkg/config"
	"github.com/lattice-ai/lattice/pkg/events"
	"github.com/lattice-ai/lattice/pkg/metrics"
	"github.com/lattice-ai/lattice/pkg/types"
)

type experiment struct {
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Metrics   map[string]interface{} `json:"metrics,omitempty"`
}

func main() {
	cfg := config.Load("chaos")
	cfg.HTTPAddr = envOr("HTTP_ADDR", ":8085")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bus, _ := events.Connect(cfg.NATSURL)
	defer bus.Close()

	var mu sync.Mutex
	history := []experiment{}

	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/v1/chaos/experiments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			mu.Lock()
			defer mu.Unlock()
			writeJSON(w, history)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		var body struct {
			Action   string `json:"action"` // crash|oom|delay|spike|recover
			Target   string `json:"target"` // worker URL
			DelayMS  int    `json:"delay_ms"`
			Duration string `json:"duration"`
			RPS      int    `json:"rps"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if body.Target == "" {
			body.Target = envOr("WORKER_URL", "http://localhost:8084")
		}
		exp := experiment{
			ID: uuid.NewString(), Action: body.Action, Target: body.Target,
			Status: "running", StartedAt: time.Now().UTC(),
			Metrics: map[string]interface{}{},
		}
		mu.Lock()
		history = append(history, exp)
		mu.Unlock()

		start := time.Now()
		switch body.Action {
		case "crash", "oom", "delay", "recover":
			payload, _ := json.Marshal(map[string]interface{}{"action": body.Action, "delay_ms": body.DelayMS})
			resp, err := http.Post(body.Target+"/v1/chaos", "application/json", bytes.NewReader(payload))
			if err != nil {
				exp.Status = "failed"
				exp.Metrics["error"] = err.Error()
			} else {
				resp.Body.Close()
				exp.Status = "injected"
			}
		case "spike":
			rps := body.RPS
			if rps <= 0 {
				rps = 50
			}
			dur := 5 * time.Second
			if body.Duration != "" {
				if d, err := time.ParseDuration(body.Duration); err == nil {
					dur = d
				}
			}
			go trafficSpike(ctx, envOr("GATEWAY_URL", "http://localhost:8080"), rps, dur)
			exp.Status = "spike_running"
			exp.Metrics["rps"] = rps
			exp.Metrics["duration"] = dur.String()
		default:
			exp.Status = "unknown_action"
		}
		exp.EndedAt = time.Now().UTC()
		exp.Metrics["injection_latency_ms"] = time.Since(start).Milliseconds()

		_ = bus.Publish(events.SubjectChaos, types.ClusterEvent{
			Type: "chaos." + body.Action, Source: "chaos",
			Payload: map[string]interface{}{"id": exp.ID, "target": body.Target, "action": body.Action},
		})

		mu.Lock()
		for i := range history {
			if history[i].ID == exp.ID {
				history[i] = exp
			}
		}
		mu.Unlock()
		writeJSON(w, exp)
	})
	mux.HandleFunc("/v1/chaos/report", func(w http.ResponseWriter, r *http.Request) {
		// pull scheduler snapshot for availability
		resp, err := http.Get(cfg.SchedulerURL + "/v1/cluster")
		avail := 1.0
		active, total := 0, 0
		if err == nil {
			defer resp.Body.Close()
			var snap struct {
				ActiveNodes int `json:"active_nodes"`
				TotalNodes  int `json:"total_nodes"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&snap)
			active, total = snap.ActiveNodes, snap.TotalNodes
			if total > 0 {
				avail = float64(active) / float64(total)
			}
		}
		mu.Lock()
		n := len(history)
		mu.Unlock()
		writeJSON(w, types.HealthReport{
			Availability: avail, ActiveNodes: active, TotalNodes: total,
			RecoveryTimeMS: 0, DroppedReqs: 0, FailedReqs: int64(n),
		})
	})

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux}
	go func() {
		slog.Info("chaos listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("chaos failed", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	_ = srv.Shutdown(context.Background())
}

func trafficSpike(ctx context.Context, gateway string, rps int, dur time.Duration) {
	deadline := time.Now().Add(dur)
	interval := time.Second / time.Duration(rps)
	client := &http.Client{Timeout: 10 * time.Second}
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		default:
		}
		body := `{"messages":[{"role":"user","content":"chaos spike ping"}],"max_tokens":16,"policy":"latency_first"}`
		req, _ := http.NewRequest(http.MethodPost, gateway+"/v1/chat/completions", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", os.Getenv("API_KEYS"))
		if req.Header.Get("X-API-Key") == "" {
			req.Header.Set("X-API-Key", "lattice-dev-key")
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		time.Sleep(interval)
	}
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
