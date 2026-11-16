package membership

// The cluster agent calling a host agent, which is the reverse of everything else in
// this package: a host normally speaks first, signed with its own key (internal/identity).
//
// WHICH KEY. The cluster already has one that every joined host already trusts: the
// certificate it serves HTTPS with, whose SHA-256 the host pinned when it joined
// (Link.Fingerprint). The cluster signs each request with that certificate's private key
// and sends the certificate along; the host accepts the request only if the certificate
// it was handed carries the key it pinned. So no new key is made, stored or
// distributed: whoever can sign here can already impersonate the cluster to its hosts.
//
// WHAT IT BUYS. Only read-only routes may use this guard (a host lists what it did, it is
// never told to do anything), so a stolen request or a compromised cluster agent still
// cannot change a host. The signature covers the method, the path and query, the target
// host's ID, a timestamp and a one-time nonce, so a captured request cannot be altered,
// aimed at another host, or replayed.

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/tlscert"
)

// Headers a cluster-to-host request carries. They are a contract between the two agents.
const (
	HeaderClusterCert      = "X-Adaetum-Cluster-Cert"
	HeaderClusterTimestamp = "X-Adaetum-Cluster-Timestamp"
	HeaderClusterNonce     = "X-Adaetum-Cluster-Nonce"
	HeaderClusterSignature = "X-Adaetum-Cluster-Signature"
)

const (
	clusterSignatureVersion = "adaetum-cluster-v1"
	// ClusterMaxSkew is how far apart the two clocks may be. A request is read-only, so
	// this only bounds how long a captured one stays replayable (and it cannot be, see the nonce).
	clusterMaxSkew   = 60 * time.Second
	maxClusterNonces = 10_000
)

// ErrNotCluster is the single answer for every failed check.
var ErrNotCluster = errors.New("not the cluster agent")

func clusterMessage(method, target, timestamp, nonce, hostID string) []byte {
	return []byte(strings.Join([]string{clusterSignatureVersion, method, target, timestamp, nonce, hostID}, "\n"))
}

// SignAsCluster adds the headers that prove a request came from the cluster agent whose
// certificate the host pinned. hostID is the host the request is for.
func SignAsCluster(r *http.Request, cert *tls.Certificate, hostID string, now time.Time) error {
	signer, ok := cert.PrivateKey.(crypto.Signer)
	if !ok || len(cert.Certificate) == 0 {
		return errors.New("the cluster agent's certificate cannot sign")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	nonceText := hex.EncodeToString(nonce)
	digest := sha256.Sum256(clusterMessage(r.Method, r.URL.RequestURI(), timestamp, nonceText, hostID))
	signature, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		return err
	}
	r.Header.Set(HeaderClusterCert, base64.StdEncoding.EncodeToString(cert.Certificate[0]))
	r.Header.Set(HeaderClusterTimestamp, timestamp)
	r.Header.Set(HeaderClusterNonce, nonceText)
	r.Header.Set(HeaderClusterSignature, base64.StdEncoding.EncodeToString(signature))
	return nil
}

// ClusterVerifier is the host's check that a request really came from its cluster agent.
type ClusterVerifier struct {
	// Link is the host's saved cluster link, read per request so joining needs no restart.
	Link func() (Link, bool)
	// HostID is this host's own ID, which the cluster put in what it signed.
	HostID string
	Now    func() time.Time

	mu     sync.Mutex
	nonces map[string]time.Time
}

// Verify returns nil only for a fresh, unreplayed request signed by the pinned certificate.
func (v *ClusterVerifier) Verify(r *http.Request) error {
	link, joined := v.Link()
	if !joined {
		return ErrNotCluster
	}
	now := v.Now()
	timestamp := r.Header.Get(HeaderClusterTimestamp)
	nonce := r.Header.Get(HeaderClusterNonce)
	signature, sigErr := base64.StdEncoding.DecodeString(r.Header.Get(HeaderClusterSignature))
	der, certErr := base64.StdEncoding.DecodeString(r.Header.Get(HeaderClusterCert))
	seconds, timeErr := strconv.ParseInt(timestamp, 10, 64)
	if nonce == "" || sigErr != nil || certErr != nil || timeErr != nil {
		return ErrNotCluster
	}
	if skew := now.Sub(time.Unix(seconds, 0)); skew > clusterMaxSkew || skew < -clusterMaxSkew {
		return ErrNotCluster
	}
	// The pin: the certificate must carry the key (or, on an older link, be the certificate)
	// this host joined.
	if !tlscert.PinMatches(NormalizeFingerprint(link.Fingerprint), der) {
		return ErrNotCluster
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return ErrNotCluster
	}
	key, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return ErrNotCluster
	}
	digest := sha256.Sum256(clusterMessage(r.Method, r.URL.RequestURI(), timestamp, nonce, v.HostID))
	if !ecdsa.VerifyASN1(key, digest[:], signature) {
		return ErrNotCluster
	}

	// Recorded only after the signature checks out, so a stranger cannot fill the cache.
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
	if _, replay := v.nonces[nonce]; replay || len(v.nonces) >= maxClusterNonces {
		return ErrNotCluster
	}
	v.nonces[nonce] = now.Add(2 * clusterMaxSkew)
	return nil
}

// Guard lets a request through only if it came from the cluster agent. Use it only on
// routes that read and never change anything.
func (v *ClusterVerifier) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || v.Verify(r) != nil {
			http.Error(w, `{"error":"not authenticated"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Endpoint is where a host says its own agent can be reached, so the cluster agent can
// read from it. It is part of the signed check-in, so only the host itself can set it.
type Endpoint struct {
	// Port is the host agent's HTTPS port.
	Port int `json:"port"`
	// Fingerprint is the SHA-256 of the host agent certificate's public key, lowercase hex (tlscert.KeyPin). The cluster
	// pins it, because the certificate is self-made and there is no authority to ask.
	Fingerprint string `json:"fingerprint"`
	// Addresses are the host's own addresses (not loopback or link-local), most useful first.
	Addresses []string `json:"addresses"`
}
