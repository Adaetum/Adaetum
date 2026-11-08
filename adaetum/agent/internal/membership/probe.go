package membership

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// probeTimeout is short on purpose: the answer feeds an indicator in the page, and
// "unreachable" must not take longer to say than the page takes to refresh.
const probeTimeout = 3 * time.Second

// Probe asks whether the cluster agent this host joined is answering right now. It
// goes through the same pinned-certificate client as every other call, so a server
// that is up but is not the cluster agent counts as unreachable. /healthz is
// unauthenticated and reveals only that the process is up, so nothing secret is sent.
func (l Link) Probe(ctx context.Context) error {
	client := l.client()
	client.Timeout = probeTimeout
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, l.URL+"/healthz", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("the cluster agent answered %d", response.StatusCode)
	}
	return nil
}
