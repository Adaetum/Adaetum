package main

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/tlscert"
)

// A plain-HTTP request to the HTTPS port is sent to https, a request for some other host is
// refused, and real TLS still works on the same port.
func TestPlainHTTPOnTheTLSPortRedirects(t *testing.T) {
	certificates := tlscert.NewManager(t.TempDir())
	if _, _, err := certificates.Current(); err != nil {
		t.Fatal(err)
	}
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := probe.Addr().String()
	probe.Close()

	server := &http.Server{
		Addr:              address,
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         &tls.Config{GetCertificate: certificates.GetCertificate, MinVersion: tls.VersionTLS12},
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello") }),
	}
	go serveTLSUpgradingHTTP(server, ownCertificateName(certificates))
	t.Cleanup(func() { server.Close() })
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", address); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := noFollow.Get("http://" + address + "/some/page?x=1")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusPermanentRedirect || response.Header.Get("Location") != "https://"+address+"/some/page?x=1" {
		t.Fatalf("plain http: status %d, location %q", response.StatusCode, response.Header.Get("Location"))
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("the redirect must not be cached")
	}

	// A forged Host must not make the agent send people somewhere else.
	request, _ := http.NewRequest("GET", "http://"+address+"/", nil)
	request.Host = "evil.example"
	response, err = noFollow.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("a foreign Host: status %d, want 400", response.StatusCode)
	}

	secure := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	response, err = secure.Get("https://" + address + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(body), "hello") {
		t.Fatalf("https still works: %q", body)
	}
}
