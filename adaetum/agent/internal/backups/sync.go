package backups

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Sync is the second tier: VolSync copies a volume to the R2 bucket every fifteen minutes,
// encrypted on the cluster first (restic), so a day's work is not the most a loss can cost.
//
// WHERE THE STATE LIVES. Whether a volume is on sync is a fact in the cluster repository, not a
// label: pods/volsync/sync/<namespace>-<claim>/ holds its ReplicationSource and the secret that
// gives its namespace the repository key. Changing it is therefore a pull request, like any
// change that deploys something. The agent reads the answer back from the cluster (a
// ReplicationSource for the claim), so the page is right whoever made the change.
//
// THE KEY. Sync has its own storage, chosen on the Sync tab (it may be the scheduled backups'
// storage or somewhere else entirely), and a repository password generated with it. Both are in
// OpenBao at SyncVaultPath. Each synced namespace gets them through an External Secret, so none
// of it is in git.

// SyncVaultPath is where the sync repository's secrets live.
const SyncVaultPath = "apps/volsync/restic"

const volsyncApp = "pods/volsync/volsync.app.yaml"

// SyncChange is one volume going onto or off sync.
type SyncChange struct {
	Namespace, Claim string
	// Enable is true to put the volume on sync, false to take it off.
	Enable bool
	// FirstUse is true when no other volume is on sync, so VolSync itself must be deployed;
	// LastUse is true when this is the last volume leaving, so VolSync can be removed.
	FirstUse, LastUse bool
}

// SyncChanger prepares and proposes sync changes.
type SyncChanger struct {
	Vault   Vault
	Propose Proposer
}

// ErrSyncStorage is returned when a volume is put on sync before sync has storage.
var ErrSyncStorage = errors.New("Choose storage for sync first (on the Sync tab)")

func directory(namespace, claim string) string {
	return "pods/volsync/sync/" + namespace + "-" + claim
}

// Change makes the change. Putting a volume on sync first makes sure the repository secrets exist.
func (c *SyncChanger) Change(ctx context.Context, change SyncChange) (string, error) {
	if !dns1123.MatchString(change.Namespace) || !dns1123.MatchString(change.Claim) {
		return "", errors.New("the volume has no usable namespace and claim name")
	}
	dir, app := directory(change.Namespace, change.Claim), "pods/volsync/"+change.Namespace+"-"+change.Claim+".app.yaml"
	if !change.Enable {
		remove := []string{app, dir + "/kustomization.yaml", dir + "/restic-secret.yaml", dir + "/replication-source.yaml"}
		if change.LastUse {
			remove = append(remove, volsyncApp)
		}
		url, _, err := c.Propose(ctx, Changes{Prefix: "adaetum/sync-off", Title: "Stop syncing " + change.Namespace + "/" + change.Claim, Body: "Stops the VolSync copy of this volume. Copies already in the bucket are kept.", Remove: remove})
		return url, err
	}
	fields, _, found, err := c.Vault.ReadFields(ctx, SyncVaultPath)
	if err != nil {
		return "", fmt.Errorf("could not check OpenBao: %w", err)
	}
	if !found {
		return "", ErrSyncStorage
	}
	files := map[string]string{
		app:                              "name: volsync-" + change.Namespace + "-" + change.Claim + "\nproject: default\nsource_type: git\nsource_target_revision: HEAD\nsource_path: " + dir + "\ndestination_namespace: " + change.Namespace + "\n",
		dir + "/kustomization.yaml":      "resources:\n  - restic-secret.yaml\n  - replication-source.yaml\n",
		dir + "/restic-secret.yaml":      resticSecret(change.Namespace, change.Claim, fields),
		dir + "/replication-source.yaml": replicationSource(change.Claim),
	}
	if change.FirstUse {
		files[volsyncApp] = volsyncAppFile
	}
	url, _, err := c.Propose(ctx, Changes{Prefix: "adaetum/sync-on", Title: "Sync " + change.Namespace + "/" + change.Claim, Body: syncBody(change), Create: files})
	return url, err
}

// resticSecret gives a namespace the sync repository's credentials. The environment names are
// those restic reads for the storage provider chosen on the Sync tab.
func resticSecret(namespace, claim string, fields map[string]any) string {
	remote := func(name, property string) string {
		return "    - secretKey: " + name + "\n      remoteRef: {key: " + SyncVaultPath + ", property: " + property + "}\n"
	}
	template := "        RESTIC_REPOSITORY: \"{{ .base }}volsync/" + namespace + "/" + claim + "\"\n        RESTIC_PASSWORD: \"{{ .password }}\"\n"
	data := remote("base", "RESTIC_BASE") + remote("password", "RESTIC_PASSWORD")
	if provider, _ := fields["PROVIDER"].(string); provider == "azure" {
		template += "        AZURE_ACCOUNT_NAME: \"{{ .account }}\"\n        AZURE_ACCOUNT_KEY: \"{{ .accountkey }}\"\n"
		data += remote("account", "AZURE_ACCOUNT_NAME") + remote("accountkey", "AZURE_ACCOUNT_KEY")
	} else {
		template += "        AWS_ACCESS_KEY_ID: \"{{ .keyid }}\"\n        AWS_SECRET_ACCESS_KEY: \"{{ .secret }}\"\n"
		data += remote("keyid", "AWS_ACCESS_KEY_ID") + remote("secret", "AWS_SECRET_ACCESS_KEY")
		if _, ok := fields["AWS_REGION"]; ok {
			template += "        AWS_DEFAULT_REGION: \"{{ .region }}\"\n"
			data += remote("region", "AWS_REGION")
		}
	}
	return `# Gives this namespace the sync repository's credentials from OpenBao (written by the agent
# when sync's storage was chosen). Nothing secret is in git.
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: volsync-restic
  namespace: ` + namespace + `
spec:
  refreshInterval: 1m
  secretStoreRef:
    name: openbao
    kind: ClusterSecretStore
  target:
    name: volsync-restic
    template:
      data:
` + template + "  data:\n" + data
}

func replicationSource(claim string) string {
	return `apiVersion: volsync.backube/v1alpha1
kind: ReplicationSource
metadata:
  name: sync
spec:
  sourcePVC: ` + claim + `
  trigger:
    schedule: "*/15 * * * *"
  restic:
    repository: volsync-restic
    # Copy from a clone of the volume, so the app keeps running and the copy is consistent.
    copyMethod: Clone
    pruneIntervalDays: 7
    retain: {hourly: 6, daily: 7, weekly: 4}
`
}

const volsyncAppFile = `name: volsync
project: default
source_type: helm
source_repo_url: https://backube.github.io/helm-charts
source_chart: volsync
source_target_revision: "0.12.1"
source_helm_values: |
  # The operator only watches ReplicationSources; a small reservation is enough.
  resources:
    requests:
      cpu: 25m
      memory: 64Mi
    limits:
      memory: 256Mi
destination_namespace: volsync-system
`

func syncBody(change SyncChange) string {
	body := "Copies `" + change.Namespace + "/" + change.Claim + "` to the R2 bucket every 15 minutes, encrypted before it leaves the cluster.\n\n" +
		"- The repository key is in OpenBao, not in this change.\n" +
		"- Once this is merged the volume leaves the daily-backup schedule automatically (one mode at a time).\n"
	if change.FirstUse {
		body += "- This also installs VolSync (a small operator pod), because no volume is on sync yet.\n"
	}
	return strings.TrimSpace(body) + "\n"
}
