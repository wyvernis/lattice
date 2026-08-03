package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/lattice-ai/lattice/pkg/types"
	"github.com/nats-io/nats.go"
)

const (
	SubjectCluster  = "lattice.cluster.>"
	SubjectNodes    = "lattice.cluster.nodes"
	SubjectRequests = "lattice.cluster.requests"
	SubjectModels   = "lattice.cluster.models"
	SubjectChaos    = "lattice.cluster.chaos"
	SubjectScale    = "lattice.cluster.scale"
)

// Bus is a NATS-backed event bus with in-memory fallback.
type Bus struct {
	nc     *nats.Conn
	local  map[string][]chan types.ClusterEvent
	mu     sync.RWMutex
	closed bool
}

// Connect opens NATS or falls back to local fan-out.
func Connect(url string) (*Bus, error) {
	b := &Bus{local: map[string][]chan types.ClusterEvent{}}
	nc, err := nats.Connect(url,
		nats.MaxReconnects(10),
		nats.ReconnectWait(time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				slog.Warn("nats disconnected", "err", err)
			}
		}),
	)
	if err != nil {
		slog.Warn("nats unavailable, using in-process bus", "err", err)
		return b, nil
	}
	b.nc = nc
	return b, nil
}

// Publish emits a cluster event.
func (b *Bus) Publish(subject string, ev types.ClusterEvent) error {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if b.nc != nil && b.nc.IsConnected() {
		return b.nc.Publish(subject, data)
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.local[subject] {
		select {
		case ch <- ev:
		default:
		}
	}
	// also fan to wildcard listeners
	for _, ch := range b.local["*"] {
		select {
		case ch <- ev:
		default:
		}
	}
	return nil
}

// Subscribe receives events. Cancel via context.
func (b *Bus) Subscribe(ctx context.Context, subject string) (<-chan types.ClusterEvent, error) {
	ch := make(chan types.ClusterEvent, 64)
	if b.nc != nil && b.nc.IsConnected() {
		sub, err := b.nc.Subscribe(subject, func(msg *nats.Msg) {
			var ev types.ClusterEvent
			if err := json.Unmarshal(msg.Data, &ev); err != nil {
				return
			}
			select {
			case ch <- ev:
			default:
			}
		})
		if err != nil {
			return nil, err
		}
		go func() {
			<-ctx.Done()
			_ = sub.Unsubscribe()
			close(ch)
		}()
		return ch, nil
	}
	b.mu.Lock()
	b.local[subject] = append(b.local[subject], ch)
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		defer b.mu.Unlock()
		list := b.local[subject]
		for i, c := range list {
			if c == ch {
				b.local[subject] = append(list[:i], list[i+1:]...)
				break
			}
		}
		close(ch)
	}()
	return ch, nil
}

// Close shuts down the bus.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	if b.nc != nil {
		b.nc.Close()
	}
}
