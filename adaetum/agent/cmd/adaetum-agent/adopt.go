package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/identity"
	"github.com/Adaetum/Adaetum/agent/internal/kubeconfig"
	"github.com/Adaetum/Adaetum/agent/internal/membership"
)

// exitAdopted is the exit status a host agent leaves with right after it adopts itself. It is
// deliberately not zero: the service restarts only on failure, and a restart is how the sender
// and the account copy pick up the new link, since they read it once at startup.
const exitAdopted = 75

// Where RKE2 keeps the credentials a cluster node already has. The server file is the
// administrator's; the other is the node's own. Both are readable by root only.
var (
	adoptKubeconfigs = []string{"/etc/rancher/rke2/rke2.yaml", "/var/lib/rancher/rke2/agent/kubelet.kubeconfig"}
	adoptRetryEvery  = 30 * time.Second
)

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// startAutoAdopt lets a host that is a cluster node join the cluster agent with no token and
// no address to type (see membership/adopt.go for why this is safe). It runs only for a host
// with no link yet, only where RKE2's credentials exist on the machine, and only in the
// background: the host agent never waits on the cluster, which may not be up for minutes
// after boot. Once it succeeds it exits so the service restarts with the link.
func startAutoAdopt(ctx context.Context, dataDir, hostID string) {
	if _, joined, err := membership.LoadLink(dataDir); err != nil || joined {
		return
	}
	kubeconfigPath := membership.FirstExisting(adoptKubeconfigs, fileExists)
	if kubeconfigPath == "" {
		return // not a cluster node, or the cluster is not installed yet: join by hand
	}
	key, err := identity.LoadOrCreateHostKey(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "host adoption: cannot load this host's key:", err)
		return
	}
	hostname, _ := os.Hostname()

	go func() {
		var last string
		say := func(message string) {
			if message != last {
				last = message
				fmt.Fprintln(os.Stderr, "host adoption:", message)
			}
		}
		for {
			// Reread the credentials each time: the cluster may have been installed after boot.
			kube, err := kubeconfig.Load(kubeconfigPath)
			var link membership.Link
			var result membership.EnrollResult
			if err == nil {
				attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
				link, result, err = membership.Adopter{Kube: kube, Enroll: membership.Enroll}.Adopt(attempt, hostID, hostname, key.PublicKey())
				cancel()
			}
			switch {
			case err != nil:
				say("waiting for the cluster (" + err.Error() + ")")
			case result.Status == string(identity.Approved), result.Status == string(identity.Pending):
				if saveErr := membership.SaveLink(dataDir, link); saveErr != nil {
					say("adopted, but could not save the link: " + saveErr.Error())
					break
				}
				fmt.Fprintf(os.Stderr, "host adoption: joined the cluster agent at %s (%s); restarting to start sending\n", link.URL, result.Status)
				os.Exit(exitAdopted)
			default:
				say("the cluster agent did not accept this host: " + result.Reason)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(adoptRetryEvery):
			}
		}
	}()
}
