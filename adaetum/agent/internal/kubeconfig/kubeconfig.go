// Package kubeconfig is a minimal Kubernetes API client for a cluster node's own credentials.
//
// RKE2 leaves a kubeconfig on every node, readable by root only: the administrator's on a
// server and the kubelet's on an agent node. A host agent uses it for three narrow things
// (read one ConfigMap, list nodes, annotate its own node) so it can adopt itself into the
// cluster (internal/membership/adopt.go). It talks to the API over HTTPS directly. It starts
// no process, which keeps the agent's rule that it runs no commands on servers.
package kubeconfig

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Client talks to one Kubernetes API server with the credentials from a kubeconfig.
type Client struct {
	server string
	http   *http.Client
	token  string
}

type file struct {
	CurrentContext string `yaml:"current-context"`
	Clusters       []struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server   string `yaml:"server"`
			CAData   string `yaml:"certificate-authority-data"`
			CAFile   string `yaml:"certificate-authority"`
			Insecure bool   `yaml:"insecure-skip-tls-verify"`
		} `yaml:"cluster"`
	} `yaml:"clusters"`
	Users []struct {
		Name string `yaml:"name"`
		User struct {
			CertData string `yaml:"client-certificate-data"`
			KeyData  string `yaml:"client-key-data"`
			CertFile string `yaml:"client-certificate"`
			KeyFile  string `yaml:"client-key"`
			Token    string `yaml:"token"`
		} `yaml:"user"`
	} `yaml:"users"`
	Contexts []struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster string `yaml:"cluster"`
			User    string `yaml:"user"`
		} `yaml:"context"`
	} `yaml:"contexts"`
}

// Load reads the kubeconfig at path. It refuses a cluster that skips certificate checks: the
// credentials in this file are administrative, so the server must be authenticated.
func Load(path string) (*Client, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg file
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("%s is not a kubeconfig: %w", path, err)
	}
	clusterName, userName := "", ""
	for _, c := range cfg.Contexts {
		if c.Name == cfg.CurrentContext {
			clusterName, userName = c.Context.Cluster, c.Context.User
		}
	}
	if clusterName == "" && len(cfg.Clusters) > 0 {
		clusterName = cfg.Clusters[0].Name
	}
	if userName == "" && len(cfg.Users) > 0 {
		userName = cfg.Users[0].Name
	}

	c := &Client{}
	roots := x509.NewCertPool()
	found := false
	for _, cl := range cfg.Clusters {
		if cl.Name != clusterName {
			continue
		}
		if cl.Cluster.Insecure {
			return nil, errors.New("this kubeconfig skips certificate checks, which is refused")
		}
		c.server = strings.TrimRight(cl.Cluster.Server, "/")
		pem, err := decodeOrRead(cl.Cluster.CAData, cl.Cluster.CAFile)
		if err != nil || !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("the kubeconfig has no usable certificate authority")
		}
		found = true
	}
	if !found || !strings.HasPrefix(c.server, "https://") {
		return nil, errors.New("the kubeconfig names no https cluster")
	}

	tlsConfig := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	for _, u := range cfg.Users {
		if u.Name != userName {
			continue
		}
		c.token = strings.TrimSpace(u.User.Token)
		certPEM, certErr := decodeOrRead(u.User.CertData, u.User.CertFile)
		keyPEM, keyErr := decodeOrRead(u.User.KeyData, u.User.KeyFile)
		if certErr == nil && keyErr == nil {
			pair, err := tls.X509KeyPair(certPEM, keyPEM)
			if err != nil {
				return nil, fmt.Errorf("the kubeconfig's client certificate is not usable: %w", err)
			}
			tlsConfig.Certificates = []tls.Certificate{pair}
		}
	}
	if c.token == "" && len(tlsConfig.Certificates) == 0 {
		return nil, errors.New("the kubeconfig carries no credentials")
	}
	c.http = &http.Client{
		Transport:     &http.Transport{TLSClientConfig: tlsConfig},
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return c, nil
}

func decodeOrRead(data, path string) ([]byte, error) {
	if data != "" {
		return base64.StdEncoding.DecodeString(data)
	}
	if path != "" {
		return os.ReadFile(path)
	}
	return nil, errors.New("not set")
}

func (c *Client) do(ctx context.Context, method, path, contentType string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.server+path, reader)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("Kubernetes API returned HTTP %d", resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// ConfigMapData returns the data of one ConfigMap.
func (c *Client) ConfigMapData(ctx context.Context, namespace, name string) (map[string]string, error) {
	var cm struct {
		Data map[string]string `json:"data"`
	}
	path := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/configmaps/" + url.PathEscape(name)
	if err := c.do(ctx, http.MethodGet, path, "", nil, &cm); err != nil {
		return nil, err
	}
	return cm.Data, nil
}

// NodeNames lists the cluster's node names.
func (c *Client) NodeNames(ctx context.Context) ([]string, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/nodes", "", nil, &list); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.Metadata.Name)
	}
	return names, nil
}

// AnnotateNode sets one annotation on one node, leaving every other field alone.
func (c *Client) AnnotateNode(ctx context.Context, node, key, value string) error {
	patch := map[string]any{"metadata": map[string]any{"annotations": map[string]string{key: value}}}
	return c.do(ctx, http.MethodPatch, "/api/v1/nodes/"+url.PathEscape(node), "application/merge-patch+json", patch, nil)
}
