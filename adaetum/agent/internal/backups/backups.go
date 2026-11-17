// Package backups is the Backups tab's view of Longhorn: whether an off-site backup target is
// set and reachable, which recurring jobs run, and which volumes those jobs actually cover.
//
// Longhorn is the backup software for volumes (snapshots on the cluster, backups to an
// S3-compatible target). This package decides nothing about how it is configured: the target
// and the jobs are declared in the cluster repository, and the agent reads them back here
// through its read-only service account. Reading is all it does today.
//
// WHY COVERAGE IS COMPUTED. "A job exists" does not mean "this volume is backed up": Longhorn
// applies a job to a volume only through labels on the volume (or the "default" group, for a
// volume with no job labels at all). A critical volume nobody labelled is the failure this
// view exists to show, so it works out, per volume, which backup jobs reach it and when the
// last backup finished.
package backups

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	jobLabel   = "recurring-job.longhorn.io/"
	groupLabel = "recurring-job-group.longhorn.io/"
	// GroupPrefix starts the name of the Longhorn recurring-job group of each schedule: the
	// schedule "daily" is the group "adaetum-backup-daily". A volume carries the label of the
	// one schedule it is on; one with none of them is not on a schedule.
	GroupPrefix = "adaetum-backup-"
	// StaleAfter is how old a volume's last backup may be before it is called stale. Backups
	// run at most daily by default, so two days means at least one run was missed.
	StaleAfter = 48 * time.Hour
)

// Target is the off-site backup target.
type Target struct {
	// Configured is false until a target URL is set.
	Configured bool `json:"configured"`
	// Available is Longhorn's own verdict that it can reach the target and list it.
	Available bool `json:"available"`
	// URL is the target address (a bucket, with no credentials: those live in a Secret).
	URL     string `json:"url,omitempty"`
	Message string `json:"message,omitempty"`
}

// Job is one Longhorn recurring job.
type Job struct {
	Name   string   `json:"name"`
	Task   string   `json:"task"`
	Cron   string   `json:"cron"`
	Retain int      `json:"retain"`
	Groups []string `json:"groups,omitempty"`
}

// Volume is one volume and what protects it.
type Volume struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Claim     string `json:"claim,omitempty"`
	// SizeBytes is the volume's maximum (what it was provisioned at).
	SizeBytes int64 `json:"sizeBytes"`
	// UsedBytes is what it holds now, which is what a backup actually copies.
	UsedBytes int64 `json:"usedBytes"`
	// Backup is the backup job that reaches this volume ("" means none does).
	Backup string `json:"backup,omitempty"`
	// Snapshot is the snapshot job that reaches it.
	Snapshot string `json:"snapshot,omitempty"`
	// LastBackupAt is when the newest backup finished (nil when there has never been one).
	LastBackupAt *time.Time `json:"lastBackupAt,omitempty"`
	// Sync is whether a VolSync ReplicationSource copies this volume's claim.
	Sync bool `json:"sync"`
	// LastSyncAt is when the newest sync finished.
	LastSyncAt *time.Time `json:"lastSyncAt,omitempty"`
	// Mode is "off", "scheduled", "sync", or "both" (a change between two modes is still
	// being made, since a pull request has to be merged for one of them).
	Mode string `json:"mode"`
	// Schedule is the name of the schedule this volume is on ("" when it is on none).
	Schedule string `json:"schedule,omitempty"`
	// State is "protected", "stale" (covered, but the last backup is old or missing),
	// or "unprotected" (no backup job reaches it).
	State string `json:"state"`
}

// State is what the tab shows.
type State struct {
	Target  Target   `json:"target"`
	Jobs    []Job    `json:"jobs"`
	Volumes []Volume `json:"volumes"`
	// Summary is "off" (no target), "failing" (target set but unreachable), "attention"
	// (some volume is stale), "unprotected" (some volume has no job; the rest are fine),
	// or "ok".
	Summary string `json:"summary"`
	// Schedules are the named backup schedules a volume can be put on.
	Schedules []Schedule `json:"schedules"`
	// SyncInstalled is whether VolSync's custom resources exist; Sync says whether its pod runs.
	SyncInstalled bool      `json:"syncInstalled"`
	Sync          SyncState `json:"sync"`
	// Storage says where each tier copies to (never the credentials). Filled by the API.
	Storage map[string]StorageInfo `json:"storage,omitempty"`
}

// Getter reads Kubernetes objects like `kubectl get ... -o json`.
type Getter func(ctx context.Context, resources string, extra ...string) ([]byte, error)

