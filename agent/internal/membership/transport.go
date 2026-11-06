package membership

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/store"
)

// Transport carries a host's events to the cluster agent.
//
// INTENT: the host POSTs its pending events to the cluster agent's endpoint and
// learns how far the cluster has durably stored them. Delivery is at-least-once;
// the receiver ignores an event it already has (identity is HostID:Seq), so a
// retry after a lost reply is harmless. Turso's own push/pull sync is deliberately
// not used: it resolves conflicts as last-push-wins and bootstraps a host's file
// from the remote, and host facts must never be merged or overwritten.
type Transport interface {
	// Send delivers events, oldest first, and returns the highest sequence number
	// the cluster has durably stored. It must not return a sequence it did not
	// store. A non-nil error means nothing is known to be stored.
	Send(ctx context.Context, events []store.Event) (ackedSeq int64, err error)
}

// Ingester is the cluster-agent side of delivery; internal/cluster.Registry is the implementation.
//
// INTENT: the cluster agent's endpoint decodes a batch from one host, stores any
// events it does not already have in its own database, and returns the highest
// contiguous sequence it now holds for that host. Only that one owner writes a
// host's events, so there is nothing to merge. When more than one cluster-agent
// replica exists, only the replica holding the Kubernetes Lease writes.
type Ingester interface {
	Ingest(ctx context.Context, hostID string, events []store.Event) (ackedSeq int64, err error)
}

// SenderOptions tunes RunSender.
type SenderOptions struct {
	// Every is how often to try. Default one minute.
	Every time.Duration
	// BatchSize bounds events per Send. Default 100.
	BatchSize int
	// OnError is told about failures; the sender itself always keeps going.
	OnError func(error)
}

// RunSender delivers a host's pending events until ctx ends.
//
// INTENT: the cluster being down, unreachable, or not yet built is NORMAL for a
// host agent, especially during bootstrap. So a failed Send is reported and
// retried later and never stops the agent or blocks any other work; the events
// simply wait in the outbox. This is what lets host agents be fully independent
// of the cluster agent.
func RunSender(ctx context.Context, db *store.Store, transport Transport, options SenderOptions) {
	if options.Every <= 0 {
		options.Every = time.Minute
	}
	if options.BatchSize <= 0 {
		options.BatchSize = 100
	}
	report := options.OnError
	if report == nil {
		report = func(error) {}
	}

	ticker := time.NewTicker(options.Every)
	defer ticker.Stop()
	for {
		if err := SendPending(ctx, db, transport, options.BatchSize); err != nil && ctx.Err() == nil {
			report(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// SendPending delivers everything pending, one batch at a time, advancing the
// delivery cursor after each acknowledged batch. It stops at the first failure so
// order is preserved: the next attempt resumes from the last acknowledged event.
func SendPending(ctx context.Context, db *store.Store, transport Transport, batchSize int) error {
	for {
		pending, err := db.Pending(ctx, batchSize)
		if err != nil {
			return fmt.Errorf("reading the outbox: %w", err)
		}
		if len(pending) == 0 {
			return nil
		}
		acked, err := transport.Send(ctx, pending)
		if err != nil {
			return fmt.Errorf("sending %d events: %w", len(pending), err)
		}
		// Trust the receiver's cursor, but never move past what was sent: a buggy or
		// hostile reply must not make the host discard events it never delivered.
		last := pending[len(pending)-1].Seq
		if acked > last {
			acked = last
		}
		if acked <= pending[0].Seq-1 {
			return errors.New("the cluster acknowledged none of the events sent")
		}
		if err := db.Ack(ctx, acked); err != nil {
			return fmt.Errorf("recording delivery progress: %w", err)
		}
		if acked < last {
			// Partial acknowledgement: stop and retry later rather than spin.
			return fmt.Errorf("the cluster stored events only up to %d of %d", acked, last)
		}
	}
}
