package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/accounts"
	"github.com/Adaetum/Adaetum/agent/internal/identity"
	"github.com/Adaetum/Adaetum/agent/internal/membership"
	"github.com/Adaetum/Adaetum/agent/internal/store"
)

// Release builds set this with -ldflags so fleet comparisons reflect the
// actual artifact rather than a version hard-coded independently of releases.
var agentVersion = "dev"

const (
	// checkinEvery is how often a host says it is alive. internal/membership treats a
	// host as stale after three missed check-ins, so changing this changes what
	// "quiet" means on the dashboard.
	checkinEvery = 5 * time.Minute
	databaseName = "agent.db"
)

// startHostIdentity is the host half of the cluster design (internal/membership). It
// opens the host's local database, records a check-in now and every few minutes,
// and, if this host has joined a cluster agent (`adaetum-agent join`), delivers the
// events to it in the background.
//
// The host agent must work with the cluster agent absent, so none of this can stop
// or slow the agent: a database that will not open, or a cluster that cannot be
// reached, is reported and the agent carries on. It is a no-op without a data
// directory, and it refuses to run over SSH: that mode is a read-only development
// aid and must not leave a database on the developer's laptop that claims to be a host.
func startHostIdentity(dataDir, sshTarget string, auth *accounts.Client) func() {
	noop := func() {}
	if dataDir == "" {
		return noop
	}
	if sshTarget != "" {
		fmt.Fprintln(os.Stderr, "-data-dir cannot be used with -ssh: the local database belongs to the machine the agent runs on.")
		os.Exit(2)
	}
	db, err := store.Open(filepath.Join(dataDir, databaseName))
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot open the local database; continuing without it:", err)
		return noop
	}
	fmt.Fprintf(os.Stderr, "local database %s (host %s)\n", filepath.Join(dataDir, databaseName), db.HostID())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 2)
	// A host that has joined a cluster copies the cluster's accounts, so people can sign
	// in here even when the cluster is down. Ends with ctx; never blocks the agent.
	startAccountSync(ctx, dataDir, db.HostID(), auth)

	go func() {
		defer func() { done <- struct{}{} }()
		hostname, _ := os.Hostname()
		record := func() {
			if _, err := db.Append(ctx, membership.CheckInKind, membership.CheckInPayload{Hostname: hostname, Version: agentVersion}); err != nil && ctx.Err() == nil {
				fmt.Fprintln(os.Stderr, "cannot record a check-in:", err)
			}
		}
		record()
		ticker := time.NewTicker(checkinEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				record()
			}
		}
	}()

	senders := 1
	if transport := clusterTransport(dataDir, db.HostID()); transport != nil {
		senders = 2
		go func() {
			defer func() { done <- struct{}{} }()
			var lastReported string
			membership.RunSender(ctx, db, transport, membership.SenderOptions{
				Every: time.Minute,
				// Say what is wrong when it changes, not every minute: an unreachable
				// cluster is normal and must not flood the log.
				OnError: func(err error) {
					if message := err.Error(); message != lastReported {
						lastReported = message
						fmt.Fprintln(os.Stderr, "cannot reach the cluster agent (will keep trying):", err)
					}
				},
			})
		}()
	}

	return func() {
		cancel()
		for i := 0; i < senders; i++ {
			<-done
		}
		db.Close()
	}
}

// clusterTransport returns a transport to the cluster agent this host joined, or
// nil if it has not joined one.
func clusterTransport(dataDir, hostID string) membership.Transport {
	link, joined, err := membership.LoadLink(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ignoring the cluster link:", err)
		return nil
	}
	if !joined {
		return nil
	}
	key, err := identity.LoadOrCreateHostKey(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot load this host's key, so events will not be sent:", err)
		return nil
	}
	fmt.Fprintf(os.Stderr, "sending events to the cluster agent at %s\n", link.URL)
	return &membership.HTTPTransport{Link: link, HostID: hostID, Key: key}
}
