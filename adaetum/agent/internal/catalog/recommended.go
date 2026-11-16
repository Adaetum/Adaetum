package catalog

import (
	"context"
	"strings"
	"sync"
	"time"
)

// recommendedApps is the short, hand-picked list the Find apps page offers before anyone
// searches: well-known, self-hostable apps whose charts are maintained and widely used.
//
// It is a list of Artifact Hub package IDs, not copies of their details. Stars, version,
// publisher and the verified/signed badges are read from Artifact Hub when the page opens, so
// they are never stale or invented here, and a package that disappears or is retired is simply
// left out. Choosing an app is still the same preview as for a search result: this list only
// saves someone from having to know what to search for. Keep it short, and change an entry
// only for a better-maintained chart of the same app.
var recommendedApps = []struct{ id, why string }{
	{"nextcloud/nextcloud", "Your own file sync, calendar and contacts, in place of a cloud drive."},
	{"immich/immich", "A private photo and video library with phone backup."},
	{"uptime-kuma/uptime-kuma", "Watches your sites and services and tells you when one goes down."},
	{"helm-hass/home-assistant", "Runs and automates smart-home devices without a vendor cloud."},
	{"mojo2600/pihole", "Blocks ads and trackers for every device on your network."},
	{"community-charts/n8n", "Connects your apps and automates tasks with a visual workflow editor."},
}

// Recommendation is a recommended app: the same details a search result carries, plus why
// it is on the list.
type Recommendation struct {
	Entry
	Why string `json:"why"`
}

// Recommended looks up every recommended app on Artifact Hub, in the list's order. An app
// that cannot be read, or that its publisher has retired, is left out; Notes says so once
// instead of failing the page.
func (c *Client) Recommended(ctx context.Context) ([]Recommendation, []string) {
	found := make([]*Recommendation, len(recommendedApps))
	var wait sync.WaitGroup
	for index, app := range recommendedApps {
		wait.Add(1)
		go func() {
			defer wait.Done()
			repository, name, _ := strings.Cut(app.id, "/")
			p, err := c.chart(ctx, repository, name)
			if err != nil || p.Deprecated {
				return
			}
			if p.Name == "" {
				p.Name = name
			}
			if p.Repository.Name == "" {
				p.Repository.Name = repository
			}
			found[index] = &Recommendation{Entry: p.entry(), Why: app.why}
		}()
	}
	wait.Wait()

	var apps []Recommendation
	for _, recommendation := range found {
		if recommendation != nil {
			apps = append(apps, *recommendation)
		}
	}
	var notes []string
	if missing := len(recommendedApps) - len(apps); missing > 0 {
		notes = append(notes, "Some suggestions could not be loaded from Artifact Hub right now.")
	}
	return apps, notes
}

// recommendedCache keeps the answer so opening the page does not make six requests to
// Artifact Hub each time. A complete answer is kept for hours; an incomplete one only
// briefly, so a temporary failure is retried soon.
type recommendedCache struct {
	mu    sync.Mutex
	until time.Time
	apps  []Recommendation
	notes []string
}

const (
	recommendedFor      = 6 * time.Hour
	recommendedRetryFor = time.Minute
)

func (rc *recommendedCache) get(ctx context.Context, c *Client, now func() time.Time) ([]Recommendation, []string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if now().Before(rc.until) {
		return rc.apps, rc.notes
	}
	// The answer is shared by everyone who opens the page, so one visitor leaving must not
	// cancel it half-way and leave a partial list in the cache.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	apps, notes := c.Recommended(ctx)
	ttl := recommendedFor
	if len(notes) > 0 {
		ttl = recommendedRetryFor
	}
	rc.apps, rc.notes, rc.until = apps, notes, now().Add(ttl)
	return apps, notes
}
