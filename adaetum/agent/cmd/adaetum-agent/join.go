package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/identity"
	"github.com/Adaetum/Adaetum/agent/internal/membership"
	"github.com/Adaetum/Adaetum/agent/internal/store"
)

// runJoin enrolls this host with a cluster agent. It is run once per host.
//
// INTENT: this is also the first step of adding a machine that did not come from
// the ISO. Such a host gets only this tool; `join` makes it known to the cluster,
// after which the cluster agent (and in future this tool) can manage its install.
// The three things a person supplies are exactly the three things that cannot be
// guessed: where the cluster agent is, which certificate to trust (its fingerprint,
// printed when the cluster agent starts), and a one-time join token an operator
// minted. The token is read from a file or the environment, never a command-line
// argument, so it does not appear in the process list or shell history.
func runJoin(args []string) {
	flags := flag.NewFlagSet("join", flag.ExitOnError)
	clusterURL := flags.String("cluster", "", "URL of the cluster agent, e.g. https://cluster-agent:8444")
	fingerprint := flags.String("fingerprint", "", "SHA-256 fingerprint of the cluster agent's certificate (printed when it starts)")
	tokenFile := flags.String("token-file", "", "file holding the join token (or set ADAETUM_JOIN_TOKEN)")
	dataDir := flags.String("data-dir", stateDir, "this host's agent data directory")
	_ = flags.Parse(args)

	fail := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", a...)
		os.Exit(1)
	}
	if *clusterURL == "" || *fingerprint == "" {
		fail("join needs -cluster and -fingerprint. The cluster agent prints its fingerprint when it starts.")
	}
	token := strings.TrimSpace(os.Getenv("ADAETUM_JOIN_TOKEN"))
	if *tokenFile != "" {
		data, err := os.ReadFile(*tokenFile)
		if err != nil {
			fail("cannot read the join token file: %v", err)
		}
		token = strings.TrimSpace(string(data))
	}
	if token == "" {
		fail("join needs a one-time join token: put it in a file and pass -token-file, or set ADAETUM_JOIN_TOKEN.")
	}

	db, err := store.Open(filepath.Join(*dataDir, databaseName))
	if err != nil {
		fail("cannot open the local database: %v", err)
	}
	hostID := db.HostID()
	db.Close()
	key, err := identity.LoadOrCreateHostKey(*dataDir)
	if err != nil {
		fail("cannot make this host's key: %v", err)
	}
	hostname, _ := os.Hostname()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	link := membership.Link{URL: *clusterURL, Fingerprint: *fingerprint}
	result, err := membership.Enroll(ctx, link, membership.EnrollRequest{HostID: hostID, Hostname: hostname, PublicKey: key.PublicKey(), JoinToken: token})
	if err != nil {
		fail("could not reach the cluster agent (nothing was changed): %v", err)
	}

	switch result.Status {
	case string(identity.Approved):
		fmt.Println("Joined. This host is approved:", result.Reason+".")
	case string(identity.Pending):
		fmt.Println("Joined, but waiting for a person to approve this host:", result.Reason+".")
		fmt.Printf("Ask an operator to approve host %s on the cluster agent. Events start flowing once they do.\n", hostID)
	default:
		fail("The cluster agent refused this host: %s", result.Reason)
	}
	// The link is saved only after a successful answer, so a mistyped URL or token
	// leaves no half-joined state behind.
	if err := membership.SaveLink(*dataDir, link); err != nil {
		fail("joined, but could not save the link: %v", err)
	}
	fmt.Println("Restart the agent (serve -data-dir", *dataDir+") to start sending.")
}
