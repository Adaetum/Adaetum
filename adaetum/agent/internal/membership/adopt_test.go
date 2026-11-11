package membership

import (
	"context"
	"errors"
	"testing"
)

type fakeKube struct {
	data      map[string]string
	nodes     []string
	annotated []string // "node key=value"
	getErr    error
}

func (f *fakeKube) ConfigMapData(_ context.Context, namespace, name string) (map[string]string, error) {
	if namespace != EndpointNamespace || name != EndpointConfigMap {
		return nil, errors.New("unexpected ConfigMap " + namespace + "/" + name)
	}
	return f.data, f.getErr
}
func (f *fakeKube) NodeNames(context.Context) ([]string, error) { return f.nodes, nil }
func (f *fakeKube) AnnotateNode(_ context.Context, node, key, value string) error {
	f.annotated = append(f.annotated, node+" "+key+"="+value)
	return nil
}

var goodEndpoint = map[string]string{"url": "https://10.43.1.2:8444", "fingerprint": "AA:BB:CC"}

func TestAdoptionPublishesTheKeyOnTheHostsOwnNodeThenEnrolsWithoutAToken(t *testing.T) {
	f := &fakeKube{data: goodEndpoint, nodes: []string{"node-1", "node-12484"}}
	var got EnrollRequest
	var gotLink Link
	a := Adopter{Kube: f, Enroll: func(_ context.Context, l Link, r EnrollRequest) (EnrollResult, error) {
		got, gotLink = r, l
		return EnrollResult{Status: "approved"}, nil
	}}
	link, result, err := a.Adopt(context.Background(), "hostid", "Node-12484.lan", "UFVCS0VZ")
	if err != nil || result.Status != "approved" {
		t.Fatalf("adopt: %v %+v", err, result)
	}
	if got.JoinToken != "" {
		t.Fatalf("adoption must not use a token: %+v", got)
	}
	if len(f.annotated) != 1 || f.annotated[0] != "node-12484 "+HostKeyAnnotation+"=UFVCS0VZ" {
		t.Fatalf("the key was not published on this host's own node: %v", f.annotated)
	}
	if link.URL != "https://10.43.1.2:8444" || link.Fingerprint == "" || link != gotLink {
		t.Fatalf("link = %+v (enrolled with %+v)", link, gotLink)
	}
}

func TestAdoptionDoesNothingToAMachineKubernetesDoesNotList(t *testing.T) {
	f := &fakeKube{data: goodEndpoint, nodes: []string{"node-1"}}
	a := Adopter{Kube: f, Enroll: func(context.Context, Link, EnrollRequest) (EnrollResult, error) {
		t.Fatal("must not enrol a machine that is not a node")
		return EnrollResult{}, nil
	}}
	if _, _, err := a.Adopt(context.Background(), "id", "stranger", "UFVCS0VZ"); !errors.Is(err, ErrNotANode) {
		t.Fatalf("err = %v", err)
	}
	if len(f.annotated) != 0 {
		t.Fatalf("annotated a node it does not own: %v", f.annotated)
	}
}

func TestAdoptionWaitsWhenTheClusterAgentHasNotPublishedYet(t *testing.T) {
	f := &fakeKube{getErr: errors.New("not found"), nodes: []string{"node-1"}}
	if _, _, err := (Adopter{Kube: f}).Adopt(context.Background(), "id", "node-1", "UFVCS0VZ"); err == nil {
		t.Fatal("expected an error so the caller retries")
	}
	if len(f.annotated) != 0 {
		t.Fatal("published a key before it knew who it was talking to")
	}
}

func TestAdoptionRefusesAnIncompleteAddress(t *testing.T) {
	f := &fakeKube{data: map[string]string{"url": "https://x:1"}, nodes: []string{"node-1"}}
	if _, _, err := (Adopter{Kube: f}).Adopt(context.Background(), "id", "node-1", "UFVCS0VZ"); err == nil {
		t.Fatal("an address with no fingerprint must be refused: there would be nothing to pin")
	}
}

func TestAdoptionRefusesANodeNameThatIsNotAKubernetesName(t *testing.T) {
	f := &fakeKube{data: goodEndpoint, nodes: []string{"../../secrets"}}
	a := Adopter{Kube: f, Enroll: func(context.Context, Link, EnrollRequest) (EnrollResult, error) {
		t.Fatal("must not enrol")
		return EnrollResult{}, nil
	}}
	if _, _, err := a.Adopt(context.Background(), "id", "../../secrets", "UFVCS0VZ"); err == nil {
		t.Fatal("expected a refusal")
	}
	if len(f.annotated) != 0 {
		t.Fatalf("a hostile name was used: %v", f.annotated)
	}
}
