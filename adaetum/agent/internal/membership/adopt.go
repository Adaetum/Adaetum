package membership

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Adaetum/Adaetum/agent/internal/identity"
)

// Automatic adoption: how a host agent joins the cluster agent with nobody typing a token.
//
// Kubernetes is the notary. A host that is a cluster node publishes its public key on its
// own Node object, which only that node (or a cluster administrator) can write. The cluster
// agent reads the node list anyway; when a host enrols with the key its Node carries, the
// key is vouched for by Kubernetes and the host is approved. Naming a node is not enough: a
// stranger who claims a node's name has no way to put their key on that Node object.
//
// The host finds the cluster agent the same way: the cluster agent publishes its address
// and certificate fingerprint in a ConfigMap, and the host reads it with the node
// credentials it already has. Neither value is secret; the fingerprint is what the host pins.

const (
	// HostKeyAnnotation is the Node annotation that carries a host's public key.
	HostKeyAnnotation = "adaetum.io/host-key"
	// EndpointNamespace and EndpointConfigMap name the ConfigMap in which the cluster agent
	// publishes where it can be reached. It holds the keys "url" and "fingerprint".
	EndpointNamespace = "adaetum"
	EndpointConfigMap = "adaetum-cluster-agent-endpoint"
)

// KubeAPI is the three Kubernetes calls adoption needs. In production it is a
// kubeconfig.Client holding the node's own credentials; tests use a fake.
type KubeAPI interface {
	ConfigMapData(ctx context.Context, namespace, name string) (map[string]string, error)
	NodeNames(ctx context.Context) ([]string, error)
	AnnotateNode(ctx context.Context, node, key, value string) error
}

// Adopter performs one adoption attempt.
type Adopter struct {
	Kube KubeAPI
	// Enroll asks the cluster agent to accept the host; it is membership.Enroll in production.
	Enroll func(ctx context.Context, link Link, request EnrollRequest) (EnrollResult, error)
}

// nodeNameOK is a Kubernetes node name (a DNS-1123 subdomain). Node names only ever travel in a
// URL path here, escaped, but a name that is not valid is a sign something is wrong.
var nodeNameOK = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)

// ErrNotANode means Kubernetes does not list this machine, so there is nothing to vouch for it.
var ErrNotANode = errors.New("this machine is not a node in the cluster Kubernetes can see")

// Adopt publishes this host's key on its Node, finds the cluster agent, and enrols without a
// token. The result is returned for the caller to save as the host's link: a link is saved
// for approved and pending hosts alike, so events start flowing the moment a person approves
// a pending one. A refusal returns the result and a nil error so the reason can be shown.
func (a Adopter) Adopt(ctx context.Context, hostID, hostname, publicKey string) (Link, EnrollResult, error) {
	if raw, err := base64.StdEncoding.DecodeString(publicKey); err != nil || len(raw) == 0 || strings.HasPrefix(publicKey, "-") {
		return Link{}, EnrollResult{}, errors.New("this host's public key is not valid base64")
	}
	data, err := a.Kube.ConfigMapData(ctx, EndpointNamespace, EndpointConfigMap)
	if err != nil {
		return Link{}, EnrollResult{}, fmt.Errorf("the cluster agent has not published where it is yet: %w", err)
	}
	if data["url"] == "" || data["fingerprint"] == "" {
		return Link{}, EnrollResult{}, errors.New("the cluster agent's published address is incomplete")
	}
	link := Link{URL: data["url"], Fingerprint: NormalizeFingerprint(data["fingerprint"])}

	nodeName, err := a.ownNode(ctx, hostname)
	if err != nil {
		return Link{}, EnrollResult{}, err
	}
	// Idempotent: the same key every time, so a restart or a retry changes nothing.
	if err := a.Kube.AnnotateNode(ctx, nodeName, HostKeyAnnotation, publicKey); err != nil {
		return Link{}, EnrollResult{}, fmt.Errorf("could not publish this host's key on its node: %w", err)
	}

	result, err := a.Enroll(ctx, link, EnrollRequest{HostID: hostID, Hostname: hostname, PublicKey: publicKey})
	if err != nil {
		return Link{}, EnrollResult{}, err
	}
	return link, result, nil
}

// ownNode finds this machine's Node by short name: Kubernetes names nodes in lower case and
// a host name may carry a domain.
func (a Adopter) ownNode(ctx context.Context, hostname string) (string, error) {
	names, err := a.Kube.NodeNames(ctx)
	if err != nil {
		return "", fmt.Errorf("cannot list the cluster's nodes: %w", err)
	}
	short := identity.ShortName(hostname)
	for _, name := range names {
		if short != "" && identity.ShortName(name) == short {
			if !nodeNameOK.MatchString(name) {
				return "", fmt.Errorf("the node name %q is not a valid Kubernetes name", name)
			}
			return name, nil
		}
	}
	return "", ErrNotANode
}

// FirstExisting returns the first path in candidates that exists according to exists, or "".
func FirstExisting(candidates []string, exists func(string) bool) string {
	for _, path := range candidates {
		if strings.TrimSpace(path) != "" && exists(path) {
			return path
		}
	}
	return ""
}
