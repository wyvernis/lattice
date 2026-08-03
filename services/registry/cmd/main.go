package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/lattice-ai/lattice/pkg/cluster"
	"github.com/lattice-ai/lattice/pkg/config"
	"github.com/lattice-ai/lattice/pkg/events"
	"github.com/lattice-ai/lattice/pkg/metrics"
	"github.com/lattice-ai/lattice/pkg/types"
)

func main() {
	cfg := config.Load("registry")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bus, _ := events.Connect(cfg.NATSURL)
	defer bus.Close()

	state := cluster.NewState()
	seedModels(state)

	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, state.Models())
		case http.MethodPost:
			var m types.ModelRecord
			if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if m.ID == "" {
				m.ID = slug(m.Name)
			}
			if m.Name == "" {
				http.Error(w, "name required", http.StatusBadRequest)
				return
			}
			now := time.Now().UTC()
			m.CreatedAt = now
			m.UpdatedAt = now
			if m.DownloadStatus == "" {
				m.DownloadStatus = "registered"
			}
			if m.Checksum == "" && m.DownloadURL != "" {
				sum := sha256.Sum256([]byte(m.DownloadURL + m.Name))
				m.Checksum = hex.EncodeToString(sum[:])
			}
			state.UpsertModel(m)
			_ = bus.Publish(events.SubjectModels, types.ClusterEvent{
				Type: "model.registered", Source: "registry",
				Payload: map[string]interface{}{"id": m.ID, "name": m.Name},
			})
			writeJSON(w, m)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/v1/models/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/models/")
		parts := strings.Split(path, "/")
		id := parts[0]
		m, ok := state.GetModel(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if len(parts) == 1 {
			if r.Method == http.MethodGet {
				writeJSON(w, m)
				return
			}
			if r.Method == http.MethodPatch {
				var patch types.ModelRecord
				if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if patch.DownloadStatus != "" {
					m.DownloadStatus = patch.DownloadStatus
				}
				if patch.Version != "" {
					m.Version = patch.Version
				}
				m.UpdatedAt = time.Now().UTC()
				state.UpsertModel(m)
				writeJSON(w, m)
				return
			}
		}
		if len(parts) == 2 && parts[1] == "download" && r.Method == http.MethodPost {
			m.DownloadStatus = "downloading"
			state.UpsertModel(m)
			go func(model types.ModelRecord) {
				time.Sleep(800 * time.Millisecond)
				model.DownloadStatus = "cached"
				model.UpdatedAt = time.Now().UTC()
				state.UpsertModel(model)
				_ = bus.Publish(events.SubjectModels, types.ClusterEvent{
					Type: "model.cached", Source: "registry",
					Payload: map[string]interface{}{"id": model.ID},
				})
			}(m)
			writeJSON(w, map[string]string{"status": "downloading", "id": id})
			return
		}
		if len(parts) == 2 && parts[1] == "verify" && r.Method == http.MethodPost {
			ok := m.Checksum != ""
			status := "verified"
			if !ok {
				status = "missing_checksum"
			}
			writeJSON(w, map[string]interface{}{"id": id, "checksum": m.Checksum, "status": status})
			return
		}
		http.NotFound(w, r)
	})

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux}
	go func() {
		slog.Info("registry listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("registry failed", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func seedModels(state *cluster.State) {
	now := time.Now().UTC()
	seeds := []types.ModelRecord{
		{ID: "qwen2.5-coder-7b", Name: "qwen2.5-coder-7b", Version: "1.0", Provider: "Alibaba", Quantizations: []string{"fp16", "awq", "gguf-q4"}, Capabilities: []string{"chat", "code"}, MinVRAMMB: 10000, PreferredBackend: "vllm", DownloadStatus: "cached", CostPerMillion: 0.10, QualityScore: 0.82, Tags: []string{"coding"}, CreatedAt: now, UpdatedAt: now},
		{ID: "deepseek-r1-7b", Name: "deepseek-r1-7b", Version: "1.0", Provider: "DeepSeek", Quantizations: []string{"fp16", "awq"}, Capabilities: []string{"chat", "reasoning"}, MinVRAMMB: 12000, PreferredBackend: "vllm", DownloadStatus: "cached", CostPerMillion: 0.20, QualityScore: 0.90, Tags: []string{"reasoning"}, CreatedAt: now, UpdatedAt: now},
		{ID: "mistral-7b", Name: "mistral-7b", Version: "0.3", Provider: "Mistral", Quantizations: []string{"fp16", "gguf-q4"}, Capabilities: []string{"chat", "summarization"}, MinVRAMMB: 9000, PreferredBackend: "vllm", DownloadStatus: "cached", CostPerMillion: 0.10, QualityScore: 0.80, Tags: []string{"summarization"}, CreatedAt: now, UpdatedAt: now},
		{ID: "aya-23-8b", Name: "aya-23-8b", Version: "1.0", Provider: "Cohere", Quantizations: []string{"fp16"}, Capabilities: []string{"chat", "translation"}, MinVRAMMB: 11000, PreferredBackend: "vllm", DownloadStatus: "registered", CostPerMillion: 0.11, QualityScore: 0.78, Tags: []string{"translation"}, CreatedAt: now, UpdatedAt: now},
		{ID: "qwen2-vl-7b", Name: "qwen2-vl-7b", Version: "1.0", Provider: "Alibaba", Quantizations: []string{"fp16"}, Capabilities: []string{"chat", "vision"}, MinVRAMMB: 14000, PreferredBackend: "vllm", DownloadStatus: "registered", CostPerMillion: 0.18, QualityScore: 0.85, Tags: []string{"vision"}, CreatedAt: now, UpdatedAt: now},
		{ID: "llama3.1-8b", Name: "llama3.1-8b", Version: "1.0", Provider: "Meta", Quantizations: []string{"fp16", "awq", "gguf-q4"}, Capabilities: []string{"chat"}, MinVRAMMB: 10000, PreferredBackend: "vllm", DownloadStatus: "cached", CostPerMillion: 0.12, QualityScore: 0.85, Tags: []string{"chat"}, CreatedAt: now, UpdatedAt: now},
		{ID: "phi-3-mini", Name: "phi-3-mini", Version: "1.0", Provider: "Microsoft", Quantizations: []string{"fp16", "gguf-q4"}, Capabilities: []string{"chat"}, MinVRAMMB: 4000, PreferredBackend: "llamacpp", DownloadStatus: "cached", CostPerMillion: 0.04, QualityScore: 0.65, Tags: []string{"cheap"}, CreatedAt: now, UpdatedAt: now},
		{ID: "nomic-embed-text", Name: "nomic-embed-text", Version: "1.5", Provider: "Nomic", Quantizations: []string{"fp16"}, Capabilities: []string{"embedding"}, MinVRAMMB: 1024, PreferredBackend: "ollama", DownloadStatus: "cached", CostPerMillion: 0.02, QualityScore: 0.90, Tags: []string{"embedding"}, CreatedAt: now, UpdatedAt: now},
	}
	for _, m := range seeds {
		if m.Checksum == "" {
			sum := sha256.Sum256([]byte(m.Name + m.Version))
			m.Checksum = hex.EncodeToString(sum[:8])
		}
		state.UpsertModel(m)
	}
	_ = uuid.New()
}

func slug(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	return s
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
