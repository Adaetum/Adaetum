package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/cleanup"
	"github.com/Adaetum/Adaetum/agent/internal/inspect"
)

const (
	auditLogPath = "/var/log/adaetum/image-cleanup.log"
	lockPath     = "/run/adaetum-image-cleanup.lock"
	// Bounded, but not tightly: a server with a backlog of images needs a few
	// runs, not dozens. Use -max 0 for no limit.
	defaultMaxImages = 100
)

// stringList is a repeatable flag.
type stringList []string

func (list *stringList) String() string     { return strings.Join(*list, ",") }
func (list *stringList) Set(v string) error { *list = append(*list, v); return nil }

func runImages(args []string) {
	if len(args) == 0 {
		imagesUsage()
	}
	switch args[0] {
	case "plan":
		runImagesPlan(args[1:])
	case "apply":
		runImagesApply(args[1:])
	case "run":
		runImagesRun(args[1:])
	default:
		imagesUsage()
	}
}

func imagesUsage() {
	fmt.Fprintln(os.Stderr, `usage:
  adaetum-agent images plan  [-ssh user@host] [-level aggressive|careful] [-o plan.json]
                             [careful only: -include-orphans, -allow-registry r ...]
  adaetum-agent images apply -plan plan.json [-dry-run] [-max N] [-yes]
  adaetum-agent images run   [-level aggressive|careful] [-max N] [-only-if-disk-over P] [-check-only]

plan  is read-only. It shows which container images could be removed and why every
      other image is kept. It can inspect another server over SSH. The default level,
      aggressive, plans every image no container on the server uses (anything wanted
      later is downloaded again); careful also keeps images other servers, rollbacks,
      and unreachable registries might need.
run   plans and applies in one step, with no prompt. It is what the scheduled cleanup
      (the image-cleanup Ansible role's timer) runs, using the settings in platform.yaml.
apply deletes images. It runs only on the server itself, as root, and re-checks every
      image against live state before removing it.`)
	os.Exit(2)
}

func runImagesPlan(args []string) {
	flags := flag.NewFlagSet("images plan", flag.ExitOnError)
	sshTarget := flags.String("ssh", "", "plan for this host over SSH (user@host) instead of the local machine")
	level := flags.String("level", inspect.LevelAggressive, "aggressive removes every image no container here uses; careful keeps what the cluster or a recovery might need")
	includeOrphans := flags.Bool("include-orphans", false, "careful level only: also plan images whose app has no version in use")
	output := flags.String("o", "", "write the plan to this file, for `images apply`")
	var registries stringList
	flags.Var(&registries, "allow-registry", "careful level only: registry whose images may be removed (repeatable); replaces the defaults")
	_ = flags.Parse(args)

	if err := cleanup.ValidateLevel(*level); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	plan, err := newInspector(*sshTarget).PlanImageCleanup(context.Background(), inspect.PlanOptions{
		Level:             *level,
		AllowedRegistries: registries,
		IncludeOrphans:    *includeOrphans,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot make a plan:", err)
		os.Exit(1)
	}
	printPlan(plan)

	if *output != "" {
		data, err := json.MarshalIndent(plan, "", "  ")
		if err == nil {
			err = os.WriteFile(*output, data, 0o600)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "cannot write the plan:", err)
			os.Exit(1)
		}
		fmt.Printf("\nPlan saved to %s. It expires %s.\n", *output, plan.ExpiresAt.Local().Format("15:04:05"))
		fmt.Printf("To apply it, on %s:  sudo adaetum-agent images apply -plan %s -dry-run\n", plan.Host, *output)
	}
}

// runImagesRun is the unattended cleanup: plan, then apply, on this server. The
// image-cleanup role's systemd timer runs it with the values from platform.yaml.
// It makes no prompt because nothing is there to answer it; the planner's floors,
// the per-image re-check, the cap, and the audit log are what keep it safe.
func runImagesRun(args []string) {
	flags := flag.NewFlagSet("images run", flag.ExitOnError)
	level := flags.String("level", inspect.LevelAggressive, "aggressive or careful (see `images plan`)")
	maxImages := flags.Int("max", defaultMaxImages, "remove at most this many images in one run (0 for no limit)")
	onlyIfDiskOver := flags.Int("only-if-disk-over", 0, "do nothing unless the disk holding the images is at least this percent full (0 always runs)")
	checkOnly := flags.Bool("check-only", false, "run every check and record what would be removed, without removing anything")
	_ = flags.Parse(args)

	if err := cleanup.ValidateLevel(*level); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *onlyIfDiskOver < 0 || *onlyIfDiskOver > 99 {
		fmt.Fprintln(os.Stderr, "-only-if-disk-over must be from 0 to 99")
		os.Exit(2)
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "images run must run as root on the server itself. Nothing was changed.")
		os.Exit(1)
	}
	if *onlyIfDiskOver > 0 {
		if used := diskUsedPercent("/var/lib/rancher"); used < float64(*onlyIfDiskOver) {
			fmt.Printf("Disk is %.0f%% full, below the %d%% threshold. Nothing to do.\n", used, *onlyIfDiskOver)
			return
		}
	}

	plan, err := inspect.NewInspector().PlanImageCleanup(context.Background(), inspect.PlanOptions{Level: *level})
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot make a plan, so nothing was removed:", err)
		os.Exit(1)
	}
	if len(plan.Items) == 0 {
		fmt.Println("Nothing can be removed safely right now.")
		return
	}
	executePlan(plan, *checkOnly, *maxImages, false)
}

