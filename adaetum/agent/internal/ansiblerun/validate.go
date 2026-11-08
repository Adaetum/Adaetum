package ansiblerun

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Request is what a person asks the agent to run.
type Request struct {
	Playbook string `json:"playbook"`
	// Limit names the servers, separated by commas.
	Limit string `json:"limit"`
	// Check rehearses: Ansible reports what it would change and changes nothing.
	Check bool              `json:"check"`
	Vars  map[string]string `json:"vars"`
}

const (
	maxValueLength = 1024
	maxLimitLength = 2048
	maxLimitHosts  = 50
)

// A host in --limit: letters, digits, dots, dashes and underscores only. No
// patterns, no groups, no "all", no negation: only named servers.
var pathChars = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

var limitHost = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Validated is a request that has passed every check, with its values ready to hand
// to Ansible.
type Validated struct {
	Playbook Playbook
	Limit    []string
	Check    bool
	// Values are the variables to send, as Ansible types: flags are bool, the rest
	// are strings. Empty optional variables are left out.
	Values map[string]any
	// Display is the same variables for showing and recording, with secrets masked.
	Display map[string]string
	// Secrets are the secret values, so output can be scrubbed.
	Secrets []string
}

// Validate checks a request against the catalog. It trusts nothing in the request:
// the playbook must exist, every variable must be declared and match, and the limit
// must be an explicit list of server names.
func Validate(request Request) (Validated, error) {
	playbook, ok := Find(request.Playbook)
	if !ok {
		return Validated{}, fmt.Errorf("%q is not a playbook that can be launched", request.Playbook)
	}

	hosts, err := parseLimit(request.Limit)
	if err != nil {
		return Validated{}, err
	}
	if playbook.LimitRequired && len(hosts) == 0 {
		return Validated{}, errors.New("name the servers to run this on; it will not run on every server by default")
	}

	// Reject undeclared variables rather than ignoring them, so a typo or an
	// attempt to set something else is visible, not silently dropped.
	names := make([]string, 0, len(request.Vars))
	for name := range request.Vars {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, declared := playbook.variable(name); !declared {
			return Validated{}, fmt.Errorf("%q is not a setting this playbook accepts", name)
		}
	}

	validated := Validated{Playbook: playbook, Limit: hosts, Check: request.Check, Values: map[string]any{}, Display: map[string]string{}}
	for _, v := range playbook.Vars {
		value := strings.TrimSpace(request.Vars[v.Name])
		if value == "" {
			value = v.Default
		}
		if value == "" {
			if v.Required {
				return Validated{}, fmt.Errorf("%s is required", v.Label)
			}
			continue
		}
		if err := checkValue(v, value); err != nil {
			return Validated{}, err
		}
		if v.Kind == KindFlag {
			validated.Values[v.Name] = value == "true"
		} else {
			validated.Values[v.Name] = value
		}
		if v.Kind == KindSecret {
			validated.Display[v.Name] = "(hidden)"
		} else {
			validated.Display[v.Name] = value
		}
	}
	validated.Secrets = playbook.secretValues(request.Vars)
	return validated, nil
}

func parseLimit(limit string) ([]string, error) {
	limit = strings.TrimSpace(limit)
	if limit == "" {
		return nil, nil
	}
	if len(limit) > maxLimitLength {
		return nil, errors.New("the list of servers is too long")
	}
	seen := map[string]bool{}
	var hosts []string
	for _, host := range strings.Split(limit, ",") {
		host = strings.TrimSpace(host)
		if !limitHost.MatchString(host) {
			return nil, fmt.Errorf("%q is not a server name. Use exact names separated by commas", host)
		}
		// "all" and "localhost" are Ansible's special names for everything and this
		// machine; neither may be smuggled in as a server name.
		if lower := strings.ToLower(host); lower == "all" || lower == "localhost" {
			return nil, fmt.Errorf("%q is not allowed here. Name the servers", host)
		}
		if !seen[host] {
			seen[host] = true
			hosts = append(hosts, host)
		}
	}
	if len(hosts) > maxLimitHosts {
		return nil, fmt.Errorf("at most %d servers at a time", maxLimitHosts)
	}
	return hosts, nil
}

func checkValue(v Var, value string) error {
	if len(value) > maxValueLength || strings.ContainsAny(value, "\n\r\x00") {
		return fmt.Errorf("%s must be a single line of at most %d characters", v.Label, maxValueLength)
	}
	switch v.Kind {
	case KindFlag:
		if value != "true" && value != "false" {
			return fmt.Errorf("%s must be true or false", v.Label)
		}
	case KindPath:
		if !strings.HasPrefix(value, "/") || filepath.Clean(value) != value || !pathChars.MatchString(value) {
			return fmt.Errorf("%s must be a full path such as /opt/adaetum/adaetum-agent", v.Label)
		}
	case KindChoice:
		for _, choice := range v.Choices {
			if value == choice {
				return nil
			}
		}
		return fmt.Errorf("%s must be one of: %s", v.Label, strings.Join(v.Choices, ", "))
	case KindText, KindSecret:
		if v.Pattern != "" && !regexp.MustCompile(v.Pattern).MatchString(value) {
			return fmt.Errorf("%s is not in the expected form", v.Label)
		}
	}
	return nil
}
