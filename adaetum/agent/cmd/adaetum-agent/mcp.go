package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/agentapi"
	"github.com/Adaetum/Adaetum/agent/internal/automation"
	"github.com/Adaetum/Adaetum/agent/internal/cleanup"
	"github.com/Adaetum/Adaetum/agent/internal/clustertools"
	"github.com/Adaetum/Adaetum/agent/internal/inspect"
	"github.com/Adaetum/Adaetum/agent/internal/mcp"
	"github.com/Adaetum/Adaetum/agent/internal/membership"
	"github.com/Adaetum/Adaetum/agent/internal/policy"
)

// maxPlanItemsShown keeps a tool result small enough for a model to read. The
// totals always cover the whole plan.
const maxPlanItemsShown = 40

// runMCP serves the agent's capabilities to an MCP client over stdio. Almost all of them are read-only; the exception is the opt-in security-switch tools (see mcpsecurity.go).
// Add a capability here only by wrapping a function the HTTP API already uses,
// and only if it cannot change anything.
func runMCP(args []string) {
	flags := flag.NewFlagSet("mcp", flag.ExitOnError)
	sshTarget := flags.String("ssh", "", "inspect this host over SSH (user@host) instead of the local machine")
	clusterURL := flags.String("cluster", "", "URL of the cluster agent, to give an assistant everything the cluster agent's page can do (cluster, playbooks, security switches, memory)")
	fingerprint := flags.String("fingerprint", "", "the cluster agent certificate's SHA-256 fingerprint (required with -cluster)")
	tokenFile := flags.String("token-file", "", "file holding the cluster agent's access token (required with -cluster)")
	allowTurnOn := flags.Bool("allow-security-changes", false, "let the assistant turn security switches ON (off by default; turning them off is always allowed)")
	_ = flags.Parse(args)

	var clusterAPI *agentapi.Client
	if *clusterURL != "" {
		token, reason := checkToken(*tokenFile, realSystem())
		if *fingerprint == "" || reason != "" {
			if reason == "" {
				reason = "-fingerprint is required with -cluster."
			}
			fmt.Fprintln(os.Stderr, "mcp: cannot offer the security tools:", reason)
			os.Exit(2)
		}
		clusterAPI = &agentapi.Client{HTTP: membership.Link{URL: *clusterURL, Fingerprint: *fingerprint}.HTTPClient(), URL: *clusterURL, Token: token}
	} else if *allowTurnOn {
		fmt.Fprintln(os.Stderr, "mcp: -allow-security-changes needs -cluster, -fingerprint and -token-file.")
		os.Exit(2)
	}

	inspector := newInspector(*sshTarget)
	collector := &automation.Collector{Source: inspector, Now: time.Now}
	tools := []mcp.Tool{
		{
			Name:        "get_host_report",
			Description: "Report on the host and its RKE2 cluster: servers, apps, pods, deployments, storage, certificates, and warnings. Read-only.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Call: func(ctx context.Context, _ json.RawMessage) (any, error) {
				return inspector.Collect(ctx), nil
			},
		},
		{
			Name:        "get_automation_status",
			Description: "State of the Ansible runner: whether recent runs succeeded, the latest error, available playbooks, and the policies platform.yaml declares. Read-only.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Call: func(ctx context.Context, _ json.RawMessage) (any, error) {
				return collector.Collect(ctx), nil
			},
		},
		{
			Name:        "plan_image_cleanup",
			Description: "Show which unused container images could be removed from the server and why every other image is kept. This only plans; nothing is removed.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"level": map[string]any{
						"type":        "string",
						"enum":        []string{inspect.LevelAggressive, inspect.LevelCareful},
						"description": "aggressive plans every image no container here uses; careful also keeps images a rollback or another server might need. Default aggressive.",
					},
				},
			},
			Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var input struct {
					Level string `json:"level"`
				}
				if len(raw) > 0 {
					if err := json.Unmarshal(raw, &input); err != nil {
						return nil, fmt.Errorf("arguments are not valid: %w", err)
					}
				}
				if input.Level == "" {
					input.Level = inspect.LevelAggressive
				}
				if err := cleanup.ValidateLevel(input.Level); err != nil {
					return nil, err
				}
				plan, err := inspector.PlanImageCleanup(ctx, inspect.PlanOptions{Level: input.Level})
				if err != nil {
					return nil, err
				}
				return summarizePlan(plan), nil
			},
		},
	}
	if clusterAPI != nil {
		// Everything a person can do on the cluster agent's page, plus memory. The
		// agent applies its own rules to every call; see internal/clustertools.
		tools = append(tools, clustertools.Tools(*clusterAPI, clustertools.Options{Via: policy.ViaMCP, AllowSecurityOn: *allowTurnOn})...)
		fmt.Fprintf(os.Stderr, "adaetum-agent MCP: cluster tools on (turning security settings ON: %v)\n", *allowTurnOn)
	}
	server := mcp.NewServer("adaetum-agent", agentVersion, tools)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// stdout belongs to the protocol; anything for a human goes to stderr.
	fmt.Fprintln(os.Stderr, "adaetum-agent MCP server ready on stdio (read-only)")
	if err := server.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type planSummary struct {
	Host              string         `json:"host"`
	Level             string         `json:"level"`
	WouldRemove       int            `json:"wouldRemove"`
	CompressedBytes   uint64         `json:"compressedBytes"`
	Note              string         `json:"note"`
	KeptByReason      map[string]int `json:"keptByReason"`
	LargestRemovals   []planItem     `json:"largestRemovals"`
	RemovalsNotListed int            `json:"removalsNotListed"`
	Notes             []string       `json:"notes,omitempty"`
}

type planItem struct {
	Repository      string `json:"repository"`
	Category        string `json:"category"`
	CompressedBytes uint64 `json:"compressedBytes"`
}

// summarizePlan reduces a plan to what a model can use: totals, why images are
// kept, and the biggest removals.
func summarizePlan(plan inspect.CleanupPlan) planSummary {
	summary := planSummary{
		Host:            plan.Host,
		Level:           plan.Options.Level,
		WouldRemove:     len(plan.Items),
		CompressedBytes: plan.TotalCompressedBytes(),
		Note:            "Sizes are as the runtime counts them (compressed); the disk freed is roughly two to three times larger. Removed images are downloaded again if needed. This is a plan only.",
		KeptByReason:    map[string]int{},
		Notes:           plan.Notes,
	}
	for _, excluded := range plan.Excluded {
		summary.KeptByReason[excluded.Reason]++
	}
	items := make([]planItem, 0, len(plan.Items))
	for _, item := range plan.Items {
		items = append(items, planItem{Repository: item.Repository, Category: item.Category, CompressedBytes: item.CompressedBytes})
	}
	sort.Slice(items, func(a, b int) bool { return items[a].CompressedBytes > items[b].CompressedBytes })
	if len(items) > maxPlanItemsShown {
		summary.RemovalsNotListed = len(items) - maxPlanItemsShown
		items = items[:maxPlanItemsShown]
	}
	summary.LargestRemovals = items
	return summary
}
