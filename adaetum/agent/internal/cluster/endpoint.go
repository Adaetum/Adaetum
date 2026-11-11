package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/membership"
)

// The cluster agent tells the host agents where it is, so nobody has to type an address or a
// fingerprint. It writes both into a ConfigMap that cluster nodes can read. Neither value is
// secret: the address is a cluster-internal Service address, and the fingerprint is a hash of
// the agent's public certificate, which a host pins before it trusts anything.

// InClusterServiceURL is the address host agents should use: this pod's own Service, as
// Kubernetes injects it into every pod's environment. Hosts that are cluster nodes can reach a
// Service address directly. The second return is false outside a pod or without the Service.
func InClusterServiceURL(serviceEnvPrefix string) (string, bool) {
	host := os.Getenv(serviceEnvPrefix + "_SERVICE_HOST")
	port := os.Getenv(serviceEnvPrefix + "_SERVICE_PORT")
	if host == "" || port == "" {
		return "", false
	}
	return "https://" + net.JoinHostPort(host, port), true
}

// PublishEndpoint writes (creating it if need be) the ConfigMap hosts read. It uses the pod's
// own service account, which may touch only ConfigMaps in its own namespace.
func PublishEndpoint(ctx context.Context, apiEndpoint, tokenFile, caFile, namespace, url, fingerprint string) error {
	client, bearer, closeIdle, err := kubeClient(tokenFile, caFile)
	if err != nil {
		return err
	}
	defer closeIdle()

	base := strings.TrimRight(apiEndpoint, "/") + "/api/v1/namespaces/" + namespace + "/configmaps"
	data := map[string]string{"url": url, "fingerprint": fingerprint}
	send := func(method, target, contentType string, body any) (int, error) {
		raw, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(raw))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", contentType)
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, nil
	}

	status, err := send(http.MethodPatch, base+"/"+membership.EndpointConfigMap, "application/merge-patch+json", map[string]any{"data": data})
	if err != nil {
		return fmt.Errorf("publish the cluster agent's address: %w", err)
	}
	if status == http.StatusNotFound {
		status, err = send(http.MethodPost, base, "application/json", map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": membership.EndpointConfigMap, "namespace": namespace},
			"data":     data,
		})
		if err != nil {
			return fmt.Errorf("publish the cluster agent's address: %w", err)
		}
	}
	if status < 200 || status > 299 {
		return fmt.Errorf("publish the cluster agent's address: API returned HTTP %d", status)
	}
	return nil
}

// RunPublishEndpoint keeps the ConfigMap current: now, soon after a failure, and on a slow
// timer after a success so a deleted ConfigMap comes back. It ends with ctx.
func RunPublishEndpoint(ctx context.Context, publish func(context.Context) error, report func(error)) {
	wait := time.Duration(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err := publish(ctx); err != nil {
			if report != nil {
				report(err)
			}
			wait = 30 * time.Second
			continue
		}
		wait = 10 * time.Minute
	}
}
