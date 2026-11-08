package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/identity"
	"github.com/Adaetum/Adaetum/agent/internal/membership"
)

// These run the real auth service (bun) behind the real Go pieces. They skip if bun is
// not installed.

func TestSigningInThroughTheAgent(t *testing.T) {
	service := startAuth(t, "primary")
	mux, guard := agentMux("an-emergency-access-token-0123456789", service)
	owner := &browser{t: t, handler: mux}

	// Before anyone exists the page is told to offer setup; nothing else works.
	if body := owner.do("GET", "/api/auth-state", nil).Body.String(); !strings.Contains(body, `"needsSetup":true`) {
		t.Fatalf("auth-state: %s", body)
	}
	if code := owner.do("GET", "/api/v1/thing", nil).Code; code != 401 {
		t.Fatalf("an anonymous request: %d", code)
	}
	if code := owner.do("POST", "/api/setup", map[string]string{"code": "AAAA-BBBB-CCCC", "name": "X", "email": "x@example.test", "password": password}).Code; code != 403 {
		t.Fatalf("a wrong setup code: %d", code)
	}
	setUp(t, service, owner, "owner@example.test")
	if code := owner.do("POST", "/api/setup", map[string]string{"code": "AAAA-BBBB-CCCC", "name": "X", "email": "y@example.test", "password": password}).Code; code != 409 {
		t.Fatalf("setup after setup: %d", code)
	}

	if code := owner.signIn("owner@example.test", "wrong wrong wrong wrong"); code != 401 {
		t.Fatalf("a wrong password: %d", code)
	}
	if code := owner.signIn("owner@example.test", password); code != 200 {
		t.Fatalf("the right password: %d", code)
	}
	var me Identity
	response := owner.do("GET", "/api/v1/me", nil)
	if err := json.Unmarshal(response.Body.Bytes(), &me); err != nil || me.Role != "owner" || me.Email != "owner@example.test" || me.Token {
		t.Fatalf("who am I: %d %s", response.Code, response.Body.String())
	}

	// The owner adds an admin and a member through the page's API.
	for _, p := range []NewPerson{{"Ada", "admin@example.test", password, "admin"}, {"Sam", "member@example.test", password, "member"}} {
		if code := owner.do("POST", "/api/v1/people", p).Code; code != 201 {
			t.Fatalf("adding %s: %d", p.Email, code)
		}
	}
	list := owner.do("GET", "/api/v1/people", nil).Body.String()
	if !strings.Contains(list, "admin@example.test") || strings.Contains(list, "password") || strings.Contains(list, "scrypt") {
		t.Fatalf("the list must show people and never a password or hash: %s", list)
	}

	member := &browser{t: t, handler: mux}
	admin := &browser{t: t, handler: mux}
	if member.signIn("member@example.test", password) != 200 || admin.signIn("admin@example.test", password) != 200 {
		t.Fatal("the new people cannot sign in")
	}
	// A member can look but not change; an admin can change but not manage people; the
	// owner does both.
	if code := member.do("GET", "/api/v1/thing", nil).Code; code != 200 {
		t.Errorf("member reads: %d", code)
	}
	if code := member.do("POST", "/api/v1/thing", map[string]string{}).Code; code != 403 {
		t.Errorf("member changes: %d", code)
	}
	if code := admin.do("POST", "/api/v1/thing", map[string]string{}).Code; code != 200 {
		t.Errorf("admin changes: %d", code)
	}
	if code := admin.do("POST", "/api/v1/people", NewPerson{"Z", "z@example.test", password, "member"}).Code; code != 403 {
		t.Errorf("admin manages people: %d", code)
	}
	if code := member.do("POST", "/api/v1/people", NewPerson{"Z", "z@example.test", password, "owner"}).Code; code != 403 {
		t.Errorf("a member makes themselves an owner: %d", code)
	}

	// The auth service's refusals keep their meaning through the agent.
	if code := owner.do("POST", "/api/v1/people", NewPerson{"Dup", "MEMBER@example.test", password, "member"}).Code; code != 400 {
		t.Errorf("a duplicate email: %d", code)
	}
	var people struct{ People []Person }
	_ = json.Unmarshal([]byte(owner.do("GET", "/api/v1/people", nil).Body.String()), &people)
	var ownerID, memberID string
	for _, p := range people.People {
		switch p.Email {
		case "owner@example.test":
			ownerID = p.ID
		case "member@example.test":
			memberID = p.ID
		}
	}
	if code := owner.do("DELETE", "/api/v1/people/"+ownerID, nil).Code; code != 409 {
		t.Errorf("removing the last owner: %d", code)
	}

	// Removing someone ends their access within the guard's short memory.
	now := time.Now()
	guard.Now = func() time.Time { return now }
	if code := owner.do("DELETE", "/api/v1/people/"+memberID, nil).Code; code != 200 {
		t.Fatalf("removing a member: %d", code)
	}
	now = now.Add(positiveTTL + time.Second)
	if code := member.do("GET", "/api/v1/thing", nil).Code; code != 401 {
		t.Errorf("a removed person's old session: %d", code)
	}
}

