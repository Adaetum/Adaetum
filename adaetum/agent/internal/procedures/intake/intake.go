// Package intake is the "intake" procedure: the old Phase 10. It rejects an unusable
// first-boot runtime payload before anything creates state on this machine. It reads and
// changes nothing, so it is safe to run at any time, alone (`adaetum-agent run intake`) or
// as part of the first-boot procedure.
//
// It ports the rules the old `validate-bootstrap-runtime-env.sh` applies to a payload on
// an installed host:
//   - the file exists, is not empty, and every line is a comment, blank, or KEY=value
//     (no `export`);
//   - the backup passphrase is present, backup to R2 is switched on, and the backup URL is
//     an http(s) address ending in /backup;
//   - the two repository tokens look like git-capable GitHub tokens;
//   - the bundle URL is a stable address (no ?v= cache busting).
//
// NOT ported yet, and said plainly so nobody assumes otherwise:
//   - validating the platform profile (platform.yaml). That is the Python profile
//     validator; it needs its own Go port, or a decision to run it as a playbook;
//   - the checks that compare the payload to the operator's .env, which exist only on the
//     operator's computer and never on an installed host (the installer runs them).
//
// A message names the keys that are wrong and never their values: the payload holds
// tokens and passphrases.
package intake

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Adaetum/Adaetum/agent/internal/runs"
)

// Name is the procedure's name.
const Name = "intake"

// InputPayload is the payload file to check. Default /etc/bootstrap-runtime.env.
const InputPayload = "payload_file"

const defaultPayload = "/etc/bootstrap-runtime.env"

// Reader reads a file from the machine.
type Reader interface {
	Read(path string) ([]byte, error)
}

var (
	safePath  = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,200}$`)
	keyName   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	backupURL = regexp.MustCompile(`^https?://[^\s]+/backup$`)
)

// New returns the procedure, reading the payload through files.
func New(files Reader) *runs.Procedure {
	return &runs.Procedure{
		Name:        Name,
		Description: "Checks that the first-boot runtime payload is usable before anything is created. It changes nothing.",
		Steps: []runs.Step{
			runs.Validation("payload-format", nil, runs.CodeConfig, func(_ context.Context, env runs.Env) (string, error) {
				_, problem, err := load(files, env)
				return problem, err
			}),
			runs.Validation("payload-contract", []string{"payload-format"}, runs.CodeConfig, func(_ context.Context, env runs.Env) (string, error) {
				values, problem, err := load(files, env)
				if err != nil || problem != "" {
					return problem, err
				}
				return contract(values), nil
			}),
		},
	}
}

// load reads and parses the payload. problem is a sentence about the file's format;
// err means the check could not be made.
func load(files Reader, env runs.Env) (values map[string]string, problem string, err error) {
	path := defaultPayload
	if value := env.Inputs[InputPayload]; value != "" {
		path = value
	}
	if !safePath.MatchString(path) || strings.Contains(path, "..") {
		return nil, "", runs.BrokenBecause(runs.CodeConfig, fmt.Sprintf("%s must be an absolute path of ordinary characters, not %q", InputPayload, path))
	}
	data, readErr := files.Read(path)
	if readErr != nil {
		return nil, fmt.Sprintf("the runtime payload %s cannot be read", path), nil
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil, fmt.Sprintf("the runtime payload %s is empty", path), nil
	}
	values = map[string]string{}
	for number, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
			return nil, fmt.Sprintf("the runtime payload has an \"export\" on line %d; only KEY=value lines are allowed", number+1), nil
		}
		key, value, found := strings.Cut(line, "=")
		if !found || !keyName.MatchString(strings.TrimSpace(key)) {
			// The line number only: the line itself may be a secret.
			return nil, fmt.Sprintf("line %d of the runtime payload is not a comment or KEY=value", number+1), nil
		}
		key = strings.TrimSpace(key)
		if _, seen := values[key]; !seen { // the first one wins, as the old script's awk did
			values[key] = normalize(value)
		}
	}
	return values, "", nil
}

// normalize trims the value and removes one pair of surrounding quotes.
func normalize(value string) string {
	value = strings.TrimSpace(strings.NewReplacer("\r", "", "\n", "").Replace(value))
	if len(value) >= 2 && (value[0] == '"' && value[len(value)-1] == '"' || value[0] == '\'' && value[len(value)-1] == '\'') {
		value = value[1 : len(value)-1]
	}
	return value
}

func gitCapable(token string) bool {
	for _, prefix := range []string{"github_pat_", "ghp_", "gho_", "ghu_", "ghs_"} {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
}

// contract checks the payload's contents and returns every problem found, as one
// sentence, so a person can fix them together. Keys only, never values. The wording is
// short on purpose: a recorded detail is capped at 300 characters, and all six problems
// together must fit.
func contract(values map[string]string) string {
	var problems []string
	if values["BOOTSTRAP_BACKUP_PASSPHRASE"] == "" && values["BOOTSTRAP_BACKUP_PASSPHRASE_B64"] == "" {
		problems = append(problems, "BOOTSTRAP_BACKUP_PASSPHRASE (or _B64) is missing")
	}
	switch strings.ToLower(values["BOOTSTRAP_BACKUP_TO_R2"]) {
	case "1", "true", "yes", "on":
	default:
		problems = append(problems, "BOOTSTRAP_BACKUP_TO_R2 is not enabled")
	}
	if !backupURL.MatchString(values["BOOTSTRAP_BACKUP_URL"]) {
		problems = append(problems, "BOOTSTRAP_BACKUP_URL is not an http(s) /backup address")
	}
	for _, key := range []string{"ARGOCD_GITHUB_TOKEN", "GITEA_SEED_SOURCE_TOKEN"} {
		if !gitCapable(values[key]) {
			problems = append(problems, key+" is missing or not a git token")
		}
	}
	if strings.Contains(values["BOOTSTRAP_BUNDLE_URL"], "?v=") {
		problems = append(problems, "BOOTSTRAP_BUNDLE_URL has ?v= cache busting")
	}
	return strings.Join(problems, "; ")
}
