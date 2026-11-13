package inspect

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The cluster agent reads Kubernetes through Get and Raw and must never touch the machine:
// Run, ReadFile and Hostname are nil, so any use would panic.
func TestCollectClusterReadsOnlyKubernetes(t *testing.T) {
	get := func(_ context.Context, resources string, _ ...string) ([]byte, error) {
		switch resources {
		case "nodes":
			return []byte(`{"items":[{"metadata":{"name":"node-1"},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`), nil
		case "pods":
			return nil, errors.New("forbidden")
		}
		return []byte(`{"items":[]}`), nil
	}
	raw := func(context.Context, string) ([]byte, error) { return []byte(`{"items":[]}`), nil }
	report := NewClusterInspector(get, raw).CollectCluster(context.Background())

	if len(report.RKE2.Nodes) != 1 || report.RKE2.Nodes[0].Name != "node-1" {
		t.Fatalf("nodes come from Kubernetes: %+v", report.RKE2.Nodes)
	}
	if report.Host.Hostname != "" || len(report.RKE2.Services) != 0 {
		t.Fatalf("a pod reports nothing about a machine: %+v %+v", report.Host, report.RKE2.Services)
	}
	if report.SchemaVersion != SchemaVersion || report.CollectedAt.IsZero() || time.Since(report.CollectedAt) > time.Minute {
		t.Fatalf("report header: %+v", report)
	}
	found := false
	for _, e := range report.Errors {
		found = found || strings.HasPrefix(e, "pods: ")
	}
	if !found {
		t.Fatalf("one failed query is recorded, not fatal: %v", report.Errors)
	}
}