func TestTheTokenStillWorksWhenTheAuthServiceIsGone(t *testing.T) {
	service := startAuth(t, "primary")
	mux, _ := agentMux("an-emergency-access-token-0123456789", service)
	owner := &browser{t: t, handler: mux}
	setUp(t, service, owner, "owner@example.test")
	owner.signIn("owner@example.test", password)

	// Kill the auth service (point the agent at a dead socket).
	dead := NewUnixClient("/tmp/does-not-exist.sock", "x")
	guard := &Guard{Token: "an-emergency-access-token-0123456789", Client: dead}
	deadMux := http.NewServeMux()
	RegisterPublic(deadMux, dead.PublicProxy())
	deadMux.Handle("/api/v1/thing", guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})))
	b := &browser{t: t, handler: deadMux, cookie: owner.cookie}

	if code := b.do("GET", "/api/v1/thing", nil).Code; code != 401 {
		t.Errorf("an account session with the auth service down: %d (it must not be trusted blindly)", code)
	}
	request := httptest.NewRequest("POST", "/api/v1/thing", nil)
	request.Header.Set("Authorization", "Bearer an-emergency-access-token-0123456789")
	recorder := httptest.NewRecorder()
	deadMux.ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Errorf("the access token with the auth service down: %d", recorder.Code)
	}
	if body := b.do("GET", "/api/auth-state", nil).Body.String(); !strings.Contains(body, `"unavailable":true`) {
		t.Errorf("the page should be told accounts are down: %s", body)
	}
}

// A cluster agent's account routes, served over TLS and checked against real host keys,
// exactly as hosts reach them.
type clusterSide struct {
	service *authService
	server  *httptest.Server
	key     *identity.HostKey
	link    membership.Link
}

func startCluster(t *testing.T, hostID string, key *identity.HostKey) *clusterSide {
	t.Helper()
	service := startAuth(t, "primary")
	verifier := &identity.Verifier{
		Now:       time.Now,
		PublicKey: func(id string) (string, bool) { return key.PublicKey(), id == hostID },
	}
	mux := http.NewServeMux()
	(&HostAPI{Local: service.Client, Verify: verifier.Verify}).Register(mux)
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	sum := sha256.Sum256(server.Certificate().Raw)
	return &clusterSide{service: service, server: server, key: key, link: membership.Link{URL: server.URL, Fingerprint: hex.EncodeToString(sum[:])}}
}

func (c *clusterSide) upstream(hostID string) *HTTPUpstream {
	return &HTTPUpstream{Link: c.link, HostID: hostID, Key: c.key}
}

