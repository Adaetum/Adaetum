package membership

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/identity"
	"github.com/Adaetum/Adaetum/agent/internal/store"
)

// Link is a host's saved connection to its cluster agent. It holds no secret: the
// host's credential is its private key, and the cluster's identity is the pinned
// certificate fingerprint.
type Link struct {
	// URL of the cluster agent, e.g. https://cluster-agent.example:8443.
	URL string `json:"url"`
	// Fingerprint is the cluster agent certificate's SHA-256, which the host
	// requires on every connection. The cluster agent makes its own certificate, so
	// there is no authority to ask; the operator pins the fingerprint printed when
	// the cluster agent starts, once, when the host joins.
	Fingerprint string `json:"fingerprint"`
}

const linkFileName = "cluster.json"

// LoadLink reads the saved link in dir. The bool is false when the host has not
// joined a cluster agent, which is a normal state, not an error.
func LoadLink(dir string) (Link, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, linkFileName))
	if errors.Is(err, os.ErrNotExist) {
		return Link{}, false, nil
	}
	if err != nil {
		return Link{}, false, err
	}
	var link Link
	if err := json.Unmarshal(data, &link); err != nil || link.URL == "" || link.Fingerprint == "" {
		return Link{}, false, fmt.Errorf("%s is not a valid cluster link", filepath.Join(dir, linkFileName))
	}
	return link, true, nil
}

// SaveLink records the link.
func SaveLink(dir string, link Link) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(link, "", "  ")
	return os.WriteFile(filepath.Join(dir, linkFileName), data, 0o600)
}

// NormalizeFingerprint accepts the forms people paste (colons or not, any case).
func NormalizeFingerprint(text string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(text), ":", ""))
}

// HTTPClient returns a client that talks only to the pinned cluster agent.
func (l Link) HTTPClient() *http.Client { return l.client() }

// client returns an HTTP client that talks only to a server presenting the pinned
// certificate. Ordinary certificate-authority checks are replaced, not weakened:
// the pin is stricter, because it names the one certificate that is acceptable.
func (l Link) client() *http.Client {
	want := NormalizeFingerprint(l.Fingerprint)
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: true, // replaced by the pin check below
				VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
					if len(raw) == 0 {
						return errors.New("the cluster agent presented no certificate")
					}
					sum := sha256.Sum256(raw[0])
					if hex.EncodeToString(sum[:]) != want {
						return errors.New("the cluster agent's certificate does not match the pinned fingerprint")
					}
					return nil
				},
			},
		},
	}
}

// EnrollRequest is what a new host sends to join. The wire format is shared with
// internal/cluster, which receives it.
type EnrollRequest struct {
	HostID    string `json:"hostId"`
	Hostname  string `json:"hostname"`
	PublicKey string `json:"publicKey"`
	JoinToken string `json:"joinToken"`
}

// EnrollResult is the cluster agent's answer.
type EnrollResult struct {
	Status string `json:"status"` // approved, pending, or rejected
	Reason string `json:"reason"`
}

// Enroll asks the cluster agent to accept this host. A rejected enrollment returns
// the result with a nil error, so the caller can show the reason.
func Enroll(ctx context.Context, link Link, request EnrollRequest) (EnrollResult, error) {
	body, _ := json.Marshal(request)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(link.URL, "/")+"/api/v1/enroll", bytes.NewReader(body))
	if err != nil {
		return EnrollResult{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := link.client().Do(httpRequest)
	if err != nil {
		return EnrollResult{}, err
	}
	defer response.Body.Close()

	var result EnrollResult
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&result); err != nil || result.Status == "" {
		return EnrollResult{}, fmt.Errorf("the cluster agent answered %d with something unexpected", response.StatusCode)
	}
	return result, nil
}

// HTTPTransport delivers events to the cluster agent over HTTPS, signed with the
// host's key (see internal/identity). It implements Transport.
type HTTPTransport struct {
	Link   Link
	HostID string
	Key    *identity.HostKey
	Now    func() time.Time
}

// Send posts a batch and returns the highest sequence the cluster holds.
func (t *HTTPTransport) Send(ctx context.Context, events []store.Event) (int64, error) {
	body, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.Link.URL, "/")+"/api/v1/events", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	now := time.Now
	if t.Now != nil {
		now = t.Now
	}
	if err := t.Key.Sign(request, t.HostID, body, now()); err != nil {
		return 0, err
	}
	response, err := t.Link.client().Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return 0, fmt.Errorf("the cluster agent answered %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	var reply struct {
		Acked int64 `json:"acked"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&reply); err != nil {
		return 0, errors.New("the cluster agent's reply was not understood")
	}
	return reply.Acked, nil
}
