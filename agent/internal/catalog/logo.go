package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Logos make the app cards recognisable. They are fetched by the agent, never by the
// browser, from the one place Artifact Hub keeps them, so a page never contacts a
// third party and a logo is checked before it is passed on: a small image of a known
// type, served so that it cannot run anything even if it is a hostile SVG.

const (
	maxLogoSize = 512 << 10
	logoTTL     = 24 * time.Hour
	missTTL     = time.Hour
	maxLogos    = 256
	// Artifact Hub rate-limits (429) a burst of searches, and a page of app cards is
	// exactly that, so lookups go two at a time and pause after any failure.
	lookupSlots = 2
	maxParts    = 8
	backoff     = 5 * time.Minute
	// A name lookup is trusted only for an official listing or a widely used chart. A
	// verified publisher is not enough: it proves who they are, not that their chart
	// is the app this name means ("ingress" would borrow another vendor's logo).
	trustedStars = 50
)

var (
	logoIDPattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	logoTypePattern = regexp.MustCompile(`^image/(png|jpeg|webp|gif|svg\+xml)$`)
)

// logoID returns the id when it has the shape Artifact Hub uses, else nothing.
func logoID(id string) string {
	if logoIDPattern.MatchString(id) {
		return id
	}
	return ""
}

var errNoLogo = errors.New("no logo")

// Logo downloads one Artifact Hub image by id.
func (c *Client) Logo(ctx context.Context, id string) ([]byte, string, error) {
	if logoID(id) == "" {
		return nil, "", errNoLogo
	}
	return c.fetchImage(ctx, fmt.Sprintf("%s/image/%s", c.artifactHub(), id))
}

// fetchImage downloads one picture from a fixed host and checks it before it is passed
// on: a known image type and a small size. Only the media type is trusted from the
// source, and only from a short list.
func (c *Client) fetchImage(ctx context.Context, address string) ([]byte, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("User-Agent", "adaetum-agent-catalog")
	response, err := c.client().Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, "", errNoLogo
	}
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("answered %d", response.StatusCode)
	}
	kind := strings.TrimSpace(strings.SplitN(response.Header.Get("Content-Type"), ";", 2)[0])
	if !logoTypePattern.MatchString(kind) {
		return nil, "", errNoLogo
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxLogoSize+1))
	if err != nil || len(data) == 0 || len(data) > maxLogoSize {
		return nil, "", errNoLogo
	}
	return data, kind, nil
}

// iconAliases are the few apps whose icon is filed under a different name than the one
// they run under.
var iconAliases = map[string]string{"argocd": "argo-cd"}

// iconNames lists the names to try in the icon set: the app's own, a known alias, and
// the name without a trailing "-system" (the namespace of "longhorn-system" is Longhorn).
func iconNames(name string) []string {
	names := []string{name}
	if alias, ok := iconAliases[name]; ok {
		names = append(names, alias)
	}
	if trimmed := strings.TrimSuffix(name, "-system"); trimmed != name && trimmed != "" {
		names = append(names, trimmed)
	}
	return names
}

// notTheApp are components that support an app rather than being it: a database in an
// app's namespace must not give the app a database's logo.
var notTheApp = map[string]bool{"postgresql": true, "postgres": true, "redis": true, "redis-master": true, "mariadb": true, "mysql": true, "valkey": true, "memcached": true, "mongodb": true}

var roleSuffixes = []string{"-controller-manager", "-controller", "-operator", "-server", "-agent", "-node", "-webhook"}

// partNames turns one component name of an app ("calico-node", "media-stack-bazarr")
// into the names its logo might be filed under: the name as is, without the app's own
// prefix, and without a trailing role word.
func partNames(app, part string) []string {
	if notTheApp[part] {
		return nil
	}
	names := []string{part}
	if rest := strings.TrimPrefix(part, app+"-"); rest != part && !notTheApp[rest] {
		names = append(names, rest)
	}
	for _, name := range names {
		for _, suffix := range roleSuffixes {
			if trimmed := strings.TrimSuffix(name, suffix); trimmed != name && trimmed != "" {
				names = append(names, trimmed)
				break
			}
		}
	}
	return names
}

// Icon looks an app up in the curated dashboard-icons set, which is keyed by exact app
// name, so a hit is the app and not a look-alike. It is tried before Artifact Hub
// because it is exact and does not rate-limit.
func (c *Client) Icon(ctx context.Context, name string, parts []string) ([]byte, string, error) {
	if !safeSegment(name) || len(name) > 63 {
		return nil, "", errNoLogo
	}
	candidates := iconNames(name)
	for _, part := range parts {
		candidates = append(candidates, partNames(name, part)...)
	}
	for _, candidate := range candidates {
		if !safeSegment(candidate) {
			continue
		}
		data, kind, err := c.fetchImage(ctx, fmt.Sprintf("%s/%s.png", c.icons(), candidate))
		if err == nil {
			return data, kind, nil
		}
	}
	return nil, "", errNoLogo
}

