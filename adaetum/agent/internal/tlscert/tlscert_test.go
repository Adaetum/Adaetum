package tlscert

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func manager(t *testing.T, id Identity, now *time.Time) *Manager {
	t.Helper()
	m := NewManager(filepath.Join(t.TempDir(), "tls"))
	m.Identity = func() Identity { return id }
	m.Now = func() time.Time { return *now }
	return m
}

var start = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func TestTheCertificateCoversEveryNameAndAddressAndVerifiesAgainstItself(t *testing.T) {
	now := start
	id := Identity{Names: []string{"node-a", "NODE-A.lan"}, IPs: []net.IP{net.ParseIP("192.168.2.233"), net.ParseIP("100.125.31.61")}}
	m := manager(t, id, &now)
	_, leaf, err := m.Current()
	if err != nil {
		t.Fatal(err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	for _, name := range []string{"node-a", "node-a.lan"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: name, CurrentTime: now}); err != nil {
			t.Errorf("name %s must verify: %v", name, err)
		}
	}
	for _, ip := range []string{"192.168.2.233", "100.125.31.61"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: ip, CurrentTime: now}); err != nil {
			t.Errorf("address %s must verify: %v", ip, err)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "other-host", CurrentTime: now}); err == nil {
		t.Error("a name the host does not answer to must not verify")
	}
}

func TestThePrivateKeyIsNeverReadableByOthers(t *testing.T) {
	now := start
	m := manager(t, Identity{Names: []string{"a"}}, &now)
	if _, _, err := m.Current(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(m.Dir, keyFile))
	if err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the key must be private to its owner: %v %v", info.Mode(), err)
	}
	dir, _ := os.Stat(m.Dir)
	if dir.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the directory must be private: %v", dir.Mode())
	}
}

func TestAnUnchangedHostKeepsItsCertificateAcrossRestarts(t *testing.T) {
	now := start
	m := manager(t, Identity{Names: []string{"a"}}, &now)
	_, first, _ := m.Current()

	restarted := &Manager{Dir: m.Dir, Identity: m.Identity, Now: m.Now}
	_, second, err := restarted.Current()
	if err != nil || Fingerprint(first) != Fingerprint(second) {
		t.Fatalf("the fingerprint operators pinned must not change on restart: %v", err)
	}
}

func TestANewAddressMakesANewCertificateButOnlyAfterTheRecheckInterval(t *testing.T) {
	now := start
	current := Identity{Names: []string{"a"}, IPs: []net.IP{net.ParseIP("192.168.2.10")}}
	m := manager(t, current, &now)
	m.Identity = func() Identity { return current }
	_, first, _ := m.Current()

	current = Identity{Names: []string{"a"}, IPs: []net.IP{net.ParseIP("192.168.2.10"), net.ParseIP("192.168.2.99")}}
	_, cached, _ := m.Current()
	if Fingerprint(cached) != Fingerprint(first) {
		t.Fatal("the address list must not be re-read on every connection")
	}

	now = now.Add(recheckEvery + time.Second)
	_, renewed, _ := m.Current()
	if Fingerprint(renewed) == Fingerprint(first) {
		t.Fatal("a host that gained an address must get a certificate that covers it")
	}
	if len(renewed.IPAddresses) != 2 {
		t.Fatalf("expected both addresses: %v", renewed.IPAddresses)
	}
}

func TestACertificateNearExpiryIsRenewed(t *testing.T) {
	now := start
	m := manager(t, Identity{Names: []string{"a"}}, &now)
	_, first, _ := m.Current()

	now = first.NotAfter.Add(-renewBefore + time.Hour)
	_, renewed, _ := m.Current()
	if Fingerprint(renewed) == Fingerprint(first) || !renewed.NotAfter.After(now.Add(renewBefore)) {
		t.Fatal("a certificate inside the renewal window must be replaced")
	}
}

func TestACorruptCertificateFileIsReplacedNotFatal(t *testing.T) {
	now := start
	m := manager(t, Identity{Names: []string{"a"}}, &now)
	if _, _, err := m.Current(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Dir, certFile), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	restarted := &Manager{Dir: m.Dir, Identity: m.Identity, Now: m.Now}
	if _, _, err := restarted.Current(); err != nil {
		t.Fatalf("a damaged file must be remade: %v", err)
	}
}

