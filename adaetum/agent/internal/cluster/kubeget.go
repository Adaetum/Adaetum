package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// KubeReader reads Kubernetes objects with the pod's own service account, so the cluster
// agent can show what a host agent shows (workloads, pods, events...) without kubectl and
// without any host access. It is read-only by construction: every request is a GET.
//
// The inspect package asks for data in kubectl's words ("pods", "-A", "-n ns"), because that
// is how the host agent reads the same cluster. Get translates exactly the few forms that
// package uses; anything else is refused rather than guessed.
type KubeReader struct {
	endpoint, tokenFile, caFile string
}

// InClusterReader reads with the pod's service account. Outside a cluster it returns nil.
func InClusterReader() *KubeReader {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil
	}
	return NewKubeReader("https://"+net.JoinHostPort(host, port), serviceAccountDir+"/token", serviceAccountDir+"/ca.crt")
}

// NewKubeReader is InClusterReader with explicit paths, for tests.
func NewKubeReader(endpoint, tokenFile, caFile string) *KubeReader {
	return &KubeReader{endpoint: strings.TrimRight(endpoint, "/"), tokenFile: tokenFile, caFile: caFile}
}

// kind says where one resource type lives in the API. Item kinds are what a kubectl list
// would have put on each item; a REST list leaves them off, and the parsers read them.
type kind struct {
	prefix     string // "/api/v1" or "/apis/<group>/<version>"
	plural     string
	itemKind   string
	namespaced bool
}

// kinds is every resource the report reads. Adding one here must be matched by a read-only
// rule in pods/adaetum/agent/serviceaccount.yaml, or the page shows a permission error.
var kinds = map[string]kind{
	"nodes":                        {"/api/v1", "nodes", "Node", false},
	"pods":                         {"/api/v1", "pods", "Pod", true},
	"services":                     {"/api/v1", "services", "Service", true},
	"events":                       {"/api/v1", "events", "Event", true},
	"persistentvolumeclaims":       {"/api/v1", "persistentvolumeclaims", "PersistentVolumeClaim", true},
	"deployments":                  {"/apis/apps/v1", "deployments", "Deployment", true},
	"statefulsets":                 {"/apis/apps/v1", "statefulsets", "StatefulSet", true},
	"daemonsets":                   {"/apis/apps/v1", "daemonsets", "DaemonSet", true},
	"ingresses":                    {"/apis/networking.k8s.io/v1", "ingresses", "Ingress", true},
	"storageclasses":               {"/apis/storage.k8s.io/v1", "storageclasses", "StorageClass", false},
	"horizontalpodautoscalers":     {"/apis/autoscaling/v2", "horizontalpodautoscalers", "HorizontalPodAutoscaler", true},
	"applications.argoproj.io":     {"/apis/argoproj.io/v1alpha1", "applications", "Application", true},
	"certificates.cert-manager.io": {"/apis/cert-manager.io/v1", "certificates", "Certificate", true},
	"volumes.longhorn.io":          {"/apis/longhorn.io/v1beta2", "volumes", "Volume", true},
	"nodes.longhorn.io":            {"/apis/longhorn.io/v1beta2", "nodes", "Node", true},
	"settings.longhorn.io":         {"/apis/longhorn.io/v1beta2", "settings", "Setting", true},
}

// Get answers what `kubectl get <resources> [-A | -n ns] [--field-selector s] [name] -o json`
// would: one object for a name, else a List holding every kind asked for.
func (k *KubeReader) Get(ctx context.Context, resources string, extra ...string) ([]byte, error) {
	var namespace, selector, name string
	for index := 0; index < len(extra); index++ {
		switch word := extra[index]; word {
		case "-A":
		case "-n", "--field-selector":
			index++
			if index >= len(extra) {
				return nil, fmt.Errorf("kubernetes get %s: %s needs a value", resources, word)
			}
			if word == "-n" {
				namespace = extra[index]
			} else {
				selector = extra[index]
			}
		default:
			if strings.HasPrefix(word, "-") || name != "" {
				return nil, fmt.Errorf("kubernetes get %s: unsupported argument %q", resources, word)
			}
			name = word
		}
	}
	names := strings.Split(resources, ",")
	if name != "" && len(names) != 1 {
		return nil, fmt.Errorf("kubernetes get %s: a name needs exactly one resource type", resources)
	}
	client, bearer, closeIdle, err := kubeClient(k.tokenFile, k.caFile)
	if err != nil {
		return nil, err
	}
	defer closeIdle()

	var items []json.RawMessage
	for _, resource := range names {
		spec, ok := kinds[resource]
		if !ok {
			return nil, fmt.Errorf("kubernetes get %s: not a resource the cluster agent reads", resource)
		}
		path := spec.prefix
		if spec.namespaced && namespace != "" {
			path += "/namespaces/" + url.PathEscape(namespace)
		}
		path += "/" + spec.plural
		if name != "" {
			path += "/" + url.PathEscape(name)
		}
		query := ""
		if selector != "" {
			query = "?fieldSelector=" + url.QueryEscape(selector)
		}
		body, err := k.fetch(ctx, client, bearer, path+query)
		if err != nil {
			return nil, fmt.Errorf("kubernetes get %s: %w", resource, err)
		}
		if name != "" {
			return body, nil
		}
		var list struct {
			Items []map[string]json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, fmt.Errorf("kubernetes get %s: %w", resource, err)
		}
		itemKind, _ := json.Marshal(spec.itemKind)
		for _, item := range list.Items {
			item["kind"] = itemKind
			encoded, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			items = append(items, encoded)
		}
	}
	if items == nil {
		items = []json.RawMessage{}
	}
	return json.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": items})
}

// Raw reads an API path as `kubectl get --raw` does (the metrics API, a node's stats).
func (k *KubeReader) Raw(ctx context.Context, path string) ([]byte, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("kubernetes raw read: %q is not an API path", path)
	}
	client, bearer, closeIdle, err := kubeClient(k.tokenFile, k.caFile)
	if err != nil {
		return nil, err
	}
	defer closeIdle()
	body, err := k.fetch(ctx, client, bearer, path)
	if err != nil {
		return nil, fmt.Errorf("kubernetes raw %s: %w", path, err)
	}
	return body, nil
}

// maxKubeBody bounds one answer; a large cluster's pod list is a few megabytes.
const maxKubeBody = 64 << 20

func (k *KubeReader) fetch(ctx context.Context, client *http.Client, bearer, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.endpoint+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxKubeBody))
}
