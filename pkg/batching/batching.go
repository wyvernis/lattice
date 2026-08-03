package batching

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lattice-ai/lattice/pkg/types"
)

// Strategy controls how requests are grouped.
type Strategy string

const (
	StrategyWindow   Strategy = "window"    // time window
	StrategySize     Strategy = "size"      // max batch size
	StrategyAdaptive Strategy = "adaptive"  // latency-aware
)

// Config tunes the batcher.
type Config struct {
	Strategy      Strategy
	MaxBatchSize  int
	MaxWait       time.Duration
	TargetLatency time.Duration
}

// Request wraps an inference job with completion channel.
type Request struct {
	Req  *types.InferenceRequest
	Done chan Result
}

// Result is batch execution outcome for one item.
type Result struct {
	Resp *types.InferenceResponse
	Err  error
}

// Batcher groups compatible requests by model.
type Batcher struct {
	cfg    Config
	mu     sync.Mutex
	queues map[string][]*Request
	flush  chan string
}

// New creates a batcher.
func New(cfg Config) *Batcher {
	if cfg.MaxBatchSize <= 0 {
		cfg.MaxBatchSize = 8
	}
	if cfg.MaxWait <= 0 {
		cfg.MaxWait = 25 * time.Millisecond
	}
	if cfg.Strategy == "" {
		cfg.Strategy = StrategyAdaptive
	}
	return &Batcher{
		cfg:    cfg,
		queues: map[string][]*Request{},
		flush:  make(chan string, 128),
	}
}

// Submit enqueues a request and waits for its result.
func (b *Batcher) Submit(ctx context.Context, req *types.InferenceRequest) (*types.InferenceResponse, error) {
	model := req.SelectedModel
	if model == "" {
		model = req.Model
	}
	r := &Request{Req: req, Done: make(chan Result, 1)}
	b.mu.Lock()
	b.queues[model] = append(b.queues[model], r)
	n := len(b.queues[model])
	b.mu.Unlock()

	if n >= b.cfg.MaxBatchSize {
		select {
		case b.flush <- model:
		default:
		}
	} else {
		time.AfterFunc(b.waitFor(n), func() {
			select {
			case b.flush <- model:
			default:
			}
		})
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-r.Done:
		return res.Resp, res.Err
	}
}

func (b *Batcher) waitFor(n int) time.Duration {
	switch b.cfg.Strategy {
	case StrategySize:
		return b.cfg.MaxWait
	case StrategyAdaptive:
		// shrink wait as queue grows
		factor := 1.0 - float64(n)/float64(b.cfg.MaxBatchSize)
		if factor < 0.1 {
			factor = 0.1
		}
		return time.Duration(float64(b.cfg.MaxWait) * factor)
	default:
		return b.cfg.MaxWait
	}
}

// Run drains flush signals and invokes handler with formed batches.
func (b *Batcher) Run(ctx context.Context, handler func(context.Context, *types.BatchRequest) ([]Result, error)) {
	for {
		select {
		case <-ctx.Done():
			return
		case model := <-b.flush:
			batch := b.drain(model)
			if batch == nil || len(batch.Requests) == 0 {
				continue
			}
			results, err := handler(ctx, batch)
			if err != nil {
				for _, r := range pendingOf(batch, b) {
					r.Done <- Result{Err: err}
				}
				continue
			}
			reqs := pendingOf(batch, b)
			for i, r := range reqs {
				if i < len(results) {
					r.Done <- results[i]
				} else {
					r.Done <- Result{Err: err}
				}
			}
		}
	}
}

func pendingOf(batch *types.BatchRequest, b *Batcher) []*Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	// recover from side map keyed by batch id — simplified: store on batch via metadata
	key := "_pending:" + batch.ID
	if v, ok := b.queues[key]; ok {
		delete(b.queues, key)
		return v
	}
	return nil
}

func (b *Batcher) drain(model string) *types.BatchRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	q := b.queues[model]
	if len(q) == 0 {
		return nil
	}
	n := len(q)
	if n > b.cfg.MaxBatchSize {
		n = b.cfg.MaxBatchSize
	}
	take := q[:n]
	b.queues[model] = q[n:]
	reqs := make([]*types.InferenceRequest, len(take))
	for i, r := range take {
		reqs[i] = r.Req
	}
	id := uuid.NewString()
	b.queues["_pending:"+id] = take
	return &types.BatchRequest{
		ID:        id,
		Requests:  reqs,
		Model:     model,
		CreatedAt: time.Now().UTC(),
	}
}

// Efficiency returns average fill ratio estimate.
func (b *Batcher) Efficiency() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	total, count := 0, 0
	for k, q := range b.queues {
		if len(k) > 9 && k[:9] == "_pending:" {
			continue
		}
		total += len(q)
		count++
	}
	if count == 0 {
		return 0
	}
	avg := float64(total) / float64(count)
	return avg / float64(b.cfg.MaxBatchSize)
}
