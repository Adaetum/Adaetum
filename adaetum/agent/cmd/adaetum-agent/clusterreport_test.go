package main

import "testing"

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
