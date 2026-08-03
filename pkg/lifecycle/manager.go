package lifecycle

import (
	"context"
	"container/list"
	"log/slog"
	"sync"
	"time"

	"github.com/lattice-ai/lattice/pkg/types"
)

// Manager handles download/verify/cache/quantize/load/unload/warm/evict with LRU + predictive preload.
type Manager struct {
	mu          sync.Mutex
	loaded      map[string]*entry
	lru         *list.List
	maxModels   int
	loader      ModelLoader
	history     []demandSample
	predictEvery time.Duration
}

type entry struct {
	model        string
	quantization string
	backend      string
	vramMB       int64
	element      *list.Element
	loadedAt     time.Time
	lastUsed     time.Time
	warm         bool
}

type demandSample struct {
	at       time.Time
	category string
	model    string
}

// ModelLoader is implemented by backends / worker.
type ModelLoader interface {
	Load(ctx context.Context, model, quantization string) error
	Unload(ctx context.Context, model string) error
}

// NewManager creates a lifecycle manager.
func NewManager(loader ModelLoader, maxModels int) *Manager {
	if maxModels <= 0 {
		maxModels = 4
	}
	return &Manager{
		loaded:       map[string]*entry{},
		lru:          list.New(),
		maxModels:    maxModels,
		loader:       loader,
		predictEvery: time.Minute,
	}
}

// EnsureLoaded loads a model if missing, evicting LRU as needed.
func (m *Manager) EnsureLoaded(ctx context.Context, model, quantization, backend string, vramMB int64) error {
	m.mu.Lock()
	if e, ok := m.loaded[model]; ok {
		e.lastUsed = time.Now()
		e.warm = true
		m.lru.MoveToFront(e.element)
		m.mu.Unlock()
		return nil
	}
	for len(m.loaded) >= m.maxModels {
		back := m.lru.Back()
		if back == nil {
			break
		}
		victim := back.Value.(string)
		m.mu.Unlock()
		_ = m.Unload(ctx, victim)
		m.mu.Lock()
	}
	m.mu.Unlock()

	if err := m.loader.Load(ctx, model, quantization); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	el := m.lru.PushFront(model)
	m.loaded[model] = &entry{
		model: model, quantization: quantization, backend: backend,
		vramMB: vramMB, element: el, loadedAt: time.Now(), lastUsed: time.Now(), warm: true,
	}
	slog.Info("model loaded", "model", model, "quant", quantization, "backend", backend)
	return nil
}

// Unload removes a model from memory.
func (m *Manager) Unload(ctx context.Context, model string) error {
	m.mu.Lock()
	e, ok := m.loaded[model]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	m.lru.Remove(e.element)
	delete(m.loaded, model)
	m.mu.Unlock()
	if err := m.loader.Unload(ctx, model); err != nil {
		return err
	}
	slog.Info("model unloaded", "model", model)
	return nil
}

// Touch records usage for LRU + demand history.
func (m *Manager) Touch(model, category string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.loaded[model]; ok {
		e.lastUsed = time.Now()
		m.lru.MoveToFront(e.element)
	}
	m.history = append(m.history, demandSample{at: time.Now(), category: category, model: model})
	if len(m.history) > 10_000 {
		m.history = m.history[len(m.history)-10_000:]
	}
}

// Snapshot returns currently loaded models.
func (m *Manager) Snapshot() []types.LoadedModel {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]types.LoadedModel, 0, len(m.loaded))
	for _, e := range m.loaded {
		out = append(out, types.LoadedModel{
			Name: e.model, Quantization: e.quantization, Backend: e.backend,
			VRAMMB: e.vramMB, LoadedAt: e.loadedAt, LastUsedAt: e.lastUsed, Warm: e.warm,
		})
	}
	return out
}

// PredictDemand returns top models likely needed in the next window based on same-hour historical pattern.
func (m *Manager) PredictDemand(window time.Duration) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	hour := time.Now().Hour()
	counts := map[string]int{}
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, s := range m.history {
		if s.at.Before(cutoff) {
			continue
		}
		if s.at.Hour() == hour {
			counts[s.model]++
		}
	}
	type kv struct {
		k string
		v int
	}
	var sorted []kv
	for k, v := range counts {
		sorted = append(sorted, kv{k, v})
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].v > sorted[i].v {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	out := make([]string, 0, 3)
	for i := 0; i < len(sorted) && i < 3; i++ {
		out = append(out, sorted[i].k)
	}
	return out
}

// RunPredictiveLoop periodically preloads predicted models and evicts cold ones.
func (m *Manager) RunPredictiveLoop(ctx context.Context, defaultQuant string) {
	t := time.NewTicker(m.predictEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			preds := m.PredictDemand(time.Hour)
			for _, model := range preds {
				if err := m.EnsureLoaded(ctx, model, defaultQuant, "auto", 0); err != nil {
					slog.Warn("predictive load failed", "model", model, "err", err)
				}
			}
			m.evictCold(ctx, 30*time.Minute)
		}
	}
}

func (m *Manager) evictCold(ctx context.Context, maxIdle time.Duration) {
	m.mu.Lock()
	var cold []string
	now := time.Now()
	for name, e := range m.loaded {
		if now.Sub(e.lastUsed) > maxIdle && m.lru.Len() > 1 {
			cold = append(cold, name)
		}
	}
	m.mu.Unlock()
	for _, name := range cold {
		_ = m.Unload(ctx, name)
	}
}