func TestAFailureToRenewKeepsServingTheCurrentCertificate(t *testing.T) {
	now := start
	id := Identity{Names: []string{"a"}}
	m := manager(t, id, &now)
	m.Identity = func() Identity { return id }
	_, first, _ := m.Current()

	// Make renewal impossible: the directory is replaced by a file.
	os.RemoveAll(m.Dir)
	os.WriteFile(m.Dir, []byte("x"), 0o600)
	id = Identity{Names: []string{"a", "b"}}
	now = now.Add(recheckEvery + time.Second)
	_, kept, err := m.Current()
	if err != nil || Fingerprint(kept) != Fingerprint(first) {
		t.Fatalf("an operator who can still connect must not be locked out by a failed renewal: %v", err)
	}
}

func TestAnEmptyIdentityIsRefused(t *testing.T) {
	now := start
	m := manager(t, Identity{}, &now)
	if _, _, err := m.Current(); err == nil {
		t.Fatal("a certificate for nothing is useless")
	}
}

func TestFingerprintFormat(t *testing.T) {
	now := start
	m := manager(t, Identity{Names: []string{"a"}}, &now)
	_, leaf, _ := m.Current()
	fingerprint := Fingerprint(leaf)
	if len(fingerprint) != 32*3-1 || strings.ToUpper(fingerprint) != fingerprint || strings.Count(fingerprint, ":") != 31 {
		t.Fatalf("unexpected fingerprint format: %s", fingerprint)
	}
}

func TestLocalIdentityAlwaysIncludesLoopback(t *testing.T) {
	id := LocalIdentity()
	found := false
	for _, ip := range id.IPs {
		if ip.IsLoopback() {
			found = true
		}
	}
	if !found || len(id.Names) == 0 {
		t.Fatalf("unexpected local identity: %+v", id)
	}
}

func TestRemakingACertificateKeepsTheKeySoPinsSurvive(t *testing.T) {
	now := start
	current := Identity{Names: []string{"a"}, IPs: []net.IP{net.ParseIP("192.168.2.10")}}
	m := manager(t, current, &now)
	m.Identity = func() Identity { return current }
	_, first, _ := m.Current()

	// New address: a different certificate, the same key.
	current = Identity{Names: []string{"a"}, IPs: []net.IP{net.ParseIP("192.168.2.10"), net.ParseIP("192.168.2.99")}}
	now = now.Add(recheckEvery + time.Second)
	_, second, _ := m.Current()
	if Fingerprint(second) == Fingerprint(first) {
		t.Fatal("the certificate must have been remade")
	}
	if KeyPin(second) != KeyPin(first) {
		t.Fatal("a remade certificate must keep the key, or every pin on it breaks")
	}

	// Renewal near expiry keeps it too.
	now = second.NotAfter.Add(-renewBefore + time.Hour)
	_, renewed, _ := m.Current()
	if KeyPin(renewed) != KeyPin(first) {
		t.Fatal("renewing must keep the key")
	}
}