// LogoIDFor finds the logo of an installed app by its name: the best-known chart with
// exactly that name from a trusted publisher. No match means no logo, not a guess.
func (c *Client) LogoIDFor(ctx context.Context, name string) (string, error) {
	if !safeSegment(name) || len(name) > 63 {
		return "", errNoLogo
	}
	entries, err := c.searchCharts(ctx, name)
	if err != nil {
		return "", err
	}
	best := ""
	stars := -1
	for _, e := range entries {
		if !strings.EqualFold(e.Name, name) || e.Logo == "" || e.Deprecated {
			continue
		}
		if !(e.Official || e.Stars >= trustedStars) || e.Stars <= stars {
			continue
		}
		best, stars = e.Logo, e.Stars
	}
	if best == "" {
		return "", errNoLogo
	}
	return best, nil
}

// normalizeRepo makes the two spellings of a chart repository comparable: Argo CD
// writes an OCI repository without a scheme, Artifact Hub with "oci://".
func normalizeRepo(address string) string {
	address = strings.ToLower(strings.TrimSpace(address))
	for _, scheme := range []string{"oci://", "https://", "http://"} {
		address = strings.TrimPrefix(address, scheme)
	}
	return strings.TrimRight(address, "/")
}

// LogoIDForChart finds the logo of the exact chart an app is deployed from. The match
// is on the chart name AND its repository address, so it is that chart and nothing
// that merely shares a name. The address is only compared, never fetched.
func (c *Client) LogoIDForChart(ctx context.Context, chart, repo string) (string, error) {
	if !safeSegment(chart) || len(chart) > 63 || repo == "" || len(repo) > 200 {
		return "", errNoLogo
	}
	packages, err := c.searchPackages(ctx, chart)
	if err != nil {
		return "", err
	}
	for _, p := range packages {
		if p.Name == chart && normalizeRepo(p.Repository.URL) == normalizeRepo(repo) {
			if id := logoID(p.LogoImageID); id != "" {
				return id, nil
			}
		}
	}
	return "", errNoLogo
}

// Migrations adds the logo cache to the cluster agent's database. Lookups cost the
// outside sources' patience (Artifact Hub rate-limits), so what was found, and what was
// found to be missing, outlives a restart. Nothing here is configuration or a secret.
var Migrations = []string{
	`CREATE TABLE app_logos (
		key      TEXT PRIMARY KEY,
		kind     TEXT NOT NULL,
		data     BLOB,
		until_ms INTEGER NOT NULL
	)`,
}

const (
	storedHitTTL  = 30 * 24 * time.Hour
	storedMissTTL = 24 * time.Hour
)

// stored reads a cached answer from the database. A row with no data is a remembered
// "no logo". Any database trouble is just a cache miss.
func (api *API) stored(ctx context.Context, key string) (cachedLogo, bool) {
	if api.DB == nil {
		return cachedLogo{}, false
	}
	var kind string
	var data []byte
	var untilMS int64
	err := api.DB.QueryRowContext(ctx, `SELECT kind, data, until_ms FROM app_logos WHERE key = ?`, key).Scan(&kind, &data, &untilMS)
	if err != nil || time.Now().UnixMilli() > untilMS {
		return cachedLogo{}, false
	}
	if len(data) == 0 {
		return cachedLogo{until: time.UnixMilli(untilMS)}, true
	}
	return cachedLogo{data: data, kind: kind, until: time.UnixMilli(untilMS)}, true
}

func (api *API) store(ctx context.Context, key string, logo cachedLogo) {
	if api.DB == nil {
		return
	}
	ttl := storedHitTTL
	if logo.data == nil {
		ttl = storedMissTTL
	}
	_, _ = api.DB.ExecContext(ctx,
		`INSERT INTO app_logos(key, kind, data, until_ms) VALUES (?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET kind = excluded.kind, data = excluded.data, until_ms = excluded.until_ms`,
		key, logo.kind, logo.data, time.Now().Add(ttl).UnixMilli())
}

type cachedLogo struct {
	data  []byte
	kind  string
	until time.Time
}

// logoCache keeps pictures (and the absence of one) in memory so a page of cards costs
// one lookup per app per day, not one per view.
type logoCache struct {
	mu      sync.Mutex
	entries map[string]cachedLogo
	// pausedUntil is, per source, when lookups resume after it failed or refused us.
	pausedUntil map[string]time.Time
	slots       chan struct{}
	once        sync.Once
}

