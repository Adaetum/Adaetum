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

// KubernetesNodes constructs a lister from explicit paths to permit local tests;
// the service-account CA is the only trusted authority for this connection.
func KubernetesNodes(endpoint, tokenFile, caFile string) NodeLister {
	return func(ctx context.Context) ([]string, error) {
		token, err := os.ReadFile(tokenFile)
		if err != nil {
			return nil, fmt.Errorf("read Kubernetes service account token: %w", err)
		}
		bearer := strings.TrimSpace(string(token))
		if bearer == "" {
			return nil, fmt.Errorf("Kubernetes service account token is empty")
		}
		ca, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read Kubernetes service account CA: %w", err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(ca) {
			return nil, fmt.Errorf("Kubernetes service account CA is invalid")
		}
		transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
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
			Items *[]struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
			} `json:"items"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&list); err != nil {
			return nil, fmt.Errorf("decode Kubernetes node list: %w", err)
		}
		if list.Items == nil {
			return nil, fmt.Errorf("Kubernetes node list is missing its items array")
		}
		names := make([]string, 0, len(*list.Items))
		for _, item := range *list.Items {
			if item.Metadata.Name == "" {
				return nil, fmt.Errorf("Kubernetes returned a node without a name")
			}
			names = append(names, item.Metadata.Name)
		}
		return names, nil
	}
}
