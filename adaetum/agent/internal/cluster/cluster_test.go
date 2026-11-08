package cluster

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/identity"
	"github.com/Adaetum/Adaetum/agent/internal/membership"
	"github.com/Adaetum/Adaetum/agent/internal/policy"
	"github.com/Adaetum/Adaetum/agent/internal/store"
)

var clock = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

const userToken = "user-token-user-token-user-token-00"

type harness struct {
	t        *testing.T
	registry *Registry
	server   *httptest.Server
	nodes    []string
	nodesErr error
	now      time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "cluster.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { registry.Close() })

	h := &harness{t: t, registry: registry, nodes: []string{"node-50449", "node-56843"}, now: clock}
	registry.Now = func() time.Time { return h.now }
	api := &API{
		Registry: registry,
		Nodes:    func(context.Context) ([]string, error) { return h.nodes, h.nodesErr },
		RequireUser: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+userToken {
					http.Error(w, "login required", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r)
			})
		},
		Now: func() time.Time { return h.now },
	}
	h.server = httptest.NewServer(api.Handler())
	t.Cleanup(h.server.Close)
	return h
}

// agent is a host agent as the cluster sees it.
type agent struct {
	h        *harness
	key      *identity.HostKey
	id       string
	hostname string
}

func (h *harness) newAgent(hostname string) *agent {
	key, err := identity.LoadOrCreateHostKey(filepath.Join(h.t.TempDir(), "id"))
	if err != nil {
		h.t.Fatal(err)
	}
	return &agent{h: h, key: key, id: randomID(h.t), hostname: hostname}
}

// randomID makes a unique 32-character host ID, as the real store does, so tests
// cannot pass because two agents accidentally share one.
func randomID(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw)
}

func (h *harness) post(path string, body any, header map[string]string) (int, map[string]any) {
	h.t.Helper()
	data, _ := json.Marshal(body)
	request, _ := http.NewRequest("POST", h.server.URL+path, bytes.NewReader(data))
	for k, v := range header {
		request.Header.Set(k, v)
	}
	return h.do(request)
}

func (h *harness) do(request *http.Request) (int, map[string]any) {
	h.t.Helper()
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	return response.StatusCode, decoded
}

func (h *harness) mint() string {
	h.t.Helper()
	status, body := h.post("/api/v1/join-tokens", map[string]any{"note": "test"}, map[string]string{"Authorization": "Bearer " + userToken})
	if status != 200 {
		h.t.Fatalf("minting a token failed: %d %v", status, body)
	}
	return body["secret"].(string)
}

func (a *agent) enroll(token string) (int, map[string]any) {
	return a.h.post("/api/v1/enroll", membership.EnrollRequest{HostID: a.id, Hostname: a.hostname, PublicKey: a.key.PublicKey(), JoinToken: token}, nil)
}

func (a *agent) send(events []store.Event) (int, map[string]any) {
	a.h.t.Helper()
	data, _ := json.Marshal(map[string]any{"events": events})
	request, _ := http.NewRequest("POST", a.h.server.URL+"/api/v1/events", bytes.NewReader(data))
	if err := a.key.Sign(request, a.id, data, a.h.now); err != nil {
		a.h.t.Fatal(err)
	}
	return a.h.do(request)
}

func (a *agent) event(seq int64, kind string, payload any) store.Event {
	data, _ := json.Marshal(payload)
	return store.Event{HostID: a.id, Seq: seq, Kind: kind, At: a.h.now, Payload: data}
}

func TestAKubernetesNodeWithAValidTokenIsApprovedAtOnce(t *testing.T) {
	h := newHarness(t)
	a := h.newAgent("node-50449")
	status, body := a.enroll(h.mint())
	if status != 200 || body["status"] != "approved" {
		t.Fatalf("expected approval: %d %v", status, body)
	}
}

func TestANewHostWaitsForAPersonThenMaySpeak(t *testing.T) {
	h := newHarness(t)
	a := h.newAgent("brand-new-box")
	status, body := a.enroll(h.mint())
	if status != 200 || body["status"] != "pending" {
		t.Fatalf("expected pending: %d %v", status, body)
	}
	if status, _ := a.send([]store.Event{a.event(1, membership.CheckInKind, nil)}); status != http.StatusUnauthorized {
		t.Fatalf("a pending host must not be able to send events: %d", status)
	}

	user := map[string]string{"Authorization": "Bearer " + userToken}
	if status, _ := h.post("/api/v1/hosts/"+a.id+"/approve", nil, user); status != 200 {
		t.Fatalf("approval failed: %d", status)
	}
	if status, body := a.send([]store.Event{a.event(1, membership.CheckInKind, nil)}); status != 200 || body["acked"].(float64) != 1 {
		t.Fatalf("an approved host must be able to send: %d %v", status, body)
	}
}

func TestEnrollmentWithoutAValidTokenIsRefusedAndLeavesNothingBehind(t *testing.T) {
	h := newHarness(t)
	a := h.newAgent("node-50449")
	for _, token := range []string{"", "adj_wrong", "adj_" + strings.Repeat("0", 64)} {
		if status, _ := a.enroll(token); status != http.StatusForbidden {
			t.Errorf("token %q must be refused: %d", token, status)
		}
	}
	hosts, _ := h.registry.Hosts(context.Background())
	if len(hosts) != 0 {
		t.Fatalf("a refused enrollment must not register a host: %+v", hosts)
	}
}

