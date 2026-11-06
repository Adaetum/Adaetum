// Package provider talks to the Cloudflare and Tailscale read APIs that setup
// needs to offer choices. Tokens are sent only as an Authorization header to the
// provider's own address and never appear in an error message.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	cloudflareBase = "https://api.cloudflare.com/client/v4"
	tailscaleBase  = "https://api.tailscale.com/api/v2"
)

// Client reaches the providers. The base URLs are fields so tests can point at a
// local server; the zero value talks to the real services.
type Client struct {
	HTTP           *http.Client
	CloudflareBase string
	TailscaleBase  string
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c Client) get(ctx context.Context, url, token string, into any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "adaetum-installer")
	resp, err := c.http().Do(req)
	if err != nil {
		// A transport error can quote the request; keep the token out of it.
		return 0, errors.New(strings.ReplaceAll(err.Error(), token, "[token]"))
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil && resp.StatusCode < 400 {
		return resp.StatusCode, fmt.Errorf("unreadable answer: %w", err)
	}
	return resp.StatusCode, nil
}

// Zone is a Cloudflare DNS zone and the account that owns it.
type Zone struct {
	Name        string
	AccountID   string
	AccountName string
}

// Label is how a zone is offered to the operator.
func (z Zone) Label() string {
	name := z.AccountName
	if name == "" {
		name = "Unnamed Cloudflare account"
	}
	return z.Name + " — account: " + name
}

// ErrRejected means the provider refused the credential.
var ErrRejected = errors.New("the provider rejected the token")

// Zones lists the active zones a Cloudflare token can read, with owning accounts.
func (c Client) Zones(ctx context.Context, token string) ([]Zone, error) {
	base := c.CloudflareBase
	if base == "" {
		base = cloudflareBase
	}
	seen := map[Zone]bool{}
	for page := 1; page <= 20; page++ {
		var doc struct {
			Success bool `json:"success"`
			Result  []struct {
				Name    string `json:"name"`
				Account struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"account"`
			} `json:"result"`
			Info struct {
				TotalPages int `json:"total_pages"`
			} `json:"result_info"`
		}
		status, err := c.get(ctx, fmt.Sprintf("%s/zones?status=active&per_page=50&page=%d", base, page), token, &doc)
		if err != nil {
			return nil, fmt.Errorf("unable to list Cloudflare zones: %w", err)
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden || !doc.Success {
			return nil, fmt.Errorf("%w: Cloudflare refused it while listing zones; it needs Zone Read for the target zone", ErrRejected)
		}
		for _, r := range doc.Result {
			name := strings.ToLower(strings.TrimSpace(r.Name))
			if name != "" && strings.TrimSpace(r.Account.ID) != "" {
				seen[Zone{name, strings.TrimSpace(r.Account.ID), strings.TrimSpace(r.Account.Name)}] = true
			}
		}
		if page >= doc.Info.TotalPages {
			break
		}
	}
	zones := make([]Zone, 0, len(seen))
	for z := range seen {
		zones = append(zones, z)
	}
	sort.Slice(zones, func(i, j int) bool {
		if zones[i].Name != zones[j].Name {
			return zones[i].Name < zones[j].Name
		}
		return zones[i].AccountID < zones[j].AccountID
	})
	if len(zones) == 0 {
		return nil, errors.New("Cloudflare returned no active zones for this token")
	}
	return zones, nil
}

// Tailnets discovers the tailnet DNS names visible from the devices a Tailscale
// token can read. A new or empty tailnet has none, which is not an error.
func (c Client) Tailnets(ctx context.Context, token string) ([]string, error) {
	base := c.TailscaleBase
	if base == "" {
		base = tailscaleBase
	}
	var doc struct {
		Devices []struct {
			DNSName string `json:"dnsName"`
		} `json:"devices"`
	}
	status, err := c.get(ctx, base+"/tailnet/-/devices", token, &doc)
	if err != nil {
		return nil, fmt.Errorf("unable to read Tailscale devices: %w", err)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, fmt.Errorf("%w: Tailscale refused it while reading devices", ErrRejected)
	}
	if status >= 400 {
		return nil, fmt.Errorf("unable to read Tailscale devices: HTTP %d", status)
	}
	seen := map[string]bool{}
	for _, d := range doc.Devices {
		name := strings.TrimRight(strings.ToLower(strings.TrimSpace(d.DNSName)), ".")
		if _, tailnet, ok := strings.Cut(name, "."); ok {
			seen[tailnet] = true
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}
