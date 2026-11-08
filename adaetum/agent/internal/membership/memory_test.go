package membership

import (
	"context"
	"sync"

	"github.com/Adaetum/Adaetum/agent/internal/store"
)

// memoryIngester is a minimal in-memory Ingester used by tests to stand in for the
// cluster agent. It is the reference for the behaviour a real one must have:
// idempotent, ordered, and acknowledging only what it actually holds.
type memoryIngester struct {
	mu   sync.Mutex
	seen map[string]map[int64]store.Event
	acks map[string]int64
}

func newMemoryIngester() *memoryIngester {
	return &memoryIngester{seen: map[string]map[int64]store.Event{}, acks: map[string]int64{}}
}

func (m *memoryIngester) Ingest(_ context.Context, hostID string, events []store.Event) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seen[hostID] == nil {
		m.seen[hostID] = map[int64]store.Event{}
	}
	for _, event := range events {
		m.seen[hostID][event.Seq] = event // a repeat overwrites with identical content
	}
	for {
		if _, held := m.seen[hostID][m.acks[hostID]+1]; !held {
			break
		}
		m.acks[hostID]++
	}
	return m.acks[hostID], nil
}
