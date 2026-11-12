// Package runstore is the run engine's journal on the agent's own database.
//
// A run record is written as an ordinary event in the host's append-only outbox
// (internal/store), under the kind prefix "runs.". That gives run history three
// things for free: it survives restarts (so runs can resume), it is ordered and
// owned by exactly one host, and it travels to the cluster agent with the host's
// other events, which is also the path analytics will use.
//
// It is a separate package so the engine (internal/runs) stays free of any
// database and can be tested against an in-memory journal.
package runstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Adaetum/Adaetum/agent/internal/runs"
	"github.com/Adaetum/Adaetum/agent/internal/store"
)

// KindPrefix namespaces run records among the host's other events.
const KindPrefix = "runs."

// maxRecordBytes keeps a record under the cluster's per-event limit (16 KiB). An
// oversized event is rejected by the cluster, and a rejected event blocks every
// event queued behind it, so the journal refuses to write one rather than jam the
// outbox. The engine's own limits keep real records far below this.
const maxRecordBytes = 12 * 1024

// Journal stores run records as host events.
type Journal struct {
	Store *store.Store
}

// Append records rec durably, or fails.
func (j Journal) Append(ctx context.Context, rec runs.Record) error {
	if encoded, err := json.Marshal(rec); err != nil {
		return fmt.Errorf("encoding a run record: %w", err)
	} else if len(encoded) > maxRecordBytes {
		return fmt.Errorf("a %s record is %d bytes; the limit is %d", rec.Kind, len(encoded), maxRecordBytes)
	}
	_, err := j.Store.Append(ctx, KindPrefix+rec.Kind, rec)
	return err
}

// Load returns every run record, oldest first.
func (j Journal) Load(ctx context.Context) ([]runs.Record, error) {
	events, err := j.Store.EventsWithPrefix(ctx, KindPrefix)
	if err != nil {
		return nil, err
	}
	records := make([]runs.Record, 0, len(events))
	for _, event := range events {
		var rec runs.Record
		if err := json.Unmarshal(event.Payload, &rec); err != nil {
			// A record this agent cannot read is skipped, not fatal: an older agent
			// must still start after a newer one wrote a record it does not understand.
			continue
		}
		records = append(records, rec)
	}
	return records, nil
}