// Collect reads the cluster and computes the state. A failure reading any one kind is an error:
// a half-known answer about backups is worse than none.
func Collect(ctx context.Context, get Getter, now time.Time) (State, error) {
	var state State
	targets, err := get(ctx, "backuptargets.longhorn.io", "-n", "longhorn-system")
	if err != nil {
		return state, err
	}
	state.Target = parseTarget(targets)
	jobsJSON, err := get(ctx, "recurringjobs.longhorn.io", "-n", "longhorn-system")
	if err != nil {
		return state, err
	}
	state.Jobs = parseJobs(jobsJSON)
	state.Schedules = schedulesFrom(state.Jobs)
	volumesJSON, err := get(ctx, "volumes.longhorn.io", "-n", "longhorn-system")
	if err != nil {
		return state, err
	}
	// VolSync is optional: when its resources do not exist the read fails, which means "not installed".
	syncJSON, syncErr := get(ctx, "replicationsources.volsync.backube", "-A")
	state.SyncInstalled = syncErr == nil
	if deployments, err := get(ctx, "deployments", "-n", "volsync-system"); err == nil {
		state.Sync = parseSyncPods(deployments)
	}
	state.Volumes = parseVolumes(volumesJSON, syncJSON, state.Jobs, now)
	fillScheduleCounts(&state)
	state.Summary = summarize(state)
	return state, nil
}

type object struct {
	Metadata struct {
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace"`
		Labels            map[string]string `json:"labels"`
		CreationTimestamp time.Time         `json:"creationTimestamp"`
	} `json:"metadata"`
	Spec   map[string]json.RawMessage `json:"spec"`
	Status map[string]json.RawMessage `json:"status"`
}

func items(data []byte) []object {
	var list struct {
		Items []object `json:"items"`
	}
	_ = json.Unmarshal(data, &list)
	return list.Items
}

func text(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func parseTarget(data []byte) Target {
	var target Target
	for _, item := range items(data) {
		// Longhorn keeps one target named "default"; take it, or the first.
		if item.Metadata.Name != "default" && target.Configured {
			continue
		}
		target.URL = text(item.Spec["backupTargetURL"])
		target.Configured = target.URL != ""
		_ = json.Unmarshal(item.Status["available"], &target.Available)
		var conditions []struct {
			Message string `json:"message"`
			Status  string `json:"status"`
			Type    string `json:"type"`
		}
		_ = json.Unmarshal(item.Status["conditions"], &conditions)
		for _, condition := range conditions {
			if condition.Type == "Unavailable" && condition.Status == "True" {
				target.Message = condition.Message
			}
		}
	}
	return target
}

func parseJobs(data []byte) []Job {
	jobs := []Job{}
	for _, item := range items(data) {
		job := Job{Name: item.Metadata.Name, Task: text(item.Spec["task"]), Cron: text(item.Spec["cron"])}
		_ = json.Unmarshal(item.Spec["retain"], &job.Retain)
		_ = json.Unmarshal(item.Spec["groups"], &job.Groups)
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].Name < jobs[b].Name })
	return jobs
}

// reaching returns the job of a given task that applies to a volume with these labels.
// Longhorn applies a job when the volume names it, or names a group the job is in; a volume
// with no job labels at all gets the jobs in the "default" group.
func reaching(labels map[string]string, jobs []Job, task string) string {
	named := map[string]bool{}
	groups := map[string]bool{}
	for key, value := range labels {
		if value != "enabled" {
			continue
		}
		if name, ok := strings.CutPrefix(key, jobLabel); ok {
			named[name] = true
		}
		if name, ok := strings.CutPrefix(key, groupLabel); ok {
			groups[name] = true
		}
	}
	useDefault := len(named) == 0 && len(groups) == 0
	for _, job := range jobs {
		if job.Task != task {
			continue
		}
		if named[job.Name] {
			return job.Name
		}
		for _, group := range job.Groups {
			if groups[group] || (useDefault && group == "default") {
				return job.Name
			}
		}
	}
	return ""
}

// SyncStaleAfter is how old a sync may be before the volume is called stale; syncs run every
// fifteen minutes.
const SyncStaleAfter = 2 * time.Hour