func state(t *testing.T, c *Client) State {
	t.Helper()
	s, err := c.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func syncOnce(t *testing.T, host *authService, up Upstream) Outcome {
	t.Helper()
	out, err := SyncOnce(context.Background(), host.Client, up)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	return out
}

// TestTheRecoveryScenario is the milestone in docs/recovery-login.md, step by step.
func TestTheRecoveryScenario(t *testing.T) {
	key, err := identity.LoadOrCreateHostKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// 1. The application is installed on a host. Before any cluster exists, the first
	//    person signs up with the code shown on the machine and becomes the owner.
	host := startAuth(t, "primary")
	hostMux, _ := agentMux("an-emergency-access-token-0123456789", host)
	hostBrowser := &browser{t: t, handler: hostMux}
	setUp(t, host, hostBrowser, "owner@example.test")

	// 2. Kubernetes is provisioned and the cluster agent's pod comes up, empty. The host
	//    joins, becomes a replica, and offers its accounts: the pod starts from them
	//    (otherwise the owner made in step 1 would exist nowhere but here).
	pod := startCluster(t, "host-a", key)
	if got := syncOnce(t, host, pod.upstream("host-a")); got.Action != Push {
		t.Fatalf("seeding the pod: %v", got)
	}
	if err := host.Client.SetMode(context.Background(), "replica"); err != nil {
		t.Fatal(err)
	}
	if state(t, pod.service.Client).Epoch != state(t, host.Client).Epoch {
		t.Fatal("the pod and the host must share one account history")
	}
	podMux, _ := agentMux("another-emergency-access-token-9876543210", pod.service)
	podOwner := &browser{t: t, handler: podMux}
	if podOwner.signIn("owner@example.test", password) != 200 {
		t.Fatal("the owner cannot sign in at the pod")
	}

	// 3. People sign in at the pod, the normal front door. The owner adds a user there.
	if code := podOwner.do("POST", "/api/v1/people", NewPerson{"Sam", "sam@example.test", password, "admin"}).Code; code != 201 {
		t.Fatalf("adding Sam at the pod: %d", code)
	}
	// Within a few seconds the host has it.
	if got := syncOnce(t, host, pod.upstream("host-a")); got.Action != Pull {
		t.Fatalf("the host did not take the new account: %v", got)
	}

	// 4. Kubernetes breaks: the pod is gone. 5. People sign in at the HOST with the most
	//    recent copy, including the person added a moment ago.
	pod.server.Close()
	if _, err := SyncOnce(context.Background(), host.Client, pod.upstream("host-a")); err == nil {
		t.Fatal("syncing with a dead pod should report that the cluster cannot be reached")
	}
	sam := &browser{t: t, handler: hostMux}
	if sam.signIn("sam@example.test", password) != 200 {
		t.Fatal("Sam cannot sign in at the host while the cluster is down")
	}
	owner := &browser{t: t, handler: hostMux}
	if owner.signIn("owner@example.test", password) != 200 {
		t.Fatal("the owner cannot sign in at the host while the cluster is down")
	}
	// 6. An admin may make changes there (turn on "run commands"); a member could not.
	if code := sam.do("POST", "/api/v1/thing", map[string]string{}).Code; code != 200 {
		t.Fatalf("an admin at the host: %d", code)
	}
	// The host does not let anyone change the account list itself while it is a replica.
	if code := owner.do("POST", "/api/v1/people", NewPerson{"Z", "z@example.test", password, "member"}).Code; code != 409 {
		t.Fatalf("changing people at a replica: %d", code)
	}
}

func TestADeadPodsReplacementIsReseededFromAHost(t *testing.T) {
	key, _ := identity.LoadOrCreateHostKey(t.TempDir())
	host := startAuth(t, "primary")
	hostMux, _ := agentMux("an-emergency-access-token-0123456789", host)
	setUp(t, host, &browser{t: t, handler: hostMux}, "owner@example.test")
	first := startCluster(t, "host-a", key)
	syncOnce(t, host, first.upstream("host-a"))
	_ = host.Client.SetMode(context.Background(), "replica")
	first.server.Close() // the pod's volume is lost

	// A brand-new, empty pod appears. The host has the accounts: it gives them back.
	second := startCluster(t, "host-a", key)
	if got := syncOnce(t, host, second.upstream("host-a")); got.Action != Push {
		t.Fatalf("re-seeding: %v", got)
	}
	podMux, _ := agentMux("another-emergency-access-token-9876543210", second.service)
	if (&browser{t: t, handler: podMux}).signIn("owner@example.test", password) != 200 {
		t.Fatal("the owner cannot sign in at the rebuilt pod")
	}
	if got := syncOnce(t, host, second.upstream("host-a")); got.Action != Nothing {
		t.Fatalf("after re-seeding both agree: %v", got)
	}
}

func TestAPodRestoredFromAnOldBackupDoesNotRollTheHostBack(t *testing.T) {
	key, _ := identity.LoadOrCreateHostKey(t.TempDir())
	host := startAuth(t, "primary")
	hostMux, _ := agentMux("an-emergency-access-token-0123456789", host)
	setUp(t, host, &browser{t: t, handler: hostMux}, "owner@example.test")
	pod := startCluster(t, "host-a", key)
	syncOnce(t, host, pod.upstream("host-a"))
	_ = host.Client.SetMode(context.Background(), "replica")

	// A backup is taken, then Kim is added and reaches the host.
	backup, err := pod.service.Client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pod.service.Client.AddPerson(context.Background(), NewPerson{"Kim", "kim@example.test", password, "member"}); err != nil {
		t.Fatal(err)
	}
	syncOnce(t, host, pod.upstream("host-a"))
	hostBefore := state(t, host.Client)

	// The pod is replaced by one restored from the old backup.
	restored := startCluster(t, "host-a", key)
	if res, err := restored.service.Client.Apply(context.Background(), backup); err != nil || res.Status != "applied" {
		t.Fatalf("restoring the backup: %+v %v", res, err)
	}
	// The cluster is the master, so the host follows it back in time. Kim was added after
	// the backup and is gone: that is the accepted cost of "the cluster always wins".
	got := syncOnce(t, host, restored.upstream("host-a"))
	if got.Action != Replace {
		t.Fatalf("an older cluster still wins: %v", got)
	}
	if state(t, host.Client) == hostBefore {
		t.Fatal("the host kept its newer accounts")
	}
	if (&browser{t: t, handler: hostMux}).signIn("kim@example.test", password) == 200 {
		t.Fatal("Kim is still able to sign in after the cluster lost her")
	}
	if (&browser{t: t, handler: hostMux}).signIn("owner@example.test", password) != 200 {
		t.Fatal("the owner the backup holds cannot sign in")
	}
}

func TestAClusterSetUpOnItsOwnReplacesAJoiningHostsAccountsButNeverTheOtherWayRound(t *testing.T) {
	key, _ := identity.LoadOrCreateHostKey(t.TempDir())
	host := startAuth(t, "primary")
	hostMux, _ := agentMux("an-emergency-access-token-0123456789", host)
	setUp(t, host, &browser{t: t, handler: hostMux}, "host-owner@example.test")
	_ = host.Client.SetMode(context.Background(), "replica")

	// The pod was set up on its own: a different owner, a different history.
	pod := startCluster(t, "host-a", key)
	podMux, _ := agentMux("another-emergency-access-token-9876543210", pod.service)
	setUp(t, pod.service, &browser{t: t, handler: podMux}, "pod-owner@example.test")

	podBefore := state(t, pod.service.Client)
	if got := syncOnce(t, host, pod.upstream("host-a")); got.Action != Replace {
		t.Fatalf("two histories: %v", got)
	}
	// The host now holds the cluster's list, and the cluster is untouched.
	if state(t, pod.service.Client) != podBefore {
		t.Fatal("the host changed the cluster's accounts")
	}
	if state(t, host.Client).Epoch != podBefore.Epoch {
		t.Fatal("the host did not adopt the cluster's history")
	}
	if (&browser{t: t, handler: hostMux}).signIn("pod-owner@example.test", password) != 200 {
		t.Fatal("the cluster's owner cannot sign in on the host")
	}
	if (&browser{t: t, handler: hostMux}).signIn("host-owner@example.test", password) == 200 {
		t.Fatal("the host's own old owner still signs in")
	}
	if (&browser{t: t, handler: podMux}).signIn("pod-owner@example.test", password) != 200 {
		t.Fatal("the cluster's owner was locked out")
	}
}

func TestAHostCannotOverwriteAClusterThatHasPeople(t *testing.T) {
	key, _ := identity.LoadOrCreateHostKey(t.TempDir())
	pod := startCluster(t, "host-a", key)
	podMux, _ := agentMux("another-emergency-access-token-9876543210", pod.service)
	setUp(t, pod.service, &browser{t: t, handler: podMux}, "pod-owner@example.test")

	// A compromised host that holds a perfectly valid list tries to push it over the pod's.
	rogue := startAuth(t, "primary")
	rogueMux, _ := agentMux("an-emergency-access-token-0123456789", rogue)
	setUp(t, rogue, &browser{t: t, handler: rogueMux}, "rogue@example.test")
	snapshot, _ := rogue.Client.Snapshot(context.Background())
	before := state(t, pod.service.Client)
	result, err := pod.upstream("host-a").Seed(context.Background(), snapshot)
	if err != nil || result.Status != "rejected" || result.Reason != "primary_has_accounts" {
		t.Fatalf("the pod took a host's list: %+v %v", result, err)
	}
	if state(t, pod.service.Client) != before {
		t.Fatal("the pod's accounts changed")
	}
}

func TestOnlyAnEnrolledHostCanAskForTheAccounts(t *testing.T) {
	key, _ := identity.LoadOrCreateHostKey(t.TempDir())
	pod := startCluster(t, "host-a", key)
	podMux, _ := agentMux("another-emergency-access-token-9876543210", pod.service)
	setUp(t, pod.service, &browser{t: t, handler: podMux}, "pod-owner@example.test")

	// Unsigned request.
	client := pod.link.HTTPClient()
	response, err := client.Get(pod.server.URL + "/api/v1/accounts/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Errorf("an unsigned request for the accounts: %d", response.StatusCode)
	}
	// Signed by a key the cluster does not know.
	other, _ := identity.LoadOrCreateHostKey(t.TempDir())
	stranger := &HTTPUpstream{Link: pod.link, HostID: "host-a", Key: other}
	if _, err := stranger.Snapshot(context.Background()); err == nil {
		t.Error("a host with the wrong key got the accounts")
	}
	// An unenrolled host id.
	unknown := &HTTPUpstream{Link: pod.link, HostID: "host-zzz", Key: key}
	if _, err := unknown.State(context.Background()); err == nil {
		t.Error("an unknown host got the state")
	}
	// The real one works.
	if _, err := pod.upstream("host-a").Snapshot(context.Background()); err != nil {
		t.Errorf("the enrolled host was refused: %v", err)
	}
}

func TestAJoinedHostBecomesAReplicaAndKeepsItsCopyCurrent(t *testing.T) {
	key, _ := identity.LoadOrCreateHostKey(t.TempDir())
	pod := startCluster(t, "host-a", key)
	podMux, _ := agentMux("another-emergency-access-token-9876543210", pod.service)
	setUp(t, pod.service, &browser{t: t, handler: podMux}, "owner@example.test")

	// A brand-new host that has never been signed in to, and has no accounts.
	host := startAuth(t, "primary")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go RunReplica(ctx, host.Client, pod.upstream("host-a"), 100*time.Millisecond, nil)

	waitFor := func(what string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if condition() {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", what)
	}
	waitFor("the host to receive the owner", func() bool { return state(t, host.Client).Users == 1 })
	if state(t, host.Client).Mode != "replica" {
		t.Fatal("a host that joined a cluster must not own accounts")
	}

	// A change at the pod reaches the host within the sync interval.
	if _, err := pod.service.Client.AddPerson(ctx, NewPerson{"Sam", "sam@example.test", password, "member"}); err != nil {
		t.Fatal(err)
	}
	waitFor("the host to receive Sam", func() bool { return state(t, host.Client).Users == 2 })

	// The cluster goes away: the host keeps working from its last copy, and the loop survives.
	pod.server.Close()
	time.Sleep(400 * time.Millisecond)
	hostMux, _ := agentMux("an-emergency-access-token-0123456789", host)
	if (&browser{t: t, handler: hostMux}).signIn("sam@example.test", password) != 200 {
		t.Fatal("Sam cannot sign in at the host while the cluster is down")
	}
}