func TestATokenWorksOnceAndExpires(t *testing.T) {
	h := newHarness(t)
	token := h.mint()
	if status, _ := h.newAgent("node-50449").enroll(token); status != 200 {
		t.Fatal("first use must work")
	}
	if status, _ := h.newAgent("node-56843").enroll(token); status != http.StatusForbidden {
		t.Fatal("a token must not enroll a second host")
	}

	expiring := h.mint()
	h.now = h.now.Add(2 * time.Hour)
	if status, _ := h.newAgent("another").enroll(expiring); status != http.StatusForbidden {
		t.Fatal("an expired token must be refused")
	}
}

func TestTheSameHostAskingAgainGetsItsStatusWithoutANewToken(t *testing.T) {
	h := newHarness(t)
	a := h.newAgent("node-50449")
	a.enroll(h.mint())
	status, body := a.enroll("")
	if status != 200 || body["status"] != "approved" {
		t.Fatalf("a lost reply must be recoverable: %d %v", status, body)
	}
}

func TestAnotherKeyCannotTakeOverAnEnrolledHostID(t *testing.T) {
	h := newHarness(t)
	a := h.newAgent("node-50449")
	a.enroll(h.mint())
	attacker := h.newAgent("node-50449")
	attacker.id = a.id
	if status, _ := attacker.enroll(h.mint()); status != http.StatusForbidden {
		t.Fatalf("a different key claiming an enrolled host ID must be refused: %d", status)
	}
}

func TestAHostCanOnlyWriteItsOwnEvents(t *testing.T) {
	h := newHarness(t)
	a := h.newAgent("node-50449")
	a.enroll(h.mint())
	forged := a.event(1, "agent.checkin", nil)
	forged.HostID = strings.Repeat("z", 32)
	if status, _ := a.send([]store.Event{forged}); status != http.StatusBadRequest {
		t.Fatalf("an event naming another host must be refused: %d", status)
	}
	hosts, _ := h.registry.Hosts(context.Background())
	if hosts[0].AckedSeq != 0 {
		t.Fatal("nothing from a refused batch may be stored")
	}
}

func TestEventsAreStoredOnceAcknowledgedInOrderAndRepeatsAreHarmless(t *testing.T) {
	h := newHarness(t)
	a := h.newAgent("node-50449")
	a.enroll(h.mint())

	batch := []store.Event{a.event(1, "k", 1), a.event(2, "k", 2)}
	_, first := a.send(batch)
	_, repeat := a.send(batch)
	if first["acked"].(float64) != 2 || repeat["acked"].(float64) != 2 {
		t.Fatalf("a retry must give the same answer: %v %v", first, repeat)
	}

	// Events 4 and 5 arrive before 3: the cursor must not jump the gap.
	_, gap := a.send([]store.Event{a.event(4, "k", 4), a.event(5, "k", 5)})
	if gap["acked"].(float64) != 2 {
		t.Fatalf("the cursor must stop at the gap: %v", gap)
	}
	_, filled := a.send([]store.Event{a.event(3, "k", 3)})
	if filled["acked"].(float64) != 5 {
		t.Fatalf("filling the gap must advance past the held events: %v", filled)
	}
}

func TestMalformedEventsAreRefused(t *testing.T) {
	h := newHarness(t)
	a := h.newAgent("node-50449")
	a.enroll(h.mint())
	bad := map[string]store.Event{
		"no kind":        a.event(1, "", nil),
		"seq zero":       a.event(0, "k", nil),
		"huge kind":      a.event(1, strings.Repeat("k", 65), nil),
		"huge payload":   a.event(1, "k", strings.Repeat("x", maxPayloadBytes)),
		"payload is not": {HostID: a.id, Seq: 1, Kind: "k", Payload: json.RawMessage("{nope")},
	}
	for name, event := range bad {
		if status, _ := a.send([]store.Event{event}); status == http.StatusOK {
			t.Errorf("%s must be refused", name)
		}
	}
}

func TestEveryEventsRequestNeedsAValidSignature(t *testing.T) {
	h := newHarness(t)
	a := h.newAgent("node-50449")
	a.enroll(h.mint())
	if status, _ := h.post("/api/v1/events", map[string]any{"events": []any{}}, nil); status != http.StatusUnauthorized {
		t.Fatalf("an unsigned request must be refused: %d", status)
	}
	impostor := h.newAgent("impostor")
	impostor.id = a.id
	if status, _ := impostor.send(nil); status != http.StatusUnauthorized {
		t.Fatalf("another key signing as this host must be refused: %d", status)
	}
}

func TestUserEndpointsNeedALogin(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"/api/v1/join-tokens", "/api/v1/hosts/x/approve"} {
		if status, _ := h.post(path, nil, nil); status != http.StatusUnauthorized {
			t.Errorf("%s must need a login: %d", path, status)
		}
	}
	request, _ := http.NewRequest("GET", h.server.URL+"/api/v1/cluster", nil)
	if status, _ := h.do(request); status != http.StatusUnauthorized {
		t.Errorf("the cluster view must need a login: %d", status)
	}
}

