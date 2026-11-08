// Package identity is how Adaetum agents prove who they are to each other.
//
// There are three separate trust boundaries, and they use different mechanisms on
// purpose so that one leaking does not open the others (the full picture is in
// docs/auth.md):
//
//  1. USER to agent (host or cluster): a person with an access token. Not here;
//     see cmd/adaetum-agent/exposure.go.
//  2. MACHINE to machine: a host agent proves to the cluster agent that it is a
//     particular enrolled host. This package: each host has its own Ed25519 key
//     pair and signs every request; the cluster holds only the public half, so
//     nothing the cluster stores can be used to impersonate a host.
//  3. ENROLLMENT: how a host becomes known in the first place. This package: an
//     operator mints a short-lived, single-use join token; the host presents it
//     once together with its public key; whether the host is then trusted at once
//     or waits for a person is decided by Decide.
//
// Names are not credentials. A hostname is only ever used to recognise a machine
// the cluster already trusts (Kubernetes lists it as a node); it never proves
// anything by itself, because anyone on the network can claim any name.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Request headers that carry a host's signature.
const (
	HeaderHost      = "X-Adaetum-Host"
	HeaderTimestamp = "X-Adaetum-Timestamp"
	HeaderNonce     = "X-Adaetum-Nonce"
	HeaderSignature = "X-Adaetum-Signature"

	// signatureVersion is part of what is signed, so a future change to the format
	// cannot be confused with this one.
	signatureVersion = "adaetum-v1"
	// MaxClockSkew is how far a request's timestamp may be from the verifier's
	// clock. Wide enough for hosts that have not synced time yet, narrow enough
	// that a captured request is useless soon after.
	MaxClockSkew = 5 * time.Minute

	keyFileName = "host.key"
)

// HostKey is a host's private signing key. It never leaves the host.
type HostKey struct {
	private ed25519.PrivateKey
}

// LoadOrCreateHostKey reads the host's key from dir, making one on first use. The
// key is created readable only by its owner and is never printed or logged.
func LoadOrCreateHostKey(dir string) (*HostKey, error) {
	path := filepath.Join(dir, keyFileName)
	data, err := os.ReadFile(path)
	if err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("%s is not a valid key file", path)
		}
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		private, ok := parsed.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%s is not an Ed25519 key", path)
		}
		return &HostKey{private: private}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// O_EXCL: if two processes race to create the first key, the loser fails
	// instead of silently replacing the identity the winner already announced.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := pem.Encode(file, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		return nil, err
	}
	return &HostKey{private: private}, nil
}

// PublicKey is the half of the key the cluster stores, as base64.
func (k *HostKey) PublicKey() string {
	return base64.StdEncoding.EncodeToString(k.private.Public().(ed25519.PublicKey))
}

// Sign adds the headers that prove a request came from the host with this key.
// The signature covers the method, path, body, a timestamp and a one-time nonce, so
// a captured request cannot be changed, redirected to another endpoint, or replayed.
func (k *HostKey) Sign(r *http.Request, hostID string, body []byte, now time.Time) error {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	nonceText := hex.EncodeToString(nonce)
	message := canonical(r.Method, r.URL.Path, timestamp, nonceText, hostID, body)

	r.Header.Set(HeaderHost, hostID)
	r.Header.Set(HeaderTimestamp, timestamp)
	r.Header.Set(HeaderNonce, nonceText)
	r.Header.Set(HeaderSignature, base64.StdEncoding.EncodeToString(ed25519.Sign(k.private, message)))
	return nil
}

func canonical(method, path, timestamp, nonce, hostID string, body []byte) []byte {
	sum := sha256.Sum256(body)
	return []byte(strings.Join([]string{signatureVersion, method, path, timestamp, nonce, hostID, hex.EncodeToString(sum[:])}, "\n"))
}

// Verifier checks signed requests. It remembers recent nonces so a request cannot
// be replayed inside the clock-skew window.
type Verifier struct {
	// PublicKey returns the public key enrolled for a host ID, as base64, and
	// whether that host is allowed to speak (approved, not merely pending).
	PublicKey func(hostID string) (key string, allowed bool)
	Now       func() time.Time

	mu     sync.Mutex
	nonces map[string]time.Time
}

// maxNonces bounds memory if something floods the endpoint with signed requests.
const maxNonces = 100_000