// acquire takes a lookup slot, or says why not. Cached answers never come here.
func (l *logoCache) acquire(ctx context.Context, source string) (release func(), err error) {
	l.once.Do(func() { l.slots = make(chan struct{}, lookupSlots) })
	l.mu.Lock()
	paused := time.Now().Before(l.pausedUntil[source])
	l.mu.Unlock()
	if paused {
		return nil, errors.New("logo source is resting")
	}
	select {
	case l.slots <- struct{}{}:
		return func() { <-l.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *logoCache) rest(source string) {
	l.mu.Lock()
	if l.pausedUntil == nil {
		l.pausedUntil = map[string]time.Time{}
	}
	l.pausedUntil[source] = time.Now().Add(backoff)
	l.mu.Unlock()
}

func (l *logoCache) get(key string) (cachedLogo, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok || time.Now().After(e.until) {
		return cachedLogo{}, false
	}
	return e, true
}

func (l *logoCache) put(key string, e cachedLogo) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries == nil || len(l.entries) >= maxLogos {
		l.entries = map[string]cachedLogo{}
	}
	l.entries[key] = e
}

func (api *API) logo(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	id, name := query.Get("image"), query.Get("name")
	chart, repo := query.Get("chart"), query.Get("repo")
	parts := query["part"]
	if len(parts) > maxParts {
		parts = parts[:maxParts]
	}
	key := "image:" + id
	if id == "" {
		key = "name:" + strings.ToLower(name) + "|" + chart
	}
	if cached, ok := api.logos.get(key); ok {
		sendLogo(w, cached)
		return
	}
	if cached, ok := api.stored(r.Context(), key); ok {
		api.logos.put(key, cached)
		sendLogo(w, cached)
		return
	}
	cached, err := api.fetchLogo(r.Context(), id, name, parts, chart, repo)
	if err != nil {
		// Remember "none" briefly so a page of cards doesn't ask again on every view.
		// A source that is merely down is not remembered.
		if errors.Is(err, errNoLogo) {
			api.logos.put(key, cachedLogo{until: time.Now().Add(missTTL)})
			api.store(r.Context(), key, cachedLogo{})
			http.Error(w, "no logo", http.StatusNotFound)
			return
		}
		// 503 tells the page the answer is "not now", so it asks again later.
		http.Error(w, "logo source unavailable", http.StatusServiceUnavailable)
		return
	}
	api.logos.put(key, cached)
	api.store(r.Context(), key, cached)
	sendLogo(w, cached)
}

func (api *API) fetchLogo(ctx context.Context, id, name string, parts []string, chart, repo string) (cachedLogo, error) {
	client := api.client()
	found := func(data []byte, kind string) cachedLogo {
		return cachedLogo{data: data, kind: kind, until: time.Now().Add(logoTTL)}
	}
	if id != "" { // a known Artifact Hub image: nothing to search for
		data, kind, err := api.fromSource(ctx, "artifacthub", func() ([]byte, string, error) { return client.Logo(ctx, id) })
		if err != nil {
			return cachedLogo{}, err
		}
		return found(data, kind), nil
	}

	// Most exact first, each source only if the one before had nothing:
	//   1. the exact-name icon set (no rate limit, no look-alikes)
	//   2. the chart the app is deployed from, then a well-used chart of that name
	//   3. Docker Hub: the logo a verified, official or well-used image of that name has
	//   4. GitHub: the organisation behind a well-known project of that name
	if data, kind, err := client.Icon(ctx, name, parts); err == nil {
		return found(data, kind), nil
	}
	// A failure of a source (as opposed to "nothing there") must not be remembered as
	// "no logo", so it is kept and returned if nothing else turns one up.
	var failed error
	for _, source := range []struct {
		name string
		get  func() ([]byte, string, error)
	}{
		{"artifacthub", func() ([]byte, string, error) { return client.artifactHubLogo(ctx, name, chart, repo) }},
		{"dockerhub", func() ([]byte, string, error) { return client.DockerLogo(ctx, name) }},
		{"github", func() ([]byte, string, error) { return client.GitHubLogo(ctx, name) }},
	} {
		data, kind, err := api.fromSource(ctx, source.name, source.get)
		if err == nil {
			return found(data, kind), nil
		}
		if !errors.Is(err, errNoLogo) {
			failed = err
		}
	}
	if failed != nil {
		return cachedLogo{}, failed
	}
	return cachedLogo{}, errNoLogo
}

// fromSource runs one lookup under that source's concurrency limit and pause.
func (api *API) fromSource(ctx context.Context, source string, get func() ([]byte, string, error)) ([]byte, string, error) {
	release, err := api.logos.acquire(ctx, source)
	if err != nil {
		return nil, "", err
	}
	defer release()
	data, kind, err := get()
	if err != nil && !errors.Is(err, errNoLogo) {
		api.logos.rest(source)
	}
	return data, kind, err
}

// artifactHubLogo finds a logo through Artifact Hub: the app's own chart when it is
// known, else a well-used chart of that name.
func (c *Client) artifactHubLogo(ctx context.Context, name, chart, repo string) ([]byte, string, error) {
	id := ""
	if chart != "" {
		if found, err := c.LogoIDForChart(ctx, chart, repo); err == nil {
			id = found
		}
	}
	if id == "" {
		found, err := c.LogoIDFor(ctx, name)
		if err != nil {
			return nil, "", err
		}
		id = found
	}
	return c.Logo(ctx, id)
}

func sendLogo(w http.ResponseWriter, logo cachedLogo) {
	if logo.data == nil {
		http.Error(w, "no logo", http.StatusNotFound)
		return
	}
	header := w.Header()
	header.Set("Content-Type", logo.kind)
	header.Set("X-Content-Type-Options", "nosniff")
	// An SVG opened directly can run script; this stops it even then.
	header.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	header.Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(logo.data)
}
