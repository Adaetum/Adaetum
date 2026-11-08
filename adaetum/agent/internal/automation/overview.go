package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Adaetum/Adaetum/agent/internal/inspect"
)

const (
	runnerDeployment = "ansible-runner"
	runsKept         = 50
)

// Source is what the collector reads. *inspect.Inspector satisfies it, over SSH
// in development or locally on a server; tests supply fixtures.
type Source interface {
	KubectlJSON(ctx context.Context, resources string, extra ...string) ([]byte, error)
	ReadRunnerPod(ctx context.Context, pod string, what inspect.RunnerRead, arg string) (string, error)
}

// RunnerPod is the running pod of the Ansible runner.
type RunnerPod struct {
	Name      string     `json:"name"`
	Phase     string     `json:"phase"`
	Node      string     `json:"node"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	Restarts  int        `json:"restarts"`
}

// Runner describes the Ansible runner deployment: what it runs and how often.
type Runner struct {
	Found      bool   `json:"found"`
	Image      string `json:"image,omitempty"`
	PullPolicy string `json:"pullPolicy,omitempty"`
	// Schedule is the cron expression of the runner's own loop.
	Schedule  string     `json:"schedule,omitempty"`
	Playbook  string     `json:"playbook,omitempty"`
	Inventory string     `json:"inventory,omitempty"`
	Replicas  int        `json:"replicas"`
	Ready     int        `json:"ready"`
	Pod       *RunnerPod `json:"pod,omitempty"`
}

// Playbook is one playbook available to the runner.
type Playbook struct {
	File        string   `json:"file"`
	Description string   `json:"description,omitempty"`
	Plays       []string `json:"plays,omitempty"`
}

// Policy declarations mirror spec.* in platform.yaml. A nil pointer means the
// profile does not declare that policy.
type HostMaintenancePolicy struct {
	Enabled      bool   `json:"enabled"`
	UpdatePolicy string `json:"updatePolicy"`
	RebootWindow string `json:"rebootWindow"`
}

// ImageCleanupPolicy is declared in platform.yaml and enforced by an Ansible role.
type ImageCleanupPolicy struct {
	Enabled               bool   `json:"enabled" yaml:"enabled"`
	Level                 string `json:"level" yaml:"level"`
	Time                  string `json:"time" yaml:"time"`
	MaxImagesPerRun       int    `json:"maxImagesPerRun" yaml:"maxImagesPerRun"`
	OnlyIfDiskOverPercent int    `json:"onlyIfDiskOverPercent" yaml:"onlyIfDiskOverPercent"`
	CheckOnly             bool   `json:"checkOnly" yaml:"checkOnly"`
}

// ImageSharingPolicy is declared in platform.yaml and enforced by an Ansible role.
type ImageSharingPolicy struct {
	Enabled    bool     `json:"enabled" yaml:"enabled"`
	Registries []string `json:"registries" yaml:"registries"`
}

// Declared is the policy platform.yaml asks for.
type Declared struct {
	HostMaintenance *HostMaintenancePolicy `json:"hostMaintenance,omitempty"`
	ImageCleanup    *ImageCleanupPolicy    `json:"imageCleanup,omitempty"`
	ImageSharing    *ImageSharingPolicy    `json:"imageSharing,omitempty"`
}

// Overview is everything the Automation tab shows.
type Overview struct {
	CollectedAt time.Time  `json:"collectedAt"`
	Runner      Runner     `json:"runner"`
	Health      Health     `json:"health"`
	Runs        []Run      `json:"runs"`
	Playbooks   []Playbook `json:"playbooks"`
	Declared    *Declared  `json:"declared,omitempty"`
	// Errors lists parts that could not be read; the rest is still shown.
	Errors []string `json:"errors,omitempty"`
}

// Collector gathers an Overview.
type Collector struct {
	Source Source
	Now    func() time.Time
}

// Collect never returns an error: each part that fails is recorded and the rest
// still reports, so a broken runner is itself something the tab can show.
func (c *Collector) Collect(ctx context.Context) Overview {
	overview := Overview{CollectedAt: c.Now().UTC(), Runs: []Run{}, Playbooks: []Playbook{}}
	fail := func(what string, err error) { overview.Errors = append(overview.Errors, what+": "+err.Error()) }

	runner, err := c.collectRunner(ctx)
	if err != nil {
		fail("runner", err)
	}
	overview.Runner = runner

	// Everything inside the pod needs a running pod to read from.
	if runner.Pod == nil || runner.Pod.Phase != "Running" {
		return overview
	}
	pod := runner.Pod.Name

	if log, err := c.Source.ReadRunnerPod(ctx, pod, inspect.ReadRunLog, ""); err != nil {
		fail("run history", err)
	} else {
		overview.Runs = ParseRuns(log, runsKept)
		overview.Health = Summarize(overview.Runs)
	}

	if playbooks, err := c.collectPlaybooks(ctx, pod); err != nil {
		fail("playbooks", err)
	} else {
		overview.Playbooks = playbooks
	}

	if profile, err := c.Source.ReadRunnerPod(ctx, pod, inspect.ReadPlatformProfile, ""); err != nil {
		fail("platform profile", err)
	} else if declared, err := ParseDeclared(profile); err != nil {
		fail("platform profile", err)
	} else {
		overview.Declared = declared
	}
	return overview
}

func (c *Collector) collectRunner(ctx context.Context) (Runner, error) {
	data, err := c.Source.KubectlJSON(ctx, "deployments", "-n", inspect.RunnerNamespace())
	if err != nil {
		return Runner{}, err
	}
	runner, err := parseRunnerDeployment(data)
	if err != nil || !runner.Found {
		return runner, err
	}
	pods, err := c.Source.KubectlJSON(ctx, "pods", "-n", inspect.RunnerNamespace())
	if err != nil {
		return runner, err
	}
	runner.Pod = parseRunnerPod(pods)
	return runner, nil
}

func parseRunnerDeployment(data []byte) (Runner, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Replicas int `json:"replicas"`
				Template struct {
					Spec struct {
						Containers []struct {
							Image           string `json:"image"`
							ImagePullPolicy string `json:"imagePullPolicy"`
							Env             []struct {
								Name  string `json:"name"`
								Value string `json:"value"`
							} `json:"env"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
			Status struct {
				ReadyReplicas int `json:"readyReplicas"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return Runner{}, err
	}
	for _, item := range list.Items {
		if item.Metadata.Name != runnerDeployment || len(item.Spec.Template.Spec.Containers) == 0 {
			continue
		}
		container := item.Spec.Template.Spec.Containers[0]
		runner := Runner{Found: true, Image: container.Image, PullPolicy: container.ImagePullPolicy, Replicas: item.Spec.Replicas, Ready: item.Status.ReadyReplicas}
		// Only plain values are read. Environment entries that come from a secret
		// have no Value, and secrets are never read here.
		for _, variable := range container.Env {
			switch variable.Name {
			case "CRON_SCHEDULE":
				runner.Schedule = variable.Value
			case "ANSIBLE_PLAYBOOK":
				runner.Playbook = variable.Value
			case "ANSIBLE_INVENTORY":
				runner.Inventory = variable.Value
			}
		}
		return runner, nil
	}
	return Runner{}, nil
}

func parseRunnerPod(data []byte) *RunnerPod {
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				NodeName string `json:"nodeName"`
			} `json:"spec"`
			Status struct {
				Phase             string     `json:"phase"`
				StartTime         *time.Time `json:"startTime"`
				ContainerStatuses []struct {
					RestartCount int `json:"restartCount"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(data, &list) != nil {
		return nil
	}
	var best *RunnerPod
	for _, item := range list.Items {
		if !strings.HasPrefix(item.Metadata.Name, runnerDeployment+"-") {
			continue
		}
		pod := &RunnerPod{Name: item.Metadata.Name, Phase: item.Status.Phase, Node: item.Spec.NodeName, StartedAt: item.Status.StartTime}
		for _, status := range item.Status.ContainerStatuses {
			pod.Restarts += status.RestartCount
		}
		// Prefer a running pod; during a rollout there may be an old one too.
		if best == nil || (pod.Phase == "Running" && best.Phase != "Running") {
			best = pod
		}
	}
	return best
}

func (c *Collector) collectPlaybooks(ctx context.Context, pod string) ([]Playbook, error) {
	list, err := c.Source.ReadRunnerPod(ctx, pod, inspect.ReadPlaybookList, "")
	if err != nil {
		return nil, err
	}
	var playbooks []Playbook
	for _, name := range strings.Fields(list) {
		// A file the fixed read refuses (odd characters) is skipped, not fatal.
		// Underscore-prefixed playbooks are internal building blocks.
		if strings.HasPrefix(name, "_") || !(strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")) {
			continue
		}
		head, err := c.Source.ReadRunnerPod(ctx, pod, inspect.ReadPlaybookHead, name)
		if err != nil {
			playbooks = append(playbooks, Playbook{File: name})
			continue
		}
		playbooks = append(playbooks, ParsePlaybookHead(name, head))
	}
	sort.Slice(playbooks, func(a, b int) bool { return playbooks[a].File < playbooks[b].File })
	return playbooks, nil
}

var (
	playName   = regexp.MustCompile(`^- name:\s*(.+?)\s*$`)
	importLine = regexp.MustCompile(`^- import_playbook:\s*(\S+)`)
)

// ParsePlaybookHead reads a playbook's description (the comment block at the top)
// and the names of its plays from the first lines of the file. It does not parse
// YAML: a header is all that is needed, and a partial file would not parse.
func ParsePlaybookHead(file, head string) Playbook {
	playbook := Playbook{File: file}
	var comments []string
	collecting := true
	for _, line := range strings.Split(head, "\n") {
		trimmed := strings.TrimSpace(line)
		if collecting {
			switch {
			case trimmed == "---" || trimmed == "":
				continue
			case strings.HasPrefix(trimmed, "#"):
				comments = append(comments, strings.TrimSpace(strings.TrimPrefix(trimmed, "#")))
				continue
			default:
				collecting = false
			}
		}
		if match := playName.FindStringSubmatch(line); match != nil {
			playbook.Plays = append(playbook.Plays, strings.Trim(match[1], `"'`))
		} else if match := importLine.FindStringSubmatch(line); match != nil {
			playbook.Plays = append(playbook.Plays, "runs "+strings.TrimSuffix(match[1], ".yml"))
		}
	}
	playbook.Description = strings.Join(comments, " ")
	if len(playbook.Description) > 400 {
		playbook.Description = playbook.Description[:400] + "…"
	}
	return playbook
}

// ParseDeclared reads the policies platform.yaml declares.
func ParseDeclared(profile string) (*Declared, error) {
	var document struct {
		Spec struct {
			HostMaintenance struct {
				Enabled bool `yaml:"enabled"`
				Updates struct {
					Policy string `yaml:"policy"`
				} `yaml:"updates"`
				Reboots struct {
					StartTime string `yaml:"startTime"`
					EndTime   string `yaml:"endTime"`
					TimeZone  string `yaml:"timeZone"`
				} `yaml:"reboots"`
			} `yaml:"hostMaintenance"`
			ImageCleanup *ImageCleanupPolicy `yaml:"imageCleanup"`
			ImageSharing *ImageSharingPolicy `yaml:"imageSharing"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(profile), &document); err != nil {
		return nil, fmt.Errorf("platform.yaml is not valid YAML: %w", err)
	}
	declared := &Declared{ImageCleanup: document.Spec.ImageCleanup, ImageSharing: document.Spec.ImageSharing}
	if document.Spec.HostMaintenance.Updates.Policy != "" {
		reboots := document.Spec.HostMaintenance.Reboots
		declared.HostMaintenance = &HostMaintenancePolicy{
			Enabled:      document.Spec.HostMaintenance.Enabled,
			UpdatePolicy: document.Spec.HostMaintenance.Updates.Policy,
			RebootWindow: strings.TrimSpace(fmt.Sprintf("%s to %s %s", reboots.StartTime, reboots.EndTime, reboots.TimeZone)),
		}
	}
	return declared, nil
}
