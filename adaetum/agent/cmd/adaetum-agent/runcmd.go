package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/Adaetum/Adaetum/agent/internal/runs"
)

// Exit codes of `adaetum-agent run`, so a script or a person can tell what happened.
const (
	exitDone     = 0
	exitFailed   = 1
	exitUsage    = 2
	exitNeedsYou = 3 // stopped on purpose for a person to decide
	exitStopped  = 4
)

// listFlag collects a repeated flag (-input a=b -input c=d).
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// runRun is `adaetum-agent run`: run one procedure from start to finish in the
// foreground, on this machine, with no daemon and no database. It is how a chunk of
// first boot is exercised alone: same procedure, same steps and checks as the agent's
// own, with the log lines on stderr and a summary on stdout.
//
// It keeps its journal in memory, so it can run beside the agent service (which owns the
// database) and leaves nothing behind but what the procedure itself changes. It changes
// the machine exactly as the procedure says, as whoever runs it, so a procedure that
// needs root needs `sudo`.
func runRun(args []string) {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	list := flags.Bool("list", false, "list the procedures and their steps, then exit")
	var inputs, approve listFlag
	flags.Var(&inputs, "input", "a setting for the run, as name=value (repeat for more). Recorded in clear: never a secret")
	flags.Var(&approve, "approve", "allow one destructive step by name (repeat for more). Without it the run stops at that step and asks")
	flags.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: adaetum-agent run [-input name=value]... [-approve step]... <procedure>")
		fmt.Fprintln(os.Stderr, "       adaetum-agent run -list")
		fmt.Fprintln(os.Stderr, "Put the flags before the procedure name.")
		fmt.Fprintln(os.Stderr, "exit status: 0 done, 1 failed, 2 usage, 3 needs a person, 4 stopped")
		flags.PrintDefaults()
	}
	_ = flags.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runOnce(ctx, os.Stdout, os.Stderr, hostProcedures(newInspector("")), *list, flags.Arg(0), inputs, approve))
}

// runOnce does the work of runRun against any set of procedures and writers, so it can be
// tested without a real machine. It returns the exit code.
func runOnce(ctx context.Context, out, logs io.Writer, procedures []*runs.Procedure, list bool, name string, inputs, approve []string) int {
	if list {
		listProcedures(out, procedures)
		return exitDone
	}
	if name == "" {
		fmt.Fprintln(logs, "name a procedure to run, or use -list to see them")
		return exitUsage
	}
	settings := map[string]string{}
	for _, item := range inputs {
		key, value, found := strings.Cut(item, "=")
		if !found || key == "" {
			fmt.Fprintf(logs, "-input %q should look like name=value\n", item)
			return exitUsage
		}
		settings[key] = value
	}

	manager, err := runs.NewManager(runs.Options{
		Journal:  &runs.MemoryJournal{},
		OnRecord: func(rec runs.Record) { fmt.Fprintln(logs, rec.Line()) },
	})
	if err != nil {
		fmt.Fprintln(logs, err)
		return exitFailed
	}
	defer manager.Close()
	known := []string{}
	for _, procedure := range procedures {
		if err := manager.Register(procedure); err != nil {
			fmt.Fprintf(logs, "procedure %s is not usable: %v\n", procedure.Name, err)
			continue
		}
		known = append(known, procedure.Name)
	}

	id, err := manager.Start(ctx, runs.StartRequest{Procedure: name, Inputs: settings, Approved: approve, StartedBy: "run"})
	if err != nil {
		fmt.Fprintln(logs, err)
		if strings.Contains(err.Error(), "unknown procedure") {
			sort.Strings(known)
			fmt.Fprintln(logs, "available:", strings.Join(known, ", "))
		}
		return exitUsage
	}
	// Ctrl-C stops the run cleanly and records it as stopped.
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			manager.Cancel(id)
		case <-done:
		}
	}()
	_ = manager.Wait(context.Background(), id)
	close(done)

	snap, _ := manager.Get(id)
	summarize(out, snap)
	switch snap.State {
	case runs.StateSucceeded:
		return exitDone
	case runs.StateNeedsHuman:
		return exitNeedsYou
	case runs.StateCancelled:
		return exitStopped
	}
	return exitFailed
}

func listProcedures(out io.Writer, procedures []*runs.Procedure) {
	manager, _ := runs.NewManager(runs.Options{Journal: &runs.MemoryJournal{}})
	defer manager.Close()
	for _, procedure := range procedures {
		_ = manager.Register(procedure)
	}
	for _, info := range manager.Procedures() {
		fmt.Fprintf(out, "%s\n", info.Name)
		if info.Description != "" {
			fmt.Fprintf(out, "  %s\n", info.Description)
		}
		for _, step := range info.Steps {
			note := ""
			if step.Kind != "convergent" {
				note = "  (" + step.Kind + ")"
			}
			fmt.Fprintf(out, "    - %s%s\n", step.Name, note)
		}
	}
}

// summarize prints the result for the person at the terminal: one line per step, then, if
// the run stopped, where and why.
func summarize(out io.Writer, snap runs.Snapshot) {
	fmt.Fprintf(out, "%s: %s\n", snap.Procedure, snap.State)
	width := 0
	for _, step := range snap.Steps {
		width = max(width, len(step.Name))
	}
	for _, step := range snap.Steps {
		fmt.Fprintf(out, "  %-*s  %s\n", width, step.Name, step.State)
	}
	if snap.State == runs.StateSucceeded {
		return
	}
	where := ""
	if snap.FailedStep != "" {
		where = "at " + snap.FailedStep + ": "
	}
	why := snap.Detail
	if why == "" {
		why = snap.Code
	}
	fmt.Fprintf(out, "stopped %s%s\n", where, why)
}
