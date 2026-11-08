// Package tlscert gives the agent a TLS certificate it makes for itself.
//
// A host agent must be reachable directly, by IP address or hostname, when the
// cluster and every shared service are down, so it cannot depend on an outside
// certificate authority. It makes a self-signed certificate instead. That gives
// encryption on the LAN, and a fingerprint an operator can check on first visit
// (trust on first use); it does not prove identity to a stranger.
//
// The certificate lists every name and address the host currently answers to. A
// host's addresses change (DHCP, Tailscale coming up), so the certificate is
// checked again as the host runs and remade when the list changes or it nears
// expiry.
package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	certFile = "agent.crt"
	keyFile  = "agent.key"
	// A host is not a public website; a long life avoids a certificate quietly
	// expiring on a machine nobody looks at. It is still remade well before then.
	lifetime    = 5 * 365 * 24 * time.Hour
	renewBefore = 30 * 24 * time.Hour
	// How often the host's addresses are looked at again while serving.
	recheckEvery = 5 * time.Minute
)

// Identity is what the certificate must cover.
type Identity struct {
	Names []string
	IPs   []net.IP
}

// LocalIdentity gathers this machine's names and every address on every
// interface, plus loopback.
func LocalIdentity() Identity {
	names := []string{"localhost"}
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		names = append(names, hostname)
		if short, _, found := strings.Cut(hostname, "."); found && short != "" {
			names = append(names, short)
		}
	}
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			if network, ok := addr.(*net.IPNet); ok && !network.IP.IsLinkLocalUnicast() {
				ips = append(ips, network.IP)
			}
		}
	}
	return normalize(Identity{Names: names, IPs: ips})
}

// normalize sorts and removes duplicates so two identities compare equal when
// they cover the same names and addresses.
func normalize(id Identity) Identity {
	seenName := map[string]bool{}
	var names []string
	for _, name := range id.Names {
		name = strings.ToLower(name)
		if name != "" && !seenName[name] {
			seenName[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)

	seenIP := map[string]bool{}
	var ips []net.IP
	for _, ip := range id.IPs {
		if ip != nil && !seenIP[ip.String()] {
			seenIP[ip.String()] = true
			ips = append(ips, ip)
		}
	}
	sort.Slice(ips, func(a, b int) bool { return ips[a].String() < ips[b].String() })
	return Identity{Names: names, IPs: ips}
}

func (id Identity) covers(cert *x509.Certificate) bool {
	have := normalize(Identity{Names: cert.DNSNames, IPs: cert.IPAddresses})
	if len(have.Names) != len(id.Names) || len(have.IPs) != len(id.IPs) {
		return false
	}
	for index := range id.Names {
		if have.Names[index] != id.Names[index] {
			return false
		}
	}
	for index := range id.IPs {
		if !have.IPs[index].Equal(id.IPs[index]) {
			return false
		}
	}
	return true
}

// Fingerprint is the SHA-256 of the certificate, written as colon-separated hex,
// the form browsers and `openssl x509 -fingerprint -sha256` show.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	text := strings.ToUpper(hex.EncodeToString(sum[:]))
	parts := make([]string, 0, len(sum))
	for index := 0; index < len(text); index += 2 {
		parts = append(parts, text[index:index+2])
	}
	return strings.Join(parts, ":")
}

// Manager keeps a certificate current for the identity it is asked for.
type Manager struct {
	Dir      string
	Identity func() Identity
	Now      func() time.Time

	mu        sync.Mutex
	current   *tls.Certificate
	leaf      *x509.Certificate
	checkedAt time.Time
}

// NewManager returns a Manager that stores its certificate in dir.
func NewManager(dir string) *Manager {
	return &Manager{Dir: dir, Identity: LocalIdentity, Now: time.Now}
}

// GetCertificate is a tls.Config callback. It never fails a connection because the
// address list could not be refreshed: it keeps serving the certificate it has.
func (m *Manager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	certificate, _, err := m.Current()
	return certificate, err
}

// Current returns the certificate to serve and its parsed form, making or
// renewing it when needed.
func (m *Manager) Current() (*tls.Certificate, *x509.Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.Now()
	if m.current != nil && now.Sub(m.checkedAt) < recheckEvery {
		return m.current, m.leaf, nil
	}
	want := normalize(m.Identity())

	certificate, leaf, err := m.load()
	if err == nil && want.covers(leaf) && now.Add(renewBefore).Before(leaf.NotAfter) {
		m.current, m.leaf, m.checkedAt = certificate, leaf, now
		return certificate, leaf, nil
	}
	certificate, leaf, makeErr := m.make(want, now)
	if makeErr != nil {
		// Keep serving what we have rather than dropping the only way in.
		if m.current != nil {
			m.checkedAt = now
			return m.current, m.leaf, nil
		}
		return nil, nil, makeErr
	}
	m.current, m.leaf, m.checkedAt = certificate, leaf, now
	return certificate, leaf, nil
}

func (m *Manager) load() (*tls.Certificate, *x509.Certificate, error) {
	certificate, err := tls.LoadX509KeyPair(filepath.Join(m.Dir, certFile), filepath.Join(m.Dir, keyFile))
	if err != nil {
		return nil, nil, err
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, nil, err
	}
	return &certificate, leaf, nil
}

func (m *Manager) make(id Identity, now time.Time) (*tls.Certificate, *x509.Certificate, error) {
	if len(id.Names) == 0 && len(id.IPs) == 0 {
		return nil, nil, errors.New("this host has no name or address to put in a certificate")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return nil, nil, err
	}
	hostname, _ := os.Hostname()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: hostname, Organization: []string{"Adaetum host agent"}},
		// Start a little in the past so a host whose clock is slightly behind a
		// client's still passes validity checks.
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(lifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              id.Names,
		IPAddresses:           id.IPs,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}

	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("creating %s: %w", m.Dir, err)
	}
	// The key is written first and with no group or world access from the start,
	// so there is never a moment when it is readable by others.
	if err := writeFile(filepath.Join(m.Dir, keyFile), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, nil, err
	}
	if err := writeFile(filepath.Join(m.Dir, certFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return nil, nil, err
	}
	return m.load()
}

// writeFile replaces path atomically so a crash cannot leave half a file.
func writeFile(path string, data []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
