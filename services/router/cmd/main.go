package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/lattice-ai/lattice/pkg/config"
	"github.com/lattice-ai/lattice/pkg/cost"
	"github.com/lattice-ai/lattice/pkg/events"
	"github.com/lattice-ai/lattice/pkg/metrics"
	routing "github.com/lattice-ai/lattice/pkg/plugins/routing"
	"github.com/lattice-ai/lattice/pkg/tracing"
	"github.com/lattice-ai/lattice/pkg/types"
)

func main() {
	cfg := config.Load("router")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdown, _ := tracing.Setup(ctx, "router", cfg.OTLPEndpoint)
	defer shutdown(context.Background())

	bus, _ := events.Connect(cfg.NATSURL)
	defer bus.Close()

	router := routing.NewHeuristicRouter(routing.DefaultPolicy())
	engine := cost.New(0.75)

	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/v1/classify", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx, span := tracing.Start(r.Context(), "router.classify", map[string]string{"stage": tracing.StageRouter})
		defer span.End()
		tracing.AnnotateStage(span, tracing.StageRouter)

		var req types.InferenceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.ID == "" {
			req.ID = uuid.NewString()
		}
		class, err := router.Classify(ctx, &req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			metrics.FailedRequests.WithLabelValues("router", "classify").Inc()
			return
		}
		policy := req.Policy
		if policy == "" {
			policy = router.Policy().DefaultPolicy
		}
		candidates, err := router.CandidateModels(ctx, class, policy)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		candidates = engine.Optimize(ctx, candidates, policy, 0)
		req.Classification = class
		if len(candidates) > 0 {
			req.SelectedModel = candidates[0]
		}
		_ = bus.Publish(events.SubjectRequests, types.ClusterEvent{
			Type: "request.classified", Source: "router",
			Payload: map[string]interface{}{"id": req.ID, "category": class.Category, "model": req.SelectedModel},
		})
		metrics.RequestsTotal.WithLabelValues("router", "classify", "ok").Inc()
		writeJSON(w, map[string]interface{}{
			"id":            req.ID,
			"classification": class,
			"candidates":    candidates,
			"selected_model": req.SelectedModel,
			"policy":        policy,
		})
	})
	mux.HandleFunc("/v1/policy", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, router.Policy())
		case http.MethodPut:
			var p routing.PolicyConfig
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			router.UpdatePolicy(p)
			writeJSON(w, p)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux}
	go func() {
		slog.Info("router listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("router failed", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
