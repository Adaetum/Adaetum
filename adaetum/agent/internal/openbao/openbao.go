// Package openbao is the cluster agent's small window onto OpenBao: it keeps one secret,
// the private key behind the agent's own TLS certificate.
//
// WHY THE KEY IS HERE. Every host pins that key (internal/tlscert KeyPin), so the key is
// what makes the cluster agent recognizable. Left only on the agent's volume, losing the
// volume (or rebuilding the pod from scratch) makes a new key and cuts every host off. In
// OpenBao there is one copy that survives that, can be rotated in one place, and is
// readable by this agent alone (policy adaetum-agent-tls, bound to its service account).
//
// WHAT IT NEVER DOES. It is not a general OpenBao client and holds no standing credential:
// each call signs in with the pod's own service-account token, uses the short-lived token
// it gets, and forgets it. OpenBao being sealed, absent or unreachable is a normal state
// at boot (the agent starts before OpenBao is configured), so every failure here is
// reported to the caller as an error and never as a reason to stop the agent.
package openbao

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// keyPath is the KV v2 secret holding the key, under the "secret" mount like every
	// other Adaetum application secret. The policy grants exactly this path.
	keyPath = "/v1/secret/data/apps/adaetum/cluster-agent-tls"
	// DefaultJWTFile is the pod's own service-account token.
	DefaultJWTFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

// KeyStore reads and writes the cluster agent's TLS private key (PEM) in OpenBao.
type KeyStore struct {
	// Addr is OpenBao's address, such as http://openbao.openbao.svc.cluster.local:8200.
	Addr string
	// Role is the Kubernetes-auth role to sign in as (adaetum-agent-tls).
	Role string
	// JWTFile is the service-account token; empty means DefaultJWTFile.
	JWTFile string
	HTTP    *http.Client
}

// Get returns the stored key, or found=false when OpenBao is reachable and holds none yet.
func (s *KeyStore) Get(ctx context.Context) (pem []byte, found bool, err error) {
	token, err := s.login(ctx)
	if err != nil {
		return nil, false, err
	}
	status, body, err := s.do(ctx, http.MethodGet, keyPath, token, nil)
	if err != nil {
		return nil, false, err
	}
	switch status {
	case http.StatusOK:
		var reply struct {
			Data struct {
				Data struct {
					Key string `json:"key"`
				} `json:"data"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &reply) != nil || reply.Data.Data.Key == "" {
			return nil, false, errors.New("OpenBao's answer held no key")
		}
		return []byte(reply.Data.Data.Key), true, nil
	case http.StatusNotFound:
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("OpenBao refused to read the key (HTTP %d)", status)
	}
}

// Put stores the key, replacing any earlier one.
func (s *KeyStore) Put(ctx context.Context, pem []byte) error {
	token, err := s.login(ctx)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"data": map[string]string{"key": string(pem)}})
	status, _, err := s.do(ctx, http.MethodPost, keyPath, token, payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusNoContent {
		return fmt.Errorf("OpenBao refused to store the key (HTTP %d)", status)
	}
	return nil
}

func (s *KeyStore) login(ctx context.Context) (string, error) {
	file := s.JWTFile
	if file == "" {
		file = DefaultJWTFile
	}
	jwt, err := os.ReadFile(file)
	if err != nil {
		return "", errors.New("this pod has no service-account token to sign in to OpenBao with")
	}
	payload, _ := json.Marshal(map[string]string{"role": s.Role, "jwt": strings.TrimSpace(string(jwt))})
	status, body, err := s.do(ctx, http.MethodPost, "/v1/auth/kubernetes/login", "", payload)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("OpenBao refused the sign-in (HTTP %d); is the %q role set up?", status, s.Role)
	}
	var reply struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	if json.Unmarshal(body, &reply) != nil || reply.Auth.ClientToken == "" {
		return "", errors.New("OpenBao's sign-in answer held no token")
	}
	return reply.Auth.ClientToken, nil
}

// do makes one request. Error text never includes the request or response body, which
// can hold the token or the key.
func (s *KeyStore) do(ctx context.Context, method, path, token string, payload []byte) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.Addr, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, errors.New("OpenBao's address is not valid")
	}
	if token != "" {
		request.Header.Set("X-Vault-Token", token)
	}
	client := s.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, errors.New("OpenBao could not be reached")
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return response.StatusCode, body, nil
}
