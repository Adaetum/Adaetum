package kubeconfig

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, server *httptest.Server, user string) string {
	t.Helper()
	ca := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	text := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: default
clusters:
- name: c
  cluster: {server: %s, certificate-authority-data: %s}
users:
- name: u
  user: %s
contexts:
- name: default
  context: {cluster: c, user: u}
`, server.URL, ca, user)
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheClientReadsAndPatchesThroughTheAPIWithTheKubeconfigsToken(t *testing.T) {
	var seen []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+" "+r.Header.Get("Content-Type"))
		switch {
		case strings.HasSuffix(r.URL.Path, "/configmaps/ep"):
			fmt.Fprint(w, `{"data":{"url":"https://x:1","fingerprint":"AA"}}`)
		case r.URL.Path == "/api/v1/nodes":
			fmt.Fprint(w, `{"items":[{"metadata":{"name":"node-a"}}]}`)
		default:
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{}`)
		}
	}))
	defer server.Close()
	c, err := Load(write(t, server, "{token: secret-token}"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	data, err := c.ConfigMapData(ctx, "adaetum", "ep")
	if err != nil || data["url"] != "https://x:1" {
		t.Fatalf("configmap: %v %v", data, err)
	}
	names, err := c.NodeNames(ctx)
	if err != nil || len(names) != 1 || names[0] != "node-a" {
		t.Fatalf("nodes: %v %v", names, err)
	}
	if err := c.AnnotateNode(ctx, "node-a", "adaetum.io/host-key", "KEY"); err != nil {
		t.Fatal(err)
	}
	last := seen[len(seen)-1]
	if last != "PATCH /api/v1/nodes/node-a Bearer secret-token application/merge-patch+json" {
		t.Fatalf("patch request = %q", last)
	}
}

func TestANodeNameCannotEscapeItsPathSegment(t *testing.T) {
	var path string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { path = r.URL.EscapedPath(); fmt.Fprint(w, `{}`) }))
	defer server.Close()
	c, err := Load(write(t, server, "{token: t}"))
	if err != nil {
		t.Fatal(err)
	}
	_ = c.AnnotateNode(context.Background(), "../secrets/x", "k", "v")
	if path != "/api/v1/nodes/..%2Fsecrets%2Fx" {
		t.Fatalf("the node name changed the request path: %q", path)
	}
}

func TestAKubeconfigThatSkipsCertificateChecksOrHasNoCredentialsIsRefused(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	if _, err := Load(write(t, server, "{}")); err == nil {
		t.Fatal("a kubeconfig with no credentials was accepted")
	}
	insecure := filepath.Join(t.TempDir(), "kc")
	text := "clusters:\n- name: c\n  cluster: {server: 'https://x:1', insecure-skip-tls-verify: true}\nusers:\n- name: u\n  user: {token: t}\ncontexts:\n- name: d\n  context: {cluster: c, user: u}\ncurrent-context: d\n"
	if err := os.WriteFile(insecure, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(insecure); err == nil {
		t.Fatal("an insecure kubeconfig was accepted")
	}
}

// RKE2's own kubeconfig authenticates with an embedded client certificate, not a token.
func TestAClientCertificateKubeconfigLoads(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "system:admin"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	priv := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	if _, err := Load(write(t, server, fmt.Sprintf("{client-certificate-data: %s, client-key-data: %s}", cert, priv))); err != nil {
		t.Fatalf("an RKE2-style kubeconfig was refused: %v", err)
	}
	if _, err := Load(write(t, server, fmt.Sprintf("{client-certificate-data: %s, client-key-data: %s}", cert, cert))); err == nil {
		t.Fatal("a client certificate with a mismatched key was accepted")
	}
}