// Verify returns the authenticated host ID, or an error that never says which
// check failed in a way that helps a guesser (callers should answer 401 for all).
func (v *Verifier) Verify(r *http.Request, body []byte) (hostID string, err error) {
	now := v.Now()
	hostID = r.Header.Get(HeaderHost)
	timestamp := r.Header.Get(HeaderTimestamp)
	nonce := r.Header.Get(HeaderNonce)
	signature, decodeErr := base64.StdEncoding.DecodeString(r.Header.Get(HeaderSignature))
	if hostID == "" || timestamp == "" || nonce == "" || decodeErr != nil {
		return "", ErrUnauthenticated
	}
	seconds, parseErr := strconv.ParseInt(timestamp, 10, 64)
	if parseErr != nil {
		return "", ErrUnauthenticated
	}
	skew := now.Sub(time.Unix(seconds, 0))
	if skew > MaxClockSkew || skew < -MaxClockSkew {
		return "", ErrUnauthenticated
	}

	keyText, allowed := v.PublicKey(hostID)
	raw, keyErr := base64.StdEncoding.DecodeString(keyText)
	if !allowed || keyErr != nil || len(raw) != ed25519.PublicKeySize {
		return "", ErrUnauthenticated
	}
	message := canonical(r.Method, r.URL.Path, timestamp, nonce, hostID, body)
	if !ed25519.Verify(ed25519.PublicKey(raw), message, signature) {
		return "", ErrUnauthenticated
	}

	// The nonce is recorded only after the signature checks out, so someone without
	// a valid key cannot fill the cache.
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.nonces == nil {
		v.nonces = map[string]time.Time{}
	}
	for seen, expires := range v.nonces {
		if now.After(expires) {
			delete(v.nonces, seen)
		}
	}
	key := hostID + ":" + nonce
	if _, replay := v.nonces[key]; replay || len(v.nonces) >= maxNonces {
		return "", ErrUnauthenticated
	}
	v.nonces[key] = now.Add(2 * MaxClockSkew)
	return hostID, nil
}

// ErrUnauthenticated is the single answer for every failed check.
var ErrUnauthenticated = errors.New("not authenticated")

// JoinToken is a one-time secret an operator mints so one new host can enroll.
type JoinToken struct {
	// Secret is shown to the operator once and never stored.
	Secret string
	// Hash is what the cluster stores; it cannot be turned back into the secret.
	Hash      string
	ExpiresAt time.Time
}

// NewJoinToken makes a join token valid for ttl.
func NewJoinToken(now time.Time, ttl time.Duration) (JoinToken, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return JoinToken{}, err
	}
	secret := "adj_" + hex.EncodeToString(random)
	return JoinToken{Secret: secret, Hash: HashToken(secret), ExpiresAt: now.Add(ttl)}, nil
}

// HashToken is how a token is stored and compared.
func HashToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// SameHash compares two token hashes without leaking where they differ.
func SameHash(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// Trust is the outcome of an enrollment.
type Trust string

const (
	// Approved hosts may send events immediately.
	Approved Trust = "approved"
	// Pending hosts are known but wait for a person to approve them.
	Pending Trust = "pending"
	// Rejected hosts are refused outright.
	Rejected Trust = "rejected"
)

// Decide says how far to trust a host that presented a join token.
//
//   - A bad token is rejected: nothing else matters without proof that an operator
//     allowed this enrollment.
//   - A good token from a machine Kubernetes already lists as a node is approved:
//     the install path (the ISO) joins nodes to the cluster, so the cluster already
//     knows and trusts that machine, and the token shows the operator meant it.
//   - A good token from a machine the cluster does not list is pending. This is the
//     "add a new host with just this tool" path: a person confirms it.
//   - If the cluster's node list could not be read, the answer is pending rather
//     than approved: when unsure, ask a person.
func Decide(hostname string, nodes []string, nodesKnown, tokenValid bool) (Trust, string) {
	if !tokenValid {
		return Rejected, "the join token is not valid, or it has expired or been used"
	}
	if !nodesKnown {
		return Pending, "the cluster's server list could not be read, so a person must confirm this host"
	}
	short := ShortName(hostname)
	for _, node := range nodes {
		if ShortName(node) == short && short != "" {
			return Approved, "matches a server the cluster already trusts"
		}
	}
	return Pending, "this host is not a server in the cluster yet, so a person must approve it"
}

// ShortName is a host name without its domain and case, the form Kubernetes node names and
// host names are compared in.
func ShortName(name string) string {
	short, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(name)), ".")
	return short
}
