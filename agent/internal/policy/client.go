package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client talks to a cluster agent's Security API with the access token. The MCP
// server uses it so an assistant can read and flip the switches the same way the
// web page does, with the same checks applied by the agent.
type Client struct {
	HTTP  *http.Client
	URL   string
	Token string
}

// List returns every lever's state.
func (c Client) List(ctx context.Context) ([]State, error) {
	var out struct {
		Levers []State `json:"levers"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/security", nil, &out); err != nil {
		return nil, err
	}
	return out.Levers, nil
}

// SetBody is what the Security API accepts.
type SetBody struct {
	Enabled         bool   `json:"enabled"`
	DurationMinutes int    `json:"durationMinutes"`
	Confirm         string `json:"confirm"`
	Via             string `json:"via"`
	Reason          string `json:"reason"`
}

// Set flips a lever. The agent's refusal is returned as the error, in its own words.
func (c Client) Set(ctx context.Context, key string, body SetBody) (State, error) {
	var state State
	err := c.do(ctx, http.MethodPut, "/api/v1/security/"+key, body, &state)
	return state, err
}

func (c Client) do(ctx context.Context, method, path string, body, into any) error {
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
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
		return fmt.Errorf("could not reach the cluster agent: %w", err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &failure) == nil && failure.Error != "" {
			return fmt.Errorf("%s", failure.Error)
		}
		return fmt.Errorf("the cluster agent answered %d", response.StatusCode)
	}
	return json.Unmarshal(data, into)
}
