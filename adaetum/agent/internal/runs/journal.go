package runs

import (
	"context"
	"sync"
	"time"
)

// Journal is where records go. It is append-only and is the engine's only memory:
// a run's state is whatever Fold says about the records. The agent's database
// provides the real one (so records join the same outbox that carries check-ins to
// the cluster agent); MemoryJournal serves tests.
//
// Append must not return until the record is durable. The engine does not start
// work it could not record.
type Journal interface {
	Append(ctx context.Context, rec Record) error
	// Load returns every record, oldest first. It is read once at start-up to
	// resume unfinished runs.
	Load(ctx context.Context) ([]Record, error)
}

// MemoryJournal is a Journal held in memory.
type MemoryJournal struct {
	mu      sync.Mutex
	records []Record
	// FailAppend, when set, is consulted for every Append; a non-nil result is
	// returned instead of recording. Tests use it to prove the engine fails closed.
	FailAppend func(Record) error
}

func (j *MemoryJournal) Append(_ context.Context, rec Record) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.FailAppend != nil {
		if err := j.FailAppend(rec); err != nil {
			return err
		}
	}
	j.records = append(j.records, rec)
	return nil
}

func (j *MemoryJournal) Load(context.Context) ([]Record, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]Record(nil), j.records...), nil
}

// Clock lets time be controlled in tests. Waiting is most of what the engine
// does, so a test that waited in real time would be useless.
type Clock interface {
	Now() time.Time
	// Sleep waits for d or until ctx ends, returning ctx's error in that case.
	Sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Locks serialises steps that change the same resource, across runs. Acquiring is
// all-or-nothing in a fixed order so two steps wanting the same pair of locks can
// never deadlock each other.
type Locks struct {
	mu    sync.Mutex
	locks map[string]chan struct{}
}

func newLocks() *Locks { return &Locks{locks: map[string]chan struct{}{}} }

func (l *Locks) get(key string) chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	ch, ok := l.locks[key]
	if !ok {
		ch = make(chan struct{}, 1)
		l.locks[key] = ch
	}
	return ch
}

// Acquire takes every lock named, waiting for each, and returns a release func. It
// gives up if ctx ends, releasing anything already taken.
func (l *Locks) Acquire(ctx context.Context, keys []string) (release func(), err error) {
	sorted := uniqueSorted(keys)
	held := make([]chan struct{}, 0, len(sorted))
	releaseAll := func() {
		for i := len(held) - 1; i >= 0; i-- {
			<-held[i]
		}
	}
	for _, key := range sorted {
		ch := l.get(key)
		select {
		case ch <- struct{}{}:
			held = append(held, ch)
		case <-ctx.Done():
			releaseAll()
			return nil, ctx.Err()
		}
	}
	return releaseAll, nil
}

func uniqueSorted(keys []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range keys {
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	// insertion sort: lock lists are tiny
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
