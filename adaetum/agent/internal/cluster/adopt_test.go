package cluster

import (
	"errors"
	"testing"
)

// A node that publishes its key on its own Node object is adopted with no token: Kubernetes
// is the one vouching for it.

func TestAHostWhoseNodeCarriesItsKeyIsApprovedWithoutAToken(t *testing.T) {
	h := newHarness(t)
	a := h.newAgent("node-50449")
	h.nodeKeys = map[string]string{"node-50449": a.key.PublicKey()}
	status, body := a.enroll("")
	if status != 200 || body["status"] != "approved" {
		t.Fatalf("a vouched host should be approved: %d %v", status, body)
	}
	// And it may speak at once.
	if status, body := a.send(nil); status != 200 {
		t.Fatalf("an approved host could not send: %d %v", status, body)
	}
}

func TestNamingANodeIsNotEnoughWithoutItsKey(t *testing.T) {
	h := newHarness(t)
	// The Node carries someone else's key; the claimant knows only the name.
	stranger, owner := h.newAgent("node-50449"), h.newAgent("node-50449")
	h.nodeKeys = map[string]string{"node-50449": owner.key.PublicKey()}
	if status, body := stranger.enroll(""); status != 403 {
		t.Fatalf("a host that only knows a node's name was let in: %d %v", status, body)
	}
}

func TestAKeyOnAnotherNodeDoesNotVouchForThisOne(t *testing.T) {
	h := newHarness(t)
	a := h.newAgent("node-56843")
	// The key is real, but it is published by node-50449, not by the node a names.
	h.nodeKeys = map[string]string{"node-50449": a.key.PublicKey()}
	if status, _ := a.enroll(""); status != 403 {
		t.Fatalf("a key published by a different node vouched for this host: %d", status)
	}
}

func TestAHostIsNotVouchedForWhenKubernetesCannotBeRead(t *testing.T) {
	h := newHarness(t)
	a := h.newAgent("node-50449")
	h.nodeKeys = map[string]string{"node-50449": a.key.PublicKey()}
	h.keysErr = errors.New("kubernetes unreachable")
	if status, _ := a.enroll(""); status != 403 {
		t.Fatalf("an unreadable node list still vouched for the host: %d", status)
	}
	// A person-made token still works as it always did: it needs no vouching.
	if status, body := a.enroll(h.mint()); status != 200 || body["status"] != "approved" {
		t.Fatalf("a valid token on a listed node should still be approved: %d %v", status, body)
	}
}

func TestAReinstalledNodeKeepsItsPlaceWhenKubernetesVouchesForTheNewKey(t *testing.T) {
	h := newHarness(t)
	old := h.newAgent("node-50449")
	h.nodeKeys = map[string]string{"node-50449": old.key.PublicKey()}
	if status, _ := old.enroll(""); status != 200 {
		t.Fatal("first enrolment failed")
	}
	// The machine is reinstalled: same host ID and name, a new key, republished on the Node.
	renewed := h.newAgent("node-50449")
	renewed.id = old.id
	h.nodeKeys = map[string]string{"node-50449": renewed.key.PublicKey()}
	if status, body := renewed.enroll(""); status != 200 || body["status"] != "approved" {
		t.Fatalf("the reinstalled node was not re-adopted: %d %v", status, body)
	}
	if status, _ := old.send(nil); status == 200 {
		t.Fatal("the old key still works after Kubernetes vouched for a new one")
	}
	if status, _ := renewed.send(nil); status != 200 {
		t.Fatalf("the new key cannot send: %d", status)
	}
}

func TestVouchingNeverReplacesTheKeyOfAHostAPersonApproved(t *testing.T) {
	h := newHarness(t)
	// A host outside Kubernetes, approved by hand.
	outsider := h.newAgent("laptop-1")
	if status, _ := outsider.enroll(h.mint()); status != 200 {
		t.Fatal("enrolment failed")
	}
	if status, _ := h.post("/api/v1/hosts/"+outsider.id+"/approve", nil, map[string]string{"Authorization": "Bearer " + userToken}); status != 200 {
		t.Fatal("approval failed")
	}
	thief := h.newAgent("laptop-1")
	thief.id = outsider.id
	h.nodes = append(h.nodes, "laptop-1")
	h.nodeKeys = map[string]string{"laptop-1": thief.key.PublicKey()}
	if status, _ := thief.enroll(""); status != 403 {
		t.Fatalf("a vouched key replaced a host a person approved: %d", status)
	}
}