func printPlan(plan inspect.CleanupPlan) {
	fmt.Printf("Image cleanup plan for %s (%s)\n\n", plan.Host, levelName(plan.Options.Level))
	if len(plan.Items) == 0 {
		fmt.Println("Nothing can be removed safely right now.")
	} else {
		fmt.Printf("Would remove %d images, %s as the runtime counts them.\n", len(plan.Items), humanBytes(plan.TotalCompressedBytes()))
		fmt.Println("The disk freed is larger, roughly two to three times that, as the runtime collects the layers.")
		fmt.Println("Removed images are downloaded again from their registry if an app needs them.")
		fmt.Println()

		type group struct {
			count int
			bytes uint64
			kind  string
		}
		byRepo := map[string]*group{}
		for _, item := range plan.Items {
			entry := byRepo[item.Repository]
			if entry == nil {
				entry = &group{kind: item.Category}
				byRepo[item.Repository] = entry
			}
			entry.count++
			entry.bytes += item.CompressedBytes
		}
		names := make([]string, 0, len(byRepo))
		for name := range byRepo {
			names = append(names, name)
		}
		sort.Slice(names, func(a, b int) bool { return byRepo[names[a]].bytes > byRepo[names[b]].bytes })
		for index, name := range names {
			if index == 12 {
				fmt.Printf("  … and %d more apps\n", len(names)-index)
				break
			}
			fmt.Printf("  %-58s %3d images  %9s  (%s)\n", name, byRepo[name].count, humanBytes(byRepo[name].bytes), byRepo[name].kind)
		}
	}

	// Kept images, by reason: the part that shows the plan is conservative.
	type kept struct {
		count int
		bytes uint64
	}
	byReason := map[string]*kept{}
	for _, excluded := range plan.Excluded {
		entry := byReason[excluded.Reason]
		if entry == nil {
			entry = &kept{}
			byReason[excluded.Reason] = entry
		}
		entry.count++
		entry.bytes += excluded.CompressedBytes
	}
	reasons := make([]string, 0, len(byReason))
	for reason := range byReason {
		reasons = append(reasons, reason)
	}
	sort.Slice(reasons, func(a, b int) bool { return byReason[reasons[a]].bytes > byReason[reasons[b]].bytes })
	fmt.Printf("\nKept: %d images\n", len(plan.Excluded))
	for _, reason := range reasons {
		fmt.Printf("  %4d  %9s  %s\n", byReason[reason].count, humanBytes(byReason[reason].bytes), reason)
	}
}

func runImagesApply(args []string) {
	flags := flag.NewFlagSet("images apply", flag.ExitOnError)
	planPath := flags.String("plan", "", "plan file made by `images plan -o`")
	dryRun := flags.Bool("dry-run", false, "run every check and show what would be removed, without removing anything")
	maxImages := flags.Int("max", defaultMaxImages, "remove at most this many images in one run (0 for no limit)")
	assumeYes := flags.Bool("yes", false, "do not ask for confirmation")
	_ = flags.Parse(args)

	fail := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", a...)
		os.Exit(1)
	}

	// Deleting happens only on the server itself, by someone with root: the
	// operating system is the authentication until the agent has its own.
	if os.Geteuid() != 0 {
		fail("images apply must run as root on the server itself (use sudo). Nothing was changed.")
	}
	if *planPath == "" {
		fail("images apply needs -plan. Make one with `images plan -o plan.json`.")
	}
	data, err := os.ReadFile(*planPath)
	if err != nil {
		fail("cannot read the plan: %v", err)
	}
	var plan inspect.CleanupPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		fail("the plan file is not valid: %v", err)
	}
	if len(plan.Items) == 0 {
		fmt.Println("The plan has nothing to remove.")
		return
	}

	executePlan(plan, *dryRun, *maxImages, !*assumeYes)
}