func clusterView(h *harness) ClusterView {
	h.t.Helper()
	request, _ := http.NewRequest("GET", h.server.URL+"/api/v1/cluster", nil)
	request.Header.Set("Authorization", "Bearer "+userToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	var view ClusterView
	if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
		h.t.Fatal(err)
	}
	return view
}

func TestTheClusterViewShowsWhichServersAreNotRunningTheAgent(t *testing.T) {
	h := newHarness(t)
	a := h.newAgent("node-50449")
	a.enroll(h.mint())
	a.send([]store.Event{a.event(1, membership.CheckInKind, membership.CheckInPayload{Hostname: "node-50449", Version: "0.1.0"})})

	view := clusterView(h)
	status := map[string]membership.Status{}
	for _, server := range view.Servers {
		status[server.Server] = server.Status
	}
	if status["node-50449"] != membership.StatusOK || status["node-56843"] != membership.StatusMissing {
		t.Fatalf("expected node-50449 ok and node-56843 missing: %v", status)
	}

	if view.Install.Limit != "node-56843" || len(view.Install.Actions) != 1 || view.Install.Actions[0].Kind != membership.InstallKind {
		t.Fatalf("the plan must offer to install on the server with no agent: %+v", view.Install)
	}

	h.now = h.now.Add(time.Hour)
	if got := clusterView(h).Servers[0].Status; got != membership.StatusStale {
		t.Fatalf("an hour of silence is stale: %v", got)
	}
}

func TestAnUnreadableKubernetesIsReportedNotMistakenForEveryServerMissing(t *testing.T) {
	h := newHarness(t)
	h.nodesErr = errors.New("connection refused")
	view := clusterView(h)
	if view.NodesError == "" || len(view.Servers) != 0 {
		t.Fatalf("expected an error and no server verdicts: %+v", view)
	}
}

func TestPendingHostsAreListedSeparatelyFromTheCluster(t *testing.T) {
	h := newHarness(t)
	h.newAgent("brand-new-box").enroll(h.mint())
	view := clusterView(h)
	if len(view.Pending) != 1 || len(view.Hosts) != 0 {
		t.Fatalf("a pending host awaits approval: %+v", view)
	}
}

func TestUnreadableKubernetesNeverAutoApproves(t *testing.T) {
	h := newHarness(t)
	h.nodesErr = errors.New("down")
	if _, body := h.newAgent("node-50449").enroll(h.mint()); body["status"] != "pending" {
		t.Fatalf("when Kubernetes cannot confirm a node, a person decides: %v", body)
	}
}

func TestTwoEnrollmentsRacingForOneTokenYieldOneWinner(t *testing.T) {
	h := newHarness(t)
	token := h.mint()
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for _, name := range []string{"node-50449", "node-56843", "other-a", "other-b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if status, _ := h.newAgent(name).enroll(token); status == 200 {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("a single-use token enrolled %d hosts", wins)
	}
}

func TestJoinTokenLifetimeIsBounded(t *testing.T) {
	h := newHarness(t)
	for _, minutes := range []int{-5, 60 * 24 * 8} {
		status, _ := h.post("/api/v1/join-tokens", map[string]any{"ttlMinutes": minutes}, map[string]string{"Authorization": "Bearer " + userToken})
		if status == 200 {
			t.Errorf("a %d-minute token must be refused", minutes)
		}
	}
}

func TestSecuritySettingsSurviveARestartAndKeepTheirTimeLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cluster.db")
	first, err := OpenRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	until := clock.Add(time.Hour)
	if err := first.SaveSetting(policy.Setting{Key: "host_ssh", Enabled: true, Until: &until, ChangedBy: "operator", ChangedAt: clock}); err != nil {
		t.Fatal(err)
	}
	if err := first.SaveSetting(policy.Setting{Key: "ansible_launcher", Enabled: false, ChangedBy: "operator", ChangedAt: clock}); err != nil {
		t.Fatal(err)
	}
	first.Close()

	second, err := OpenRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	got, found, err := second.Setting("host_ssh")
	if err != nil || !found || !got.Enabled || got.Until == nil || !got.Until.Equal(until) || got.ChangedBy != "operator" {
		t.Fatalf("a setting and its time limit must survive a restart: %+v %v %v", got, found, err)
	}
	if off, _, _ := second.Setting("ansible_launcher"); off.Enabled || off.Until != nil {
		t.Fatalf("an off setting has no time limit: %+v", off)
	}
	if _, found, _ := second.Setting("never-set"); found {
		t.Fatal("an unset key must report not found, so the startup flag decides")
	}
	// Saving again replaces, not duplicates.
	second.SaveSetting(policy.Setting{Key: "host_ssh", Enabled: false, ChangedBy: "x", ChangedAt: clock})
	if again, _, _ := second.Setting("host_ssh"); again.Enabled || again.Until != nil {
		t.Fatalf("the latest setting wins: %+v", again)
	}
}
