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
	"context"
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

// KeyPin is what other agents pin: the SHA-256 of the certificate's public key, as lowercase
// hex. It is the key, not the certificate, so remaking the certificate (new addresses, nearing
// expiry) while keeping the key leaves every pin valid. See Manager.make.
func KeyPin(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

// PinMatches says whether a presented certificate (DER) is the one a pin names. A pin is
// either the key pin above or, for links made before key pins existed, the SHA-256 of the
// whole certificate. Both are accepted so an upgrade never cuts a host off; the two are
// hashes of different data, so one pin cannot be mistaken for the other.
func PinMatches(pin string, der []byte) bool {
	if pin == "" {
		return false
	}
	whole := sha256.Sum256(der)
	if hex.EncodeToString(whole[:]) == pin {
		return true
	}
	cert, err := x509.ParseCertificate(der)
	return err == nil && KeyPin(cert) == pin
}

// KeySource is a second home for the private key (OpenBao in the cluster agent), so the key
// outlives the volume the certificate is kept on and can be rotated in one place.
type KeySource interface {
	// Get returns the stored key as PEM; found is false when the source is reachable but
	// holds none yet. An error means "could not tell", never "there is none".
	Get(ctx context.Context) (pem []byte, found bool, err error)
	Put(ctx context.Context, pem []byte) error
}

// Manager keeps a certificate current for the identity it is asked for.
type Manager struct {
	Dir      string
	Identity func() Identity
	Now      func() time.Time
	// Source, when set, makes the source's key the key (see SyncKey). The agent works
	// without it, and without reaching it: the key on disk is always a complete fallback.
	Source KeySource
	// OnKeyChanged is told, after SyncKey has switched to a different key and remade the
	// certificate, so the agent can announce the new pin to its hosts.
	OnKeyChanged func(*x509.Certificate)

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
	// Keep the key the agent already has. Other agents pin the key (KeyPin), so a certificate
	// remade for new addresses must carry the same one; a new key is made only the first time,
	// or when the stored one is unusable.
	key := m.existingKey()
	if key == nil {
		var err error
		if key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
			return nil, nil, err
		}
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

// SyncKey makes Source and the key on disk agree, with Source winning:
//
//   - Source holds a key that is not the one on disk: adopt it (rotation, or recovery after
//     the volume was lost), write it to disk as the offline fallback, and remake the
//     certificate with it. OnKeyChanged then fires.
//   - Source holds none: it is seeded with the key this agent already has (made first if need
//     be), so the first agent to run defines the key and later ones recover it.
//   - Source cannot be reached: nothing changes. This is normal while OpenBao is not set up yet.
//
// Network calls happen here, never while a client is connecting, so a slow OpenBao cannot
// stall a handshake.
func (m *Manager) SyncKey(ctx context.Context) (changed bool, err error) {
	if m.Source == nil {
		return false, nil
	}
	remote, found, err := m.Source.Get(ctx)
	if err != nil {
		return false, err
	}
	if !found {
		if _, _, err := m.Current(); err != nil { // makes a key if there is none yet
			return false, err
		}
		local := m.existingKey()
		if local == nil {
			return false, errors.New("there is no key to store")
		}
		pemBytes, err := encodeKey(local)
		if err != nil {
			return false, err
		}
		return false, m.Source.Put(ctx, pemBytes)
	}

	block, _ := pem.Decode(remote)
	var key *ecdsa.PrivateKey
	if block != nil {
		key, _ = x509.ParseECPrivateKey(block.Bytes)
	}
	if key == nil || key.Curve != elliptic.P256() {
		return false, errors.New("the key held in the key source is not a usable P-256 key, so it was ignored")
	}

	m.mu.Lock()
	local := m.existingKey()
	if local != nil && local.Equal(key) {
		m.mu.Unlock()
		return false, nil
	}
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		m.mu.Unlock()
		return false, err
	}
	pemBytes, _ := encodeKey(key)
	if err := writeFile(filepath.Join(m.Dir, keyFile), pemBytes, 0o600); err != nil {
		m.mu.Unlock()
		return false, err
	}
	// The certificate on disk no longer matches the key, so the next Current remakes it.
	m.checkedAt = time.Time{}
	m.mu.Unlock()

	_, leaf, err := m.Current()
	if err != nil {
		return true, err
	}
	if m.OnKeyChanged != nil {
		m.OnKeyChanged(leaf)
	}
	return true, nil
}

func encodeKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

// existingKey returns the stored private key, or nil if there is none or it cannot be used.
func (m *Manager) existingKey() *ecdsa.PrivateKey {
	data, err := os.ReadFile(filepath.Join(m.Dir, keyFile))
	if err != nil {
		return nil
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil || key.Curve != elliptic.P256() {
		return nil
	}
	return key
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