func TestAnUnusableStoredKeyIsReplacedNotFatal(t *testing.T) {
	now := start
	m := manager(t, Identity{Names: []string{"a"}}, &now)
	_, first, _ := m.Current()
	if err := os.WriteFile(filepath.Join(m.Dir, keyFile), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted := &Manager{Dir: m.Dir, Identity: m.Identity, Now: m.Now}
	_, second, err := restarted.Current()
	if err != nil || KeyPin(second) == KeyPin(first) {
		t.Fatalf("a damaged key must be replaced with a new one: %v", err)
	}
}

func TestAPinNamesTheKeyOrTheWholeCertificateAndNothingElse(t *testing.T) {
	now := start
	m := manager(t, Identity{Names: []string{"a"}}, &now)
	_, leaf, _ := m.Current()
	_, other, _ := manager(t, Identity{Names: []string{"a"}}, &now).Current()

	wholeCert := strings.ToLower(strings.ReplaceAll(Fingerprint(leaf), ":", ""))
	for name, pin := range map[string]string{"key pin": KeyPin(leaf), "old whole-certificate pin": wholeCert} {
		if !PinMatches(pin, leaf.Raw) {
			t.Errorf("the %s must match its own certificate", name)
		}
		if PinMatches(pin, other.Raw) {
			t.Errorf("the %s must not match another agent's certificate", name)
		}
	}
	if PinMatches("", leaf.Raw) || PinMatches("abc", leaf.Raw) {
		t.Error("an empty or malformed pin must match nothing")
	}
}

// memorySource is a key source held in memory.
type memorySource struct {
	pem  []byte
	err  error
	puts int
}

func (s *memorySource) Get(context.Context) ([]byte, bool, error) {
	return s.pem, s.pem != nil, s.err
}
func (s *memorySource) Put(_ context.Context, p []byte) error {
	s.pem, s.puts = p, s.puts+1
	return nil
}

func TestAnEmptyKeySourceIsSeededWithTheKeyTheAgentAlreadyHas(t *testing.T) {
	now := start
	m := manager(t, Identity{Names: []string{"a"}}, &now)
	_, before, _ := m.Current()
	source := &memorySource{}
	m.Source = source

	changed, err := m.SyncKey(t.Context())
	if err != nil || changed || source.puts != 1 {
		t.Fatalf("changed=%v puts=%d err=%v", changed, source.puts, err)
	}
	// A second agent with no key of its own recovers the same one.
	fresh := manager(t, Identity{Names: []string{"a"}}, &now)
	fresh.Source = source
	if changed, err := fresh.SyncKey(t.Context()); err != nil || !changed {
		t.Fatalf("a new agent must adopt the stored key: changed=%v err=%v", changed, err)
	}
	_, after, _ := fresh.Current()
	if KeyPin(after) != KeyPin(before) {
		t.Fatal("a rebuilt agent must come back with the key every host pinned")
	}
}

func TestARotatedKeyInTheSourceIsAdoptedAndAnnounced(t *testing.T) {
	now := start
	m := manager(t, Identity{Names: []string{"a"}}, &now)
	_, first, _ := m.Current()
	other := manager(t, Identity{Names: []string{"a"}}, &now)
	other.Current()
	rotated, _ := os.ReadFile(filepath.Join(other.Dir, keyFile))

	var announced *x509.Certificate
	m.Source = &memorySource{pem: rotated}
	m.OnKeyChanged = func(c *x509.Certificate) { announced = c }
	changed, err := m.SyncKey(t.Context())
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	_, now2, _ := m.Current()
	if KeyPin(now2) == KeyPin(first) || announced == nil || KeyPin(announced) != KeyPin(now2) {
		t.Fatal("the certificate must carry the rotated key and the new pin must be announced")
	}
	// The key is on disk too, so a restart with OpenBao down keeps it.
	restarted := &Manager{Dir: m.Dir, Identity: m.Identity, Now: m.Now}
	_, again, _ := restarted.Current()
	if KeyPin(again) != KeyPin(now2) {
		t.Fatal("the adopted key must survive a restart without the source")
	}
	if changed, _ := m.SyncKey(t.Context()); changed {
		t.Fatal("an unchanged key must not be treated as a change")
	}
}

func TestAnUnreachableOrUnusableKeySourceChangesNothing(t *testing.T) {
	now := start
	m := manager(t, Identity{Names: []string{"a"}}, &now)
	_, first, _ := m.Current()

	m.Source = &memorySource{err: errors.New("sealed")}
	if changed, err := m.SyncKey(t.Context()); err == nil || changed {
		t.Fatalf("unreachable: changed=%v err=%v", changed, err)
	}
	m.Source = &memorySource{pem: []byte("not a key")}
	if changed, err := m.SyncKey(t.Context()); err == nil || changed {
		t.Fatalf("garbage: changed=%v err=%v", changed, err)
	}
	_, after, _ := m.Current()
	if KeyPin(after) != KeyPin(first) {
		t.Fatal("a source that cannot be used must never replace the working key")
	}
}
