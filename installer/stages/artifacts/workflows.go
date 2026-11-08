package artifacts

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/Adaetum/Adaetum/installer/githubapi"
)

func base64Std(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

type dispatched struct {
	file string
	id   int64
}

// dispatch triggers a workflow and finds the run it started: the first run id that
// was not there before.
func (s *Step) dispatch(ctx context.Context, gh githubapi.Client, repo, branch, file string, inputs map[string]string, say func(string)) (int64, error) {
	before := map[int64]bool{}
	if ids, err := gh.RunIDs(ctx, repo, file, branch); err == nil {
		for _, id := range ids {
			before[id] = true
		}
	}
	if err := gh.Dispatch(ctx, repo, file, branch, inputs); err != nil {
		return 0, fmt.Errorf("failed to trigger %s: %w", file, err)
	}
	for try := 0; try < 30; try++ {
		if ids, err := gh.RunIDs(ctx, repo, file, branch); err == nil {
			for _, id := range ids {
				if !before[id] {
					return id, nil
				}
			}
		}
		s.Sleep(s.PollEvery / 2)
	}
	return 0, errors.New("triggered " + file + " but could not find its run")
}

// wait watches a run until it completes. It returns an error unless it succeeded.
func (s *Step) wait(ctx context.Context, gh githubapi.Client, repo string, d dispatched, say func(string)) error {
	for poll := 0; poll < s.MaxPolls; poll++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		run, err := gh.Run(ctx, repo, d.id)
		if err == nil && run.Status == "completed" {
			if run.Conclusion == "success" {
				say(fmt.Sprintf("%s succeeded (%s).", d.file, run.HTMLURL))
				return nil
			}
			return fmt.Errorf("%s failed (conclusion=%s). Run: %s", d.file, run.Conclusion, run.HTMLURL)
		}
		if err == nil && poll%6 == 0 {
			say(fmt.Sprintf("%s run %d: %s", d.file, d.id, run.Status))
		}
		s.Sleep(s.PollEvery)
	}
	return fmt.Errorf("%s timed out (run %d).", d.file, d.id)
}
