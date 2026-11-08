package cluster

import (
	"context"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/identity"
)

// Kubernetes is the authority on which servers belong to the cluster, so the registry does
// not keep its own list of hosts: it follows the node list and drops what Kubernetes dropped.
// Without this a server deleted from the cluster would keep an approved key for good, and
// could keep pulling the account list and sending events.

// kubernetesMatch is what Enroll records for a host approved because Kubernetes listed it.
// Only these hosts are reconciled: a host a person approved by hand was never a node, so
// absence from the node list says nothing about it.
const kubernetesMatch = "kubernetes node match"

// Reconcile removes the hosts that were approved as Kubernetes nodes and are no longer in
// the node list, together with their stored events, and returns the host names it removed.
//
// The caller passes only a node list it actually read. An empty list is refused as a
// likely read failure: reconciling against it would drop every host at once.
func (r *Registry) Reconcile(ctx context.Context, nodes []string, gone map[string]bool) ([]string, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	listed := map[string]bool{}
	for _, node := range nodes {
		listed[identity.ShortName(node)] = true
	}
	rows, err := r.db.QueryContext(ctx, `SELECT host_id, hostname FROM hosts WHERE approved_by = ?`, kubernetesMatch)
	if err != nil {
		return nil, err
	}
	type host struct{ id, name string }
	var candidates []host
	for rows.Next() {
		var h host
		if err := rows.Scan(&h.id, &h.name); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	var removed []string
	for _, h := range candidates {
		if listed[identity.ShortName(h.name)] {
			delete(gone, h.id)
			continue
		}
		// Absent once could be a node that is being replaced or a flaky read; absent on
		// two reads in a row is gone.
		if !gone[h.id] {
			gone[h.id] = true
			continue
		}
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return removed, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE host_id = ?`, h.id); err != nil {
			tx.Rollback()
			return removed, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM hosts WHERE host_id = ?`, h.id); err != nil {
			tx.Rollback()
			return removed, err
		}
		if err := tx.Commit(); err != nil {
			return removed, err
		}
		delete(gone, h.id)
		removed = append(removed, h.name)
	}
	return removed, nil
}

// RunReconcile reconciles on a timer until ctx ends. A failed read of the node list is
// skipped quietly and tried again: the registry is only ever changed from a list that was
// read successfully, so an outage can never remove anyone.
func RunReconcile(ctx context.Context, registry *Registry, nodes NodeLister, every time.Duration, report func(removed []string, err error)) {
	if every <= 0 {
		every = 5 * time.Minute
	}
	gone := map[string]bool{}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		list, err := nodes(ctx)
		if err != nil {
			continue
		}
		removed, err := registry.Reconcile(ctx, list, gone)
		if report != nil && (err != nil || len(removed) > 0) {
			report(removed, err)
		}
	}
}
