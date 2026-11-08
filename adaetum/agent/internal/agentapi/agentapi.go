// Package agentapi is a small client for the agent's own HTTP API.
//
// It exists so that every capability has ONE implementation: the web page, the MCP
// server, and the in-app assistant all act through the same endpoints, so the same
// checks, the same token, the same audit records, and the same switches apply no
// matter who is asking. The MCP server reaches a cluster agent over the network with
// a pinned certificate; the in-app assistant reaches the agent it lives in through
// an in-process transport (ServeTransport), with no network at all.
package agentapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
)

// Client calls the agent's API as a signed-in person would.
type Client struct {
	HTTP  *http.Client
	URL   string
	Token string
}

// Do sends a request and decodes a JSON answer into `into` (which may be nil). A
// refusal from the agent is returned as an error in the agent's own words, because
// those words are written for people ("name the servers to run this on").
func (c Client) Do(ctx context.Context, method, path string, body, into any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("could not reach the agent: %w", err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 4<<20))

	if response.StatusCode < 200 || response.StatusCode > 299 {
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &failure) == nil && failure.Error != "" {
			return fmt.Errorf("%s", failure.Error)
		}
		if text := strings.TrimSpace(string(data)); text != "" && len(text) < 300 {
			return fmt.Errorf("%s", text)
		}
		return fmt.Errorf("the agent answered %d", response.StatusCode)
	}
	if into == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return json.Unmarshal(data, into)
}

// ServeTransport is an http.RoundTripper that hands each request straight to a
// handler in this process and returns its answer. The in-app assistant uses it to
// call the agent's own API without a socket, a certificate, or a loopback port,
// while still passing through every middleware (the access-token check included).
type ServeTransport struct{ Handler http.Handler }

// RoundTrip implements http.RoundTripper.
func (t ServeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	t.Handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}
