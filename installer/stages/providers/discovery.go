package providers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Adaetum/Adaetum/installer/provider"
	"tailscale.com/tsnet"
)

var errDiscoveryCleanup = errors.New("temporary Tailscale setup identity could not be cleaned up")

type discoveryNode func(context.Context, string, string, string) (string, error)

// discoverEmptyTailnet joins a temporary, isolated tsnet node because the public
// API does not expose the DNS suffix for a tailnet without devices. Cleanup uses
// a fresh context so cancelling setup still removes the node and its auth key.
func discoverEmptyTailnet(ctx context.Context, api provider.Client, token string, join discoveryNode) (name string, err error) {
	base := api.TailscaleBase
	if base == "" {
		base = "https://api.tailscale.com/api/v2"
	}
	client := api.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	call := func(ctx context.Context, method, path string, body any, out any) error {
		var input io.Reader
		if body != nil {
			b, e := json.Marshal(body)
			if e != nil {
				return e
			}
			input = bytes.NewReader(b)
		}
		req, e := http.NewRequestWithContext(ctx, method, base+path, input)
		if e != nil {
			return errors.New("invalid Tailscale request")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(req)
		if e != nil {
			return errors.New("Tailscale request failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound && method == http.MethodDelete {
			return nil
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("Tailscale returned HTTP %d", resp.StatusCode)
		}
		if out != nil {
			if e = json.NewDecoder(resp.Body).Decode(out); e != nil {
				return errors.New("Tailscale returned an invalid response")
			}
		}
		return nil
	}
	random := make([]byte, 8)
	if _, err = rand.Read(random); err != nil {
		return "", err
	}
	host := "adaetum-setup-" + hex.EncodeToString(random)
	dir, err := os.MkdirTemp("", "adaetum-tailnet-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	var key struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	body := map[string]any{"description": "Temporary Adaetum setup", "expirySeconds": 3600, "capabilities": map[string]any{"devices": map[string]any{"create": map[string]any{"reusable": false, "ephemeral": true, "preauthorized": true}}}}
	if err = call(ctx, http.MethodPost, "/tailnet/-/keys", body, &key); err != nil {
		return "", err
	}
	if key.ID == "" {
		return "", errors.Join(errDiscoveryCleanup, errors.New("Tailscale returned a temporary key without an identifier; check Tailscale Keys"))
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var list struct {
			Devices []struct {
				ID       string `json:"id"`
				NodeID   string `json:"nodeId"`
				Hostname string `json:"hostname"`
				Name     string `json:"name"`
			} `json:"devices"`
		}
		var problems []error
		if e := call(cleanup, http.MethodGet, "/tailnet/-/devices", nil, &list); e != nil {
			problems = append(problems, e)
		} else {
			for _, d := range list.Devices {
				if d.Hostname == host || strings.Split(d.Name, ".")[0] == host {
					id := d.ID
					if id == "" {
						id = d.NodeID
					}
					if id == "" {
						problems = append(problems, errors.New("temporary node identifier missing"))
						continue
					}
					if e := call(cleanup, http.MethodDelete, "/device/"+url.PathEscape(id), nil, nil); e != nil {
						problems = append(problems, e)
					}
				}
			}
			list.Devices = nil
			if e := call(cleanup, http.MethodGet, "/tailnet/-/devices", nil, &list); e != nil {
				problems = append(problems, e)
			} else {
				for _, d := range list.Devices {
					if d.Hostname == host || strings.Split(d.Name, ".")[0] == host {
						problems = append(problems, errors.New("temporary node is still visible"))
					}
				}
			}
		}
		if e := call(cleanup, http.MethodDelete, "/tailnet/-/keys/"+url.PathEscape(key.ID), nil, nil); e != nil {
			problems = append(problems, e)
		}
		if len(problems) > 0 {
			err = errors.Join(err, errDiscoveryCleanup, errors.Join(problems...))
		}
	}()
	if key.Key == "" {
		return "", errors.New("Tailscale returned an incomplete temporary key")
	}
	name, err = join(ctx, host, dir, key.Key)
	if err != nil {
		return "", err
	}
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if !tailnetName.MatchString(name) {
		return "", errors.New("temporary Tailscale node did not receive a valid DNS suffix")
	}
	return name, nil
}

func joinDiscoveryNode(ctx context.Context, host, dir, key string) (string, error) {
	node := &tsnet.Server{Hostname: host, Dir: dir, AuthKey: key, Ephemeral: true, Logf: func(string, ...any) {}, UserLogf: func(string, ...any) {}}
	defer node.Close()
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	status, err := node.Up(ctx)
	if err != nil {
		return "", errors.New("temporary Tailscale node could not join")
	}
	if status.CurrentTailnet == nil {
		return "", errors.New("temporary Tailscale node has no tailnet information")
	}
	return status.CurrentTailnet.MagicDNSSuffix, nil
}
