package cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/identity"
	"github.com/Adaetum/Adaetum/agent/internal/membership"
)

const serviceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// InClusterNodes reads Kubernetes' authoritative node list with the pod's own
// read-only service account. The projected token is reread on each call because
// Kubernetes rotates it while the pod is running.
func InClusterNodes() NodeLister {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return func(context.Context) ([]string, error) {
			return nil, fmt.Errorf("Kubernetes service address is unavailable; run in a cluster or use -ssh for development")
		}
	}
	return KubernetesNodes("https://"+net.JoinHostPort(host, port), serviceAccountDir+"/token", serviceAccountDir+"/ca.crt")
}

// NodeKeyLister returns, for each Kubernetes node that has published a host key, that key
// by the node's short name. A node publishes its key by annotating its own Node object
// (membership.HostKeyAnnotation), which only that node, or a cluster administrator, can do.
// That is what lets the cluster agent trust a host's key without a person-made token.
type NodeKeyLister func(ctx context.Context) (map[string]string, error)

// InClusterNodeKeys reads the published host keys with the pod's own read-only service account.
func InClusterNodeKeys() NodeKeyLister {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return func(context.Context) (map[string]string, error) {
			return nil, fmt.Errorf("Kubernetes service address is unavailable")
		}
	}
	return KubernetesNodeKeys("https://"+net.JoinHostPort(host, port), serviceAccountDir+"/token", serviceAccountDir+"/ca.crt")
}

// KubernetesNodeKeys is InClusterNodeKeys with explicit paths, for tests.
func KubernetesNodeKeys(endpoint, tokenFile, caFile string) NodeKeyLister {
	return func(ctx context.Context) (map[string]string, error) {
		nodes, err := listNodeObjects(ctx, endpoint, tokenFile, caFile)
		if err != nil {
			return nil, err
		}
		keys := map[string]string{}
		for _, node := range nodes {
			if key := strings.TrimSpace(node.Metadata.Annotations[membership.HostKeyAnnotation]); key != "" {
				keys[identity.ShortName(node.Metadata.Name)] = key
			}
		}
		return keys, nil
	}
}

// KubernetesNodes constructs a lister from explicit paths to permit local tests;
// the service-account CA is the only trusted authority for this connection.
func KubernetesNodes(endpoint, tokenFile, caFile string) NodeLister {
	return func(ctx context.Context) ([]string, error) {
		nodes, err := listNodeObjects(ctx, endpoint, tokenFile, caFile)
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(nodes))
		for _, node := range nodes {
			names = append(names, node.Metadata.Name)
		}
		return names, nil
	}
}

type nodeObject struct {
	Metadata struct {
		Name        string            `json:"name"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
}

// kubeClient is an HTTP client that trusts only the service-account CA, and the bearer token
// to present. The token is reread on every call because Kubernetes rotates it.
func kubeClient(tokenFile, caFile string) (*http.Client, string, func(), error) {
	token, err := os.ReadFile(tokenFile)
	if err != nil {
		return nil, "", nil, fmt.Errorf("read Kubernetes service account token: %w", err)
	}
	bearer := strings.TrimSpace(string(token))
	if bearer == "" {
		return nil, "", nil, fmt.Errorf("Kubernetes service account token is empty")
	}
	ca, err := os.ReadFile(caFile)
	if err != nil {
		return nil, "", nil, fmt.Errorf("read Kubernetes service account CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, "", nil, fmt.Errorf("Kubernetes service account CA is invalid")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client, bearer, transport.CloseIdleConnections, nil
}

// listNodeObjects reads every Node with the service account at tokenFile.
func listNodeObjects(ctx context.Context, endpoint, tokenFile, caFile string) ([]nodeObject, error) {
	client, bearer, closeIdle, err := kubeClient(tokenFile, caFile)
	if err != nil {
		return nil, err
	}
	defer closeIdle()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/api/v1/nodes", nil)
	if err != nil {
		return nil, fmt.Errorf("prepare Kubernetes node list: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list Kubernetes nodes: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list Kubernetes nodes: API returned HTTP %d", resp.StatusCode)
	}
	var list struct {
		Items *[]nodeObject `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode Kubernetes node list: %w", err)
	}
	if list.Items == nil {
		return nil, fmt.Errorf("Kubernetes node list is missing its items array")
	}
	for _, item := range *list.Items {
		if item.Metadata.Name == "" {
			return nil, fmt.Errorf("Kubernetes returned a node without a name")
		}
	}
	return *list.Items, nil
}
