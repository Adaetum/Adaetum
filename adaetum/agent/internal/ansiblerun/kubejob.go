package ansiblerun

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// KubernetesJob launches a tightly scoped, validated playbook in the existing
// Ansible runner's namespace. It reuses the operator-owned runner pod template
// and its node, so repository data and OpenBao credentials come from their
// existing source of truth, never from an arbitrary image or caller-supplied pod.
// The service account needs namespace-scoped job and secret permissions; those
// permissions are powerful and must only be granted to the cluster agent.
type KubernetesJob struct {
	Endpoint     string
	TokenFile    string
	HTTP         *http.Client
	Namespace    string
	PollInterval time.Duration
}

func NewKubernetesJob() (*KubernetesJob, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, errors.New("Kubernetes service address is unavailable")
	}
	const dir = "/var/run/secrets/kubernetes.io/serviceaccount"
	ca, err := os.ReadFile(dir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("read Kubernetes CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("Kubernetes CA is invalid")
	}
	return &KubernetesJob{Endpoint: "https://" + net.JoinHostPort(host, port), TokenFile: dir + "/token", Namespace: "ansible", HTTP: &http.Client{
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (k *KubernetesJob) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(k.Endpoint, "/")+path, input)
	if err != nil {
		return nil, err
	}
	token, err := os.ReadFile(k.TokenFile)
	if err != nil {
		return nil, fmt.Errorf("read service account token: %w", err)
	}
	if strings.TrimSpace(string(token)) == "" {
		return nil, errors.New("service account token is empty")
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := k.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Kubernetes request failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("Kubernetes %s returned HTTP %d", method, resp.StatusCode)
	}
	return resp, nil
}
func (k *KubernetesJob) json(ctx context.Context, method, path string, body, out any) error {
	resp, err := k.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return err
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode Kubernetes response: %w", err)
	}
	return nil
}
func (k *KubernetesJob) namespace() string {
	if k.Namespace == "" {
		return "ansible"
	}
	return k.Namespace
}
func (k *KubernetesJob) path(group string) string {
	return group + "/namespaces/" + url.PathEscape(k.namespace())
}

