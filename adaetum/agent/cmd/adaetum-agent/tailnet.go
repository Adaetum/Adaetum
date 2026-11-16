package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/Adaetum/Adaetum/agent/internal/auditlog"
	"github.com/Adaetum/Adaetum/agent/internal/cluster"
	"github.com/Adaetum/Adaetum/agent/internal/tailnet"
)

// registerTailnetHTTPS serves the Settings page's Tailscale HTTPS switch (cluster agent only:
// the setting is about the cluster's Ingresses). It reads the Ingresses with the pod's
// read-only service account and changes nothing in the cluster; the only change it can make
// is the tailnet setting itself, with a token the person supplies for that one request.
// Changes are written to the same record as the Security switches, and refused if it cannot
// be written. Outside a cluster there are no Ingresses to read, so the route is not added.
func registerTailnetHTTPS(mux *http.ServeMux, auditPath string, guard func(http.Handler) http.Handler) {
	reader := cluster.InClusterReader()
	if reader == nil {
		return
	}
	api := &tailnet.API{
		Ingresses:   func(ctx context.Context) ([]byte, error) { return reader.Get(ctx, "ingresses", "-A") },
		RequireUser: guard,
		Who:         personName,
	}
	if log, err := auditlog.Open(auditPath); err != nil {
		fmt.Fprintf(os.Stderr, "tailnet HTTPS: changes are disabled because the record at %s cannot be written (%v)\n", auditPath, err)
	} else {
		api.Audit = log.Record // kept open for the life of the agent
	}
	api.Register(mux)
}