func parseVolumes(data, syncData []byte, jobs []Job, now time.Time) []Volume {
	synced := map[string]*time.Time{}
	for _, source := range items(syncData) {
		claim := text(source.Spec["sourcePVC"])
		var at *time.Time
		if parsed, err := time.Parse(time.RFC3339, text(source.Status["lastSyncTime"])); err == nil {
			at = &parsed
		}
		synced[source.Metadata.Namespace+"/"+claim] = at
	}
	volumes := []Volume{}
	for _, item := range items(data) {
		volume := Volume{Name: item.Metadata.Name}
		if size, err := strconv.ParseInt(text(item.Spec["size"]), 10, 64); err == nil {
			volume.SizeBytes = size
		}
		if used, err := strconv.ParseInt(text(item.Status["actualSize"]), 10, 64); err == nil {
			volume.UsedBytes = used
		} else {
			var number int64
			if json.Unmarshal(item.Status["actualSize"], &number) == nil {
				volume.UsedBytes = number
			}
		}
		var kube struct {
			Namespace string `json:"namespace"`
			PVCName   string `json:"pvcName"`
		}
		_ = json.Unmarshal(item.Status["kubernetesStatus"], &kube)
		volume.Namespace, volume.Claim = kube.Namespace, kube.PVCName
		if at, err := time.Parse(time.RFC3339, text(item.Status["lastBackupAt"])); err == nil && !at.IsZero() {
			volume.LastBackupAt = &at
		}
		volume.Schedule = scheduleOf(item.Metadata.Labels)
		volume.Backup = reaching(item.Metadata.Labels, jobs, "backup")
		volume.Snapshot = reaching(item.Metadata.Labels, jobs, "snapshot")
		at, isSynced := synced[volume.Namespace+"/"+volume.Claim]
		volume.Sync, volume.LastSyncAt = isSynced && volume.Claim != "", at
		volume.Mode = "off"
		switch {
		case volume.Sync && volume.Schedule != "":
			volume.Mode = "both"
		case volume.Sync:
			volume.Mode = "sync"
		case volume.Schedule != "":
			volume.Mode = "scheduled"
		}
		switch {
		case volume.Sync:
			if volume.LastSyncAt == nil || now.Sub(*volume.LastSyncAt) > SyncStaleAfter {
				volume.State = "stale"
			} else {
				volume.State = "protected"
			}
		case volume.Backup == "":
			volume.State = "unprotected"
		case volume.LastBackupAt == nil || now.Sub(*volume.LastBackupAt) > StaleAfter:
			volume.State = "stale"
		default:
			volume.State = "protected"
		}
		volumes = append(volumes, volume)
	}
	sort.Slice(volumes, func(a, b int) bool { return volumes[a].Name < volumes[b].Name })
	return volumes
}

func summarize(state State) string {
	switch {
	case !state.Target.Configured:
		return "off"
	case !state.Target.Available:
		return "failing"
	}
	stale, unprotected := 0, 0
	for _, volume := range state.Volumes {
		switch volume.State {
		case "stale":
			stale++
		case "unprotected":
			unprotected++
		}
	}
	switch {
	case stale > 0:
		return "attention"
	case unprotected > 0:
		return "unprotected"
	}
	return "ok"
}

// API serves GET /api/v1/backups.
type API struct {
	Get Getter
	// Vault, when set, lets the page say where each tier's storage is (never the credentials).
	Vault interface {
		ReadFields(ctx context.Context, path string) (map[string]any, int, bool, error)
	}
	RequireUser func(http.Handler) http.Handler
	Now         func() time.Time
}

// Register adds the route behind the sign-in guard.
func (a *API) Register(mux *http.ServeMux) {
	var handler http.Handler = http.HandlerFunc(a.serve)
	if a.RequireUser != nil {
		handler = a.RequireUser(handler)
	}
	mux.Handle("GET /api/v1/backups", handler)
}

func (a *API) serve(w http.ResponseWriter, r *http.Request) {
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	state, err := Collect(r.Context(), a.Get, now())
	if err == nil && a.Vault != nil {
		state.Storage = map[string]StorageInfo{}
		for tier, path := range map[string]string{TierScheduled: VaultPath, TierSync: SyncVaultPath} {
			fields, _, found, readErr := a.Vault.ReadFields(r.Context(), path)
			if readErr == nil {
				state.Storage[tier] = infoOf(fields, found)
			}
		}
	}
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Longhorn could not be read: %v", err)})
		return
	}
	_ = json.NewEncoder(w).Encode(state)
}

// SyncState is whether VolSync's own pod is running.
type SyncState struct {
	// Desired and Ready count the replicas of its deployment; both are 0 when it is not deployed.
	Desired int `json:"desired"`
	Ready   int `json:"ready"`
}

func parseSyncPods(data []byte) SyncState {
	var state SyncState
	for _, item := range items(data) {
		var desired, ready int
		_ = json.Unmarshal(item.Spec["replicas"], &desired)
		_ = json.Unmarshal(item.Status["readyReplicas"], &ready)
		state.Desired += desired
		state.Ready += ready
	}
	return state
}

// scheduleOf is the schedule a volume's labels put it on: the group label with our prefix.
func scheduleOf(labels map[string]string) string {
	name := ""
	for key, value := range labels {
		if rest, ok := strings.CutPrefix(key, groupLabel+GroupPrefix); ok && value == "enabled" && (name == "" || rest < name) {
			name = rest
		}
	}
	return name
}
