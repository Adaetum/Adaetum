package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/automation"
)

// A restarted pod has a new name and IP; its certificate must not, or every host that pinned
// the fingerprint loses the cluster agent.
func TestPodCertificateIdentityIsTheServiceNotThePod(t *testing.T) {
	env := map[string]string{"ADAETUM_CLUSTER_AGENT_SERVICE_HOST": "10.43.51.144", "HOSTNAME": "adaetum-cluster-agent-abc", "POD_IP": "10.42.0.9"}
	identity, ok := podCertificateIdentity(func(k string) string { return env[k] })
	if !ok {
		t.Fatal("a pod with the Service address must get a fixed identity")
	}
	got := identity()
	found := false
	for _, ip := range got.IPs {
		found = found || ip.String() == "10.43.51.144"
		if ip.String() == "10.42.0.9" {
			t.Fatal("the pod IP must not be in the certificate")
		}
	}
	if !found {
		t.Fatalf("the Service address must be covered: %v", got.IPs)
	}
	for _, name := range got.Names {
		if name == "adaetum-cluster-agent-abc" {
			t.Fatal("the pod name must not be in the certificate")
		}
	}
	if _, ok := podCertificateIdentity(func(string) string { return "" }); ok {
		t.Fatal("outside a pod the machine's own identity stays")
	}
}

// The Automation page must work on the cluster agent: the runner's Deployment comes from
// Kubernetes, and what lives inside the runner pod reports why it cannot be read instead of
// failing the whole page.
func TestClusterAutomationReportsWhatItCannotRead(t *testing.T) {
	deployments := `{"items":[{"metadata":{"name":"ansible-runner"},"spec":{"replicas":1,"template":{"spec":{"containers":[{"name":"ansible-runner","image":"r/ansible-runner:1"}]}}},"status":{"readyReplicas":1}}]}`
	pods := `{"items":[{"metadata":{"name":"ansible-runner-abc"},"spec":{"nodeName":"n"},"status":{"phase":"Running"}}]}`
	source := clusterAutomationSource{get: func(_ context.Context, resources string, _ ...string) ([]byte, error) {
		if resources == "pods" {
			return []byte(pods), nil
		}
		return []byte(deployments), nil
	}}
	got := (&automation.Collector{Source: source, Now: time.Now}).Collect(context.Background())
	if !got.Runner.Found || got.Runner.Pod == nil || got.Runner.Pod.Phase != "Running" {
		t.Fatalf("the runner should come from Kubernetes: %+v", got.Runner)
	}
	if len(got.Errors) == 0 || !strings.Contains(got.Errors[0], "cannot read inside the runner pod") {
		t.Fatalf("what is inside the pod should say why it is missing: %v", got.Errors)
	}
}