// executePlan removes the images in a plan on this server, re-checking each
// against live state first. It exits the process with a non-zero status when the
// run did not complete cleanly, so a timer or operator can tell.
func executePlan(plan inspect.CleanupPlan, dryRun bool, maxImages int, askFirst bool) {
	fail := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", a...)
		os.Exit(1)
	}
	unlock, err := cleanup.Lock(lockPath)
	if err != nil {
		fail("%v", err)
	}
	defer unlock()

	audit, closeAudit, err := cleanup.OpenAuditLog(auditLogPath)
	if err != nil {
		fail("cannot open the audit log %s, so nothing was removed: %v", auditLogPath, err)
	}
	defer closeAudit()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	inspector := inspect.NewInspector()
	inUse, err := inspector.ContainerUseChecker(ctx)
	if err != nil {
		fail("cannot read this server's containers, so nothing was removed: %v", err)
	}

	if !dryRun && askFirst {
		if !confirm(plan, maxImages) {
			fmt.Println("Cancelled. Nothing was changed.")
			return
		}
	}

	before := freeBytes("/var/lib/rancher")
	deps := cleanup.Deps{
		Hostname: os.Hostname,
		Now:      time.Now,
		Replan:   inspector.PlanImageCleanup,
		InUse:    inUse,
		Remove:   cleanup.RuntimeRemove,
		// Echo each decision as it happens, in addition to recording it.
		Audit: func(entry cleanup.Entry) error {
			if err := audit(entry); err != nil {
				return err
			}
			switch entry.Event {
			case "remove":
				fmt.Printf("removing   %s\n", firstName(entry))
			case "would-remove":
				fmt.Printf("would remove %s\n", firstName(entry))
			case "skip", "fail":
				fmt.Printf("%-10s %s: %s\n", entry.Event, firstName(entry), entry.Reason)
			}
			return nil
		},
	}

	outcome, err := cleanup.Apply(ctx, plan, cleanup.Options{DryRun: dryRun, MaxImages: maxImages}, deps)
	if err != nil {
		fail("%v", err)
	}

	verb := "Removed"
	if outcome.DryRun {
		verb = "Would remove"
	}
	fmt.Printf("\n%s %d images (%s as the runtime counts them). Skipped %d. Failed %d.\n",
		verb, len(outcome.Removed), humanBytes(outcome.RemovedCompressedBytes()), len(outcome.Skipped), len(outcome.Failed))
	if outcome.Stopped != "" {
		fmt.Println("Stopped early:", outcome.Stopped)
	}
	if !outcome.DryRun {
		after := freeBytes("/var/lib/rancher")
		fmt.Printf("Free space on this disk: %s before, %s now. It keeps rising for a few minutes while the runtime collects the layers.\n",
			humanBytes(before), humanBytes(after))
		fmt.Println("Audit log:", auditLogPath)
	}
	if len(outcome.Failed) > 0 || outcome.Stopped != "" && outcome.Stopped != "cancelled" && !strings.HasPrefix(outcome.Stopped, "reached the limit") {
		os.Exit(1)
	}
}

// confirm makes the operator type the server's name. Typing it, not "yes", is
// what stops the command being run in the wrong terminal.
func confirm(plan inspect.CleanupPlan, maxImages int) bool {
	count := len(plan.Items)
	if maxImages > 0 && maxImages < count {
		count = maxImages
	}
	fmt.Printf("This will remove up to %d container images from %s.\n", count, plan.Host)
	fmt.Println("Each is checked again right before it is removed. Images are downloaded again if an app needs them.")
	fmt.Printf("Type the server name (%s) to continue: ", plan.Host)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(answer) == plan.Host
}

func firstName(entry cleanup.Entry) string {
	if len(entry.Names) > 0 {
		return entry.Names[0]
	}
	return entry.Image
}

func freeBytes(path string) uint64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize)
}

func levelName(level string) string {
	if level == inspect.LevelAggressive {
		return "aggressive"
	}
	return "careful"
}

func humanBytes(bytes uint64) string {
	const unit = 1024
	value, suffixes := float64(bytes), []string{"B", "KiB", "MiB", "GiB", "TiB"}
	index := 0
	for value >= unit && index < len(suffixes)-1 {
		value /= unit
		index++
	}
	if index == 0 {
		return fmt.Sprintf("%d B", bytes)
	}
	return fmt.Sprintf("%.1f %s", value, suffixes[index])
}
