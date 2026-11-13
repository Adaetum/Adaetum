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

// TailnetHTTPS reports whether HTTPS certificates are enabled on the token's tailnet.
// known is false when the answer did not say, for example an older API, so a caller never
// treats "unknown" as "off". The setting gates every Tailscale ingress in the cluster.
func (c Client) TailnetHTTPS(ctx context.Context, token string) (on, known bool, err error) {
	var doc struct {
		HTTPSEnabled *bool `json:"httpsEnabled"`
	}
	status, err := c.get(ctx, c.tailscaleBase()+"/tailnet/-/settings", token, &doc)
	if err != nil {
		return false, false, fmt.Errorf("unable to read the Tailscale HTTPS setting: %w", err)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return false, false, fmt.Errorf("%w: Tailscale refused it while reading tailnet settings", ErrRejected)
	}
	if status >= 400 {
		return false, false, fmt.Errorf("unable to read the Tailscale HTTPS setting: HTTP %d", status)
	}
	if doc.HTTPSEnabled == nil {
		return false, false, nil
	}
	return *doc.HTTPSEnabled, true, nil
}

// EnableTailnetHTTPS turns HTTPS certificates on, then reads the setting back so a
// success is only reported when Tailscale actually shows it enabled. Writing it needs a
// token that may change tailnet settings (the networking_settings scope, or an admin's
// access token); a refusal is returned as ErrRejected for the caller to explain.
func (c Client) EnableTailnetHTTPS(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.tailscaleBase()+"/tailnet/-/settings", strings.NewReader(`{"httpsEnabled":true}`))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "adaetum-installer")
	resp, err := c.http().Do(req)
	if err != nil {
		return errors.New(strings.ReplaceAll(err.Error(), token, "[token]"))
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: Tailscale refused it while changing tailnet settings", ErrRejected)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("unable to turn on Tailscale HTTPS: HTTP %d", resp.StatusCode)
	}
	on, known, err := c.TailnetHTTPS(ctx, token)
	if err != nil {
		return err
	}
	if !known || !on {
		return errors.New("Tailscale accepted the change but does not show HTTPS certificates as enabled")
	}
	return nil
}

func (c Client) tailscaleBase() string {
	if c.TailscaleBase != "" {
		return c.TailscaleBase
	}
	return tailscaleBase
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

// Tailnets discovers DNS names from devices and, for an empty tailnet, from the
// MagicDNS search domain when the API exposes it. No result is not an error.
func (c Client) Tailnets(ctx context.Context, token string) ([]string, error) {
	base := c.TailscaleBase
	if base == "" {
		base = tailscaleBase
	}
	var doc struct {
		Devices []struct {
			DNSName string `json:"name"`
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
	if len(seen) == 0 {
		// Tailscale documents the MagicDNS domain as the first search domain.
		// Only use it when MagicDNS is enabled and it has the expected form;
		// custom search domains must not be mistaken for the tailnet name.
		var preferences struct {
			MagicDNS bool `json:"magicDNS"`
		}
		var paths struct {
			SearchPaths []string `json:"searchPaths"`
		}
		prefsStatus, prefsErr := c.get(ctx, base+"/tailnet/-/dns/preferences", token, &preferences)
		if prefsErr == nil && prefsStatus < 400 && preferences.MagicDNS {
			pathsStatus, pathsErr := c.get(ctx, base+"/tailnet/-/dns/searchpaths", token, &paths)
			if pathsErr == nil && pathsStatus < 400 && len(paths.SearchPaths) > 0 {
				name := strings.TrimRight(strings.ToLower(strings.TrimSpace(paths.SearchPaths[0])), ".")
				label, suffix, ok := strings.Cut(name, ".")
				if ok && suffix == "ts.net" && label != "" && strings.Trim(label, "abcdefghijklmnopqrstuvwxyz0123456789-") == "" && !strings.HasPrefix(label, "-") && !strings.HasSuffix(label, "-") {
					seen[name] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}
