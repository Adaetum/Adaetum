package backups

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A tiny cluster: one schedule "daily" (merged), and volumes described by their labels.
type volumeSpec struct {
	name, namespace, claim, created string
	labels                          map[string]string
}

type cluster struct {
	volumes []volumeSpec
	synced  map[string]bool // "namespace/claim" has a ReplicationSource
	noSync  bool            // VolSync not installed
	noJobs  bool
}

func (c cluster) get(_ context.Context, resource string, _ ...string) ([]byte, error) {
	switch resource {
	case "backuptargets.longhorn.io":
		return []byte(`{"items":[]}`), nil
	case "recurringjobs.longhorn.io":
		if c.noJobs {
			return []byte(`{"items":[]}`), nil
		}
		return []byte(`{"items":[{"metadata":{"name":"backup-daily"},"spec":{"task":"backup","cron":"0 3 * * *","retain":7,"groups":["adaetum-backup-daily"]}}]}`), nil
	case "volumes.longhorn.io":
		var items []string
		for _, v := range c.volumes {
			labels, _ := json.Marshal(v.labels)
			items = append(items, fmt.Sprintf(`{"metadata":{"name":%q,"creationTimestamp":%q,"labels":%s},"spec":{"size":"2147483648"},"status":{"actualSize":1048576,"kubernetesStatus":{"namespace":%q,"pvcName":%q}}}`, v.name, v.created, labels, v.namespace, v.claim))
		}
		return []byte(`{"items":[` + strings.Join(items, ",") + `]}`), nil
	case "replicationsources.volsync.backube":
		if c.noSync {
			return nil, context.DeadlineExceeded
		}
		var items []string
		for key := range c.synced {
			parts := strings.SplitN(key, "/", 2)
			items = append(items, fmt.Sprintf(`{"metadata":{"namespace":%q},"spec":{"sourcePVC":%q},"status":{"lastSyncTime":"2026-11-17T00:00:00Z"}}`, parts[0], parts[1]))
		}
		return []byte(`{"items":[` + strings.Join(items, ",") + `]}`), nil
	case "deployments":
		return []byte(`{"items":[{"metadata":{"name":"volsync"},"spec":{"replicas":1},"status":{"readyReplicas":1}}]}`), nil
	}
	return nil, nil
}

type fakeVault struct {
	data    map[string]map[string]any
	written map[string]map[string]any
}

func newVault() *fakeVault {
	return &fakeVault{data: map[string]map[string]any{}, written: map[string]map[string]any{}}
}

func (f *fakeVault) ReadFields(_ context.Context, path string) (map[string]any, int, bool, error) {
	fields, ok := f.data[path]
	return fields, 1, ok, nil
}
func (f *fakeVault) WriteFields(_ context.Context, path string, fields map[string]any, _ int) error {
	f.written[path] = fields
	f.data[path] = fields
	return nil
}

func mustTime(text string) time.Time {
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		panic(err)
	}
	return at
}

func fakeCloudflare(t *testing.T, accounts int) (*httptest.Server, *[]string) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var result any
		switch {
		case r.URL.Path == "/accounts":
			list := []map[string]string{}
			for i := 0; i < accounts; i++ {
				list = append(list, map[string]string{"id": "acct" + string(rune('1'+i)), "name": "Account"})
			}
			result = list
		case strings.HasSuffix(r.URL.Path, "/permission_groups"):
			result = []map[string]string{{"id": "r", "name": "Workers R2 Storage Bucket Item Read"}, {"id": "w", "name": "Workers R2 Storage Bucket Item Write"}}
		case strings.HasSuffix(r.URL.Path, "/r2/buckets"):
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			result = map[string]string{"name": body["name"]}
		case strings.HasSuffix(r.URL.Path, "/tokens"):
			var body struct {
				Policies []struct {
					Resources map[string]string `json:"resources"`
				} `json:"policies"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			for resource := range body.Policies[0].Resources {
				if !strings.HasPrefix(resource, "com.cloudflare.edge.r2.bucket.acct1_default_adaetum-") {
					t.Errorf("the key is not limited to the new bucket: %s", resource)
				}
			}
			result = map[string]string{"id": "key-id", "value": "key-value"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
	}))
	t.Cleanup(server.Close)
	return server, &calls
}