// runner returns the operator's runner pod specification and the node hosting
// its ready instance. Its hostPath is node-local, so a job must use that node.
func (k *KubernetesJob) runner(ctx context.Context) (map[string]any, string, error) {
	var deployment struct {
		Spec struct {
			Template struct {
				Spec map[string]any `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	path := k.path("/apis/apps/v1") + "/deployments/ansible-runner"
	if err := k.json(ctx, "GET", path, nil, &deployment); err != nil {
		return nil, "", fmt.Errorf("read Ansible runner: %w", err)
	}
	spec := deployment.Spec.Template.Spec
	if spec == nil || spec["serviceAccountName"] != "ansible-runner" {
		return nil, "", errors.New("Ansible runner deployment has an unexpected service account")
	}
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 {
		return nil, "", errors.New("Ansible runner must have exactly one container")
	}
	container, ok := containers[0].(map[string]any)
	if !ok || container["name"] != "ansible-runner" {
		return nil, "", errors.New("Ansible runner container is missing")
	}
	image, ok := container["image"].(string)
	if !ok || strings.TrimSpace(image) == "" {
		return nil, "", errors.New("Ansible runner image is missing")
	}
	if extra, ok := spec["initContainers"].([]any); ok && len(extra) > 0 {
		return nil, "", errors.New("Ansible runner has unexpected init containers")
	}
	// The role's inventory and playbooks live on the runner's hostPath. Refuse
	// an altered template rather than execute against a different source tree.
	mounted := false
	if mounts, ok := container["volumeMounts"].([]any); ok {
		for _, item := range mounts {
			if m, ok := item.(map[string]any); ok && m["name"] == "ansible-data" && m["mountPath"] == "/ansible" {
				mounted = true
			}
		}
	}
	validVolume := false
	if volumes, ok := spec["volumes"].([]any); ok {
		for _, item := range volumes {
			if v, ok := item.(map[string]any); ok && v["name"] == "ansible-data" {
				if host, ok := v["hostPath"].(map[string]any); ok && host["path"] == "/opt/ansible-runner" && host["type"] == "Directory" {
					validVolume = true
				}
			}
		}
	}
	if !mounted || !validVolume {
		return nil, "", errors.New("Ansible runner does not have the expected repository volume")
	}
	var pods struct {
		Items []struct {
			Spec struct {
				NodeName string `json:"nodeName"`
			} `json:"spec"`
			Status struct {
				Phase      string `json:"phase"`
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := k.json(ctx, "GET", k.path("/api/v1")+"/pods?labelSelector="+url.QueryEscape("app=ansible-runner"), nil, &pods); err != nil {
		return nil, "", fmt.Errorf("find Ansible runner pod: %w", err)
	}
	node := ""
	for _, pod := range pods.Items {
		ready := false
		for _, c := range pod.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				ready = true
			}
		}
		if pod.Status.Phase == "Running" && ready && pod.Spec.NodeName != "" {
			if node != "" {
				return nil, "", errors.New("more than one Ansible runner is ready")
			}
			node = pod.Spec.NodeName
		}
	}
	if node == "" {
		return nil, "", errors.New("no ready Ansible runner pod is available")
	}
	return spec, node, nil
}

// Ready checks only existing runner state and never creates a job.
func (k *KubernetesJob) Ready(ctx context.Context) error { _, _, err := k.runner(ctx); return err }

func randomJobName() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "adaetum-run-" + hex.EncodeToString(b[:]), nil
}

func (k *KubernetesJob) Run(ctx context.Context, spec ExecSpec, output io.Writer) (int, error) {
	allowed := false
	for _, entry := range Catalog() {
		if entry.File == spec.Playbook {
			allowed = true
			break
		}
	}
	hosts, limitErr := parseLimit(spec.Limit)
	if !allowed || limitErr != nil || len(hosts) == 0 {
		return 0, errors.New("Ansible job requires a catalogued playbook and explicit server names")
	}
	spec.Limit = strings.Join(hosts, ",")
	pod, node, err := k.runner(ctx)
	if err != nil {
		return 0, err
	}
	// The Manager already validated every argument and wrote the private file.
	vars, err := os.ReadFile(spec.ExtraVarsFile)
	if err != nil {
		return 0, fmt.Errorf("read private Ansible variables: %w", err)
	}
	if len(vars) > 1<<20 {
		return 0, errors.New("Ansible variables exceed the job limit")
	}
	name, err := randomJobName()
	if err != nil {
		return 0, err
	}
	container := pod["containers"].([]any)[0].(map[string]any)
	args := []string{spec.Playbook, "--limit", spec.Limit, "-e", "@/run/adaetum-job/vars.json"}
	if spec.Check {
		args = append(args, "--check", "--diff")
	}
	container["command"] = []string{"ansible-playbook"}
	container["args"] = args
	container["workingDir"] = "/ansible/ansible"
	mounts, _ := container["volumeMounts"].([]any)
	for _, m := range mounts {
		if v, ok := m.(map[string]any); ok && (v["name"] == "adaetum-job-vars" || v["mountPath"] == "/run/adaetum-job") {
			return 0, errors.New("runner template conflicts with job variables")
		}
	}
	container["volumeMounts"] = append(mounts, map[string]any{"name": "adaetum-job-vars", "mountPath": "/run/adaetum-job", "readOnly": true})
	volumes, _ := pod["volumes"].([]any)
	for _, v := range volumes {
		if m, ok := v.(map[string]any); ok && m["name"] == "adaetum-job-vars" {
			return 0, errors.New("runner template conflicts with job volume")
		}
	}
	pod["volumes"] = append(volumes, map[string]any{"name": "adaetum-job-vars", "secret": map[string]any{"secretName": name, "defaultMode": 256}})
	pod["restartPolicy"] = "Never"
	pod["nodeName"] = node
	// Probes and lifecycle belong to the long-running runner, not an Ansible job.
	for _, key := range []string{"livenessProbe", "readinessProbe", "startupProbe", "lifecycle"} {
		delete(container, key)
	}
	job := map[string]any{"apiVersion": "batch/v1", "kind": "Job", "metadata": map[string]any{"name": name, "namespace": k.namespace(), "labels": map[string]string{"adaetum.io/ansible-job": "true"}}, "spec": map[string]any{"backoffLimit": 0, "activeDeadlineSeconds": 7200, "ttlSecondsAfterFinished": 300, "template": map[string]any{"metadata": map[string]any{"labels": map[string]string{"adaetum.io/ansible-job": "true"}}, "spec": pod}}}
	jobs := k.path("/apis/batch/v1") + "/jobs"
	if err := k.json(ctx, "POST", jobs+"?dryRun=All", job, nil); err != nil {
		return 0, fmt.Errorf("validate Ansible job: %w", err)
	}
	var created struct {
		Metadata struct {
			UID string `json:"uid"`
		} `json:"metadata"`
	}
	if err := k.json(ctx, "POST", jobs, job, &created); err != nil {
		return 0, fmt.Errorf("create Ansible job: %w", err)
	}
	// Cleanup is detached from a cancelled request. The owner reference and Job
	// TTL also clean up if the agent crashes after creating the secret.
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = k.json(clean, "DELETE", k.path("/api/v1")+"/secrets/"+name, nil, nil)
		_ = k.json(clean, "DELETE", jobs+"/"+name, map[string]string{"propagationPolicy": "Background"}, nil)
	}()
	if created.Metadata.UID == "" {
		return 0, errors.New("created Ansible job has no UID")
	}
	secret := map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": name, "namespace": k.namespace(), "ownerReferences": []any{map[string]any{"apiVersion": "batch/v1", "kind": "Job", "name": name, "uid": created.Metadata.UID}}}, "type": "Opaque", "data": map[string][]byte{"vars.json": vars}}
	if err := k.json(ctx, "POST", k.path("/api/v1")+"/secrets", secret, nil); err != nil {
		return 0, fmt.Errorf("prepare private job variables: %w", err)
	}
	return k.wait(ctx, name, output)
}

func (k *KubernetesJob) wait(ctx context.Context, name string, output io.Writer) (int, error) {
	logCtx, stopLogs := context.WithCancel(ctx)
	defer stopLogs()
	interval := k.PollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	podName := ""
	var logsDone chan error
	for {
		var pods struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Status struct {
					Phase             string `json:"phase"`
					ContainerStatuses []struct {
						Name  string `json:"name"`
						State struct {
							Terminated *struct {
								ExitCode int `json:"exitCode"`
							} `json:"terminated"`
						} `json:"state"`
					} `json:"containerStatuses"`
				} `json:"status"`
			} `json:"items"`
		}
		if err := k.json(ctx, "GET", k.path("/api/v1")+"/pods?labelSelector="+url.QueryEscape("job-name="+name), nil, &pods); err != nil {
			return 0, fmt.Errorf("read Ansible job pod: %w", err)
		}
		for _, pod := range pods.Items {
			if podName == "" && pod.Metadata.Name != "" && (pod.Status.Phase == "Running" || pod.Status.Phase == "Succeeded" || pod.Status.Phase == "Failed") {
				podName = pod.Metadata.Name
				logsDone = make(chan error, 1)
				go func() {
					resp, err := k.request(logCtx, "GET", k.path("/api/v1")+"/pods/"+url.PathEscape(podName)+"/log?follow=true&container=ansible-runner", nil)
					if err == nil {
						_, err = io.Copy(output, resp.Body)
						resp.Body.Close()
					}
					logsDone <- err
				}()
			}
			for _, c := range pod.Status.ContainerStatuses {
				if c.Name == "ansible-runner" && c.State.Terminated != nil {
					if logsDone != nil {
						select {
						case err := <-logsDone:
							if err != nil {
								return c.State.Terminated.ExitCode, fmt.Errorf("job completed but its logs could not be read; inspect the run before retrying: %w", err)
							}
						case <-ctx.Done():
							return -1, nil
						case <-time.After(5 * time.Second):
							return c.State.Terminated.ExitCode, errors.New("job completed but its log stream did not finish; inspect the run before retrying")
						}
					}
					return c.State.Terminated.ExitCode, nil
				}
			}
		}
		var job struct {
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
					Reason string `json:"reason"`
				} `json:"conditions"`
			} `json:"status"`
		}
		if err := k.json(ctx, "GET", k.path("/apis/batch/v1")+"/jobs/"+name, nil, &job); err != nil {
			return 0, fmt.Errorf("read Ansible job: %w", err)
		}
		for _, c := range job.Status.Conditions {
			if c.Status == "True" && c.Type == "Failed" {
				return 1, fmt.Errorf("Ansible job failed: %s", c.Reason)
			}
			if c.Status == "True" && c.Type == "Complete" && len(pods.Items) == 0 {
				return 0, errors.New("Ansible job completed but its pod logs are unavailable; inspect the run before retrying")
			}
		}
		select {
		case <-ctx.Done():
			return -1, nil
		case <-ticker.C:
		}
	}
}
