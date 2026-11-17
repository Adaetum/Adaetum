package backups

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A schedule is a named backup timer: "daily" is a cron time and how many backups to keep. Each
// one is a Longhorn recurring job in its own group, so a volume is put on a schedule by one
// label and a schedule's time is changed in one place for every volume on it.
//
// SOURCE OF TRUTH. The schedules are declared in the cluster repository
// (pods/longhorn-system/backups/recurring-jobs.yaml). Changing them is a pull request that
// replaces that file with the list below; the agent reads the active list back from the
// cluster's RecurringJobs, so the page shows what is merged, not what is merely proposed.

// Schedule is one named backup timer.
type Schedule struct {
	Name   string `json:"name"`
	Cron   string `json:"cron"`
	Retain int    `json:"retain"`
	// Volumes counts the volumes on this schedule (filled when the state is collected).
	Volumes int `json:"volumes"`
}

const schedulesFile = "pods/longhorn-system/backups/recurring-jobs.yaml"

var (
	scheduleName = regexp.MustCompile(`^[a-z][a-z0-9]{0,19}$`)
	// A cron time is five fields of numbers, lists, ranges, steps and *; names such as "mon" are
	// left out on purpose so what is saved is what the page can show back.
	cronField = regexp.MustCompile(`^(\*|[0-9]+)([-/,][0-9*]+)*$`)
)

func validCron(cron string) bool {
	fields := strings.Fields(cron)
	if len(fields) != 5 {
		return false
	}
	for _, field := range fields {
		if !cronField.MatchString(field) {
			return false
		}
	}
	return true
}

// schedulesFrom lists the backup jobs that are in a schedule group.
func schedulesFrom(jobs []Job) []Schedule {
	found := []Schedule{}
	for _, job := range jobs {
		if job.Task != "backup" {
			continue
		}
		for _, group := range job.Groups {
			if name, ok := strings.CutPrefix(group, GroupPrefix); ok {
				found = append(found, Schedule{Name: name, Cron: job.Cron, Retain: job.Retain})
			}
		}
	}
	sort.Slice(found, func(a, b int) bool { return found[a].Name < found[b].Name })
	return found
}

// DefaultSchedules is what turning scheduled backups on starts with.
func DefaultSchedules() []Schedule {
	return []Schedule{{Name: "daily", Cron: "0 3 * * *", Retain: 7}}
}

func checkSchedules(list []Schedule) error {
	if len(list) == 0 || len(list) > 8 {
		return errors.New("keep between 1 and 8 schedules")
	}
	seen := map[string]bool{}
	for _, item := range list {
		switch {
		case !scheduleName.MatchString(item.Name):
			return fmt.Errorf("%q is not a usable name: use lower-case letters and digits, starting with a letter, up to 20", item.Name)
		case seen[item.Name]:
			return fmt.Errorf("there are two schedules called %s", item.Name)
		case !validCron(item.Cron):
			return fmt.Errorf("%q is not a cron time: use five fields such as 0 3 * * *", item.Cron)
		case item.Retain < 1 || item.Retain > 90:
			return fmt.Errorf("%s: keep between 1 and 90 backups", item.Name)
		}
		seen[item.Name] = true
	}
	return nil
}

// recurringJobsYaml is the file for a list of schedules.
func recurringJobsYaml(list []Schedule) string {
	var out strings.Builder
	out.WriteString(`# One backup job per schedule, each in its own group. A volume is on a schedule when it carries
# the label recurring-job-group.longhorn.io/adaetum-backup-<name>: enabled, which the Backups
# page sets. Edit the list on the Backups page, not by hand.
`)
	for index, item := range list {
		if index > 0 {
			out.WriteString("---\n")
		}
		out.WriteString("apiVersion: longhorn.io/v1beta2\nkind: RecurringJob\nmetadata:\n  name: backup-" + item.Name + "\n  namespace: longhorn-system\nspec:\n  name: backup-" + item.Name + "\n  task: backup\n  cron: " + strconv.Quote(item.Cron) + "\n  retain: " + strconv.Itoa(item.Retain) + "\n  concurrency: 1\n  groups: [" + GroupPrefix + item.Name + "]\n")
	}
	return out.String()
}

// SchedulesAPI serves PUT /api/v1/backups/schedules: replace the list of schedules.
type SchedulesAPI struct {
	Get         Getter
	Propose     Proposer
	Audit       func(entry any) error
	RequireUser func(http.Handler) http.Handler
	Who         func(*http.Request) string
}

// Register adds the route behind the sign-in guard.
func (a *SchedulesAPI) Register(mux *http.ServeMux) {
	var handler http.Handler = http.HandlerFunc(a.serve)
	if a.RequireUser != nil {
		handler = a.RequireUser(handler)
	}
	mux.Handle("PUT /api/v1/backups/schedules", handler)
}

func (a *SchedulesAPI) serve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Schedules []Schedule `json:"schedules"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil {
		reply(w, http.StatusBadRequest, map[string]any{"error": "unreadable request"})
		return
	}
	if err := checkSchedules(body.Schedules); err != nil {
		reply(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	state, err := Collect(r.Context(), a.Get, time.Now())
	if err != nil {
		reply(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	if len(state.Schedules) == 0 {
		reply(w, http.StatusConflict, map[string]any{"error": "Scheduled backups are not set up yet: choose storage for them first, and merge that pull request."})
		return
	}
	// A schedule with volumes on it cannot vanish: those volumes would silently stop being backed up.
	kept := map[string]bool{}
	for _, item := range body.Schedules {
		kept[item.Name] = true
	}
	for _, existing := range state.Schedules {
		if !kept[existing.Name] && scheduleVolumes(state, existing.Name) > 0 {
			reply(w, http.StatusConflict, map[string]any{"error": "Volumes are still on the " + existing.Name + " schedule. Move them to another mode first."})
			return
		}
	}
	if a.Audit == nil || a.Propose == nil {
		reply(w, http.StatusServiceUnavailable, map[string]any{"error": "Changes are refused because there is no record to write them to."})
		return
	}
	who := ""
	if a.Who != nil {
		who = a.Who(r)
	}
	if err := a.Audit(map[string]any{"time": time.Now().UTC(), "action": "schedules", "schedules": body.Schedules, "by": who}); err != nil {
		reply(w, http.StatusServiceUnavailable, map[string]any{"error": "Changes are refused because the record cannot be written."})
		return
	}
	proposal, _, err := a.Propose(r.Context(), Changes{Prefix: "adaetum/schedules", Title: "Change the backup schedules", Body: "Replaces the backup schedules with the list set on the Backups page. Volumes on a schedule pick up the change when this is merged.\n", Update: map[string]string{schedulesFile: recurringJobsYaml(body.Schedules)}})
	if err != nil {
		reply(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	reply(w, http.StatusOK, map[string]string{"proposalUrl": proposal})
}

func scheduleVolumes(state State, name string) int {
	count := 0
	for _, volume := range state.Volumes {
		if volume.Schedule == name {
			count++
		}
	}
	return count
}

// fillScheduleCounts sets each schedule's volume count.
func fillScheduleCounts(state *State) {
	for index := range state.Schedules {
		state.Schedules[index].Volumes = scheduleVolumes(*state, state.Schedules[index].Name)
	}
}

var _ = context.Background
