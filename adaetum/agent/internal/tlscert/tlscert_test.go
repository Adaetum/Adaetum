package tlscert

import (
	"crypto/x509"
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
