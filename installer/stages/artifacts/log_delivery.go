package artifacts

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// verifyLogDelivery checks the live recovery contract, not merely the Worker
// deployment result: an authenticated write must be listable and readable from
// the configured Worker/R2 binding. A stable probe name avoids accumulating
// setup artifacts in the operator's bucket.
func (r *run) verifyLogDelivery(ctx context.Context) error {
	base := strings.TrimRight(strings.TrimSpace(r.values["KS_BASE_URL"]), "/")
	token := strings.TrimSpace(r.values["KS_SHARED_TOKEN"])
	if base == "" || token == "" {
		return fmt.Errorf("KS_BASE_URL and KS_SHARED_TOKEN are required for the Worker/R2 log-delivery check")
	}
	if _, err := url.ParseRequestURI(base); err != nil {
		return fmt.Errorf("invalid KS_BASE_URL: %w", err)
	}

	var nonceBytes [12]byte
	if _, err := rand.Read(nonceBytes[:]); err != nil {
		return fmt.Errorf("generate delivery probe nonce: %w", err)
	}
	nonce := hex.EncodeToString(nonceBytes[:])
	host, name := "setup-probe", "delivery-probe.log"
	want := []byte("adaetum log-delivery probe " + nonce + "\n")
	client := r.s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	postURL := base + "/logs?" + url.Values{"host": []string{host}, "file": []string{name}}.Encode()
	if err := workerRequest(ctx, client, http.MethodPost, postURL, token, bytes.NewReader(want), func(resp *http.Response) error {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return responseError("write", resp)
		}
		return nil
	}); err != nil {
		return err
	}

	key := "logs/" + host + "/" + name
	listURL := base + "/logs?" + url.Values{"host": []string{host}, "name_prefix": []string{"delivery-probe"}}.Encode()
	if err := workerRequest(ctx, client, http.MethodGet, listURL, token, nil, func(resp *http.Response) error {
		if resp.StatusCode != http.StatusOK {
			return responseError("list", resp)
		}
		var listed struct{ Keys []string `json:"keys"` }
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&listed); err != nil {
			return fmt.Errorf("decode list response: %w", err)
		}
		for _, got := range listed.Keys {
			if got == key {
				return nil
			}
		}
		return fmt.Errorf("list response omitted probe key %q", key)
	}); err != nil {
		return err
	}

	readURL := base + "/logs/" + host + "/" + name
	return workerRequest(ctx, client, http.MethodGet, readURL, token, nil, func(resp *http.Response) error {
		if resp.StatusCode != http.StatusOK {
			return responseError("read", resp)
		}
		got, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return fmt.Errorf("read probe response: %w", err)
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("probe readback did not match the uploaded body")
		}
		return nil
	})
}

func workerRequest(ctx context.Context, client *http.Client, method, target, token string, body io.Reader, check func(*http.Response) error) error {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	req.Header.Set("x-ks-token", token)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Worker %s request: %w", method, err)
	}
	defer resp.Body.Close()
	return check(resp)
}

func responseError(operation string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf("Worker %s returned HTTP %d: %s", operation, resp.StatusCode, strings.TrimSpace(string(body)))
}

