package backups

import (
	"errors"
	"strings"
)

// scheduledChanges is the pull request that turns scheduled backups on. The files are plain
// Kubernetes objects, reviewed and merged like any other change; the credentials are NOT among
// them (External Secrets reads them from OpenBao). The jobs are in per-schedule groups, which
// reach only the volumes the Backups page has put on a schedule: nothing is backed up by default.
func scheduledChanges(fields map[string]any) (Changes, error) {
	provider, _ := fields["PROVIDER"].(string)
	bucket, _ := fields["BUCKET"].(string)
	var url string
	var secretKeys [][2]string // Secret key Longhorn reads, OpenBao field it comes from
	switch provider {
	case "azure":
		url = "azblob://" + bucket + "@core.windows.net/"
		secretKeys = [][2]string{{"AZBLOB_ACCOUNT_NAME", "AZURE_ACCOUNT_NAME"}, {"AZBLOB_ACCOUNT_KEY", "AZURE_ACCOUNT_KEY"}}
	case "r2", "s3":
		region, _ := fields["AWS_REGION"].(string)
		url = "s3://" + bucket + "@" + region + "/"
		secretKeys = [][2]string{{"AWS_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID"}, {"AWS_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY"}}
		if endpoint, _ := fields["AWS_ENDPOINTS"].(string); endpoint != "" {
			secretKeys = append(secretKeys, [2]string{"AWS_ENDPOINTS", "AWS_ENDPOINTS"})
		}
	default:
		return Changes{}, errors.New("the stored storage has no known provider")
	}
	var data strings.Builder
	for _, key := range secretKeys {
		data.WriteString("    - secretKey: " + key[0] + "\n      remoteRef: {key: " + VaultPath + ", property: " + key[1] + "}\n")
	}
	return Changes{
		Prefix: "adaetum/backups",
		Title:  "Turn on scheduled backups",
		Body: "Turns on scheduled backups to the " + provider + " storage `" + bucket + "`.\n\n" +
			"- One schedule to start with (daily at 03:00, keeping 7). Add or change schedules on the Backups page.\n" +
			"- Nothing is backed up until volumes are put on a schedule there.\n" +
			"- The credentials are in OpenBao, not in this change.\n" +
			"- Merge to apply. Closing this leaves the storage unused.\n",
		Create: map[string]string{
			"pods/longhorn-system/longhorn-backups.app.yaml": `name: longhorn-backups
project: default
sync_automated: true
source_type: git
source_target_revision: HEAD
source_path: pods/longhorn-system/backups
destination_namespace: longhorn-system
# Longhorn already created the "default" BackupTarget; server-side apply adopts it.
sync_options:
  - ServerSideApply=true
`,
			"pods/longhorn-system/backups/kustomization.yaml": "resources:\n  - external-secret.yaml\n  - backup-target.yaml\n  - recurring-jobs.yaml\n",
			"pods/longhorn-system/backups/external-secret.yaml": `# The storage credentials live in OpenBao (written by the agent when storage was chosen);
# External Secrets turns them into the Secret Longhorn reads.
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: longhorn-backup-credentials
  namespace: longhorn-system
spec:
  refreshInterval: 1m
  secretStoreRef:
    name: openbao
    kind: ClusterSecretStore
  target:
    name: longhorn-backup-credentials
  data:
` + data.String(),
			"pods/longhorn-system/backups/backup-target.yaml": `apiVersion: longhorn.io/v1beta2
kind: BackupTarget
metadata:
  name: default
  namespace: longhorn-system
spec:
  backupTargetURL: ` + url + `
  credentialSecret: longhorn-backup-credentials
  pollInterval: 300s
`,
			schedulesFile: recurringJobsYaml(DefaultSchedules()),
		},
	}, nil
}
