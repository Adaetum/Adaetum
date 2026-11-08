// Package ansiblerun lets the agent launch Ansible playbooks on demand.
//
// THE PATTERN. The agent does not push software or change servers itself. Ansible
// does, over the SSH access it already has. The agent's job is to ACTUATE a
// playbook: show what can be run, take the variables a person fills in, run
// ansible-playbook, and keep a record. So "install or update the agent on this
// server" is a playbook (ansible/playbooks/host-agent.yml), and the web page is the
// button and the form for it.
//
// What keeps that safe, because it runs Ansible as an administrator:
//
//   - Only playbooks in the Catalog can be launched, and only with the variables the
//     catalog declares for them, each checked against a pattern or a list of
//     choices. The page cannot name a file, a path to run, or an arbitrary variable.
//   - Servers are passed as --limit and must be named explicitly for playbooks that
//     change servers, so nothing can be launched at "every server" by leaving a
//     field empty.
//   - Rehearsing (--check) is the default in the UI and is one tick away in the API.
//   - Secrets (a join token) go in a private file Ansible reads, never on a command
//     line, and are scrubbed from the captured output and the audit record.
//   - One run at a time, every launch is recorded before it starts, and a run that
//     cannot be recorded is refused.
//   - The launcher is off unless the agent is started to allow it, can run Ansible,
//     can write its record, and has an access token; and it never works over -ssh.
//
// WHERE IT RUNS. Executor is the seam. LocalExec runs ansible-playbook on the
// machine the agent is on (the cluster agent's pod image will carry Ansible, and a
// host can run it for itself). KubernetesJob, which would run the same playbook as a
// Job from the existing runner's pod template, is a stub.
package ansiblerun

import "sort"

// VarKind says how a variable is entered and how it is sent to Ansible.
type VarKind string

const (
	// KindPath is an absolute, already-clean file path on the machine running
	// Ansible (no "..", no trailing slash). It names a file Ansible will copy, so it
	// is held to a stricter form than free text.
	KindPath VarKind = "path"
	// KindText is a single line of text, checked against Pattern if set.
	KindText VarKind = "text"
	// KindFlag is true or false.
	KindFlag VarKind = "flag"
	// KindChoice is one of Choices.
	KindChoice VarKind = "choice"
	// KindSecret is text that must never be shown or logged: it is masked in the
	// form, passed to Ansible in a private file, and scrubbed from output.
	KindSecret VarKind = "secret"
)

// Var is one variable a playbook accepts. Name is the Ansible variable name.
type Var struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Help     string   `json:"help,omitempty"`
	Kind     VarKind  `json:"kind"`
	Required bool     `json:"required,omitempty"`
	Default  string   `json:"default,omitempty"`
	Choices  []string `json:"choices,omitempty"`
	// Pattern is a regular expression the whole value must match. Empty means any
	// single line up to the length limit.
	Pattern string `json:"pattern,omitempty"`
}

// Playbook is something the agent may launch.
type Playbook struct {
	// ID is what the API and page use; File is relative to the Ansible directory and
	// is never taken from a request.
	ID          string `json:"id"`
	File        string `json:"file"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// LimitRequired: the person must name the servers. True for anything that
	// changes servers, so an empty box can never mean "all of them".
	LimitRequired bool  `json:"limitRequired"`
	Vars          []Var `json:"vars"`
}

// Catalog is every playbook the agent may launch. It is code, reviewed with the
// playbooks it describes, and deliberately short: add a playbook here when it is
// safe to launch from the page, not merely because it exists.
func Catalog() []Playbook {
	const (
		checksumPattern   = `^sha256:[0-9a-f]{64}$`
		urlPattern        = `^https://[A-Za-z0-9._~:/?#@!$&()*+,;=%-]+$`
		fingerprintRegexp = `^([0-9A-Fa-f]{2}:){31}[0-9A-Fa-f]{2}$|^[0-9A-Fa-f]{64}$`
		zonePattern       = `^[a-z0-9_-]*$`
	)
	return []Playbook{
		{
			ID:    "host-agent",
			File:  "playbooks/host-agent.yml",
			Title: "Install or update the Adaetum agent",
			Description: "Puts the agent on a server, or updates it, over the SSH access Ansible already has. " +
				"Safe to run again. Rehearse first: it shows what would change without changing anything.",
			LimitRequired: true,
			Vars: []Var{
				{Name: "host_agent_binary_src", Label: "Agent file on this machine", Kind: KindPath,
					Help: "Absolute path to the adaetum-agent binary to install. Set this or the web address below, not both."},
				{Name: "host_agent_binary_url", Label: "Agent download address", Kind: KindText, Pattern: urlPattern,
					Help: "HTTPS address to download the binary from. Needs the checksum below."},
				{Name: "host_agent_binary_checksum", Label: "Download checksum", Kind: KindText, Pattern: checksumPattern,
					Help: "sha256:… of the download. The install refuses a file that does not match."},
				{Name: "host_agent_auth_enabled", Label: "Install the host account service", Kind: KindFlag, Default: "false"},
				{Name: "host_agent_auth_binary_src", Label: "Auth file on this machine", Kind: KindPath,
					Help: "Absolute path to the auth binary. Set this or the auth download address, not both."},
				{Name: "host_agent_auth_binary_url", Label: "Auth download address", Kind: KindText, Pattern: urlPattern,
					Help: "HTTPS address of the auth binary; requires its SHA-256 checksum."},
				{Name: "host_agent_auth_binary_checksum", Label: "Auth download checksum", Kind: KindText, Pattern: checksumPattern},
				{Name: "host_agent_firewall_zone", Label: "Firewall zone to open the port in", Kind: KindText, Pattern: zonePattern,
					Help: "For example internal. Leave empty to leave the firewall alone."},
				{Name: "host_agent_allow_changes", Label: "Let the agent's page remove container images", Kind: KindFlag, Default: "false"},
				{Name: "host_agent_cluster_url", Label: "Cluster agent address (to join)", Kind: KindText, Pattern: urlPattern,
					Help: "Fill this, the fingerprint, and the join token to enroll the server with the cluster agent."},
				{Name: "host_agent_cluster_fingerprint", Label: "Cluster agent certificate fingerprint", Kind: KindText, Pattern: fingerprintRegexp},
				{Name: "host_agent_join_token", Label: "Join token", Kind: KindSecret,
					Help: "A one-time token from the Cluster page. It is never shown or saved."},
			},
		},
		{
			ID:            "image-cleanup",
			File:          "playbooks/image-cleanup.yml",
			Title:         "Set up the daily image cleanup",
			Description:   "Installs or updates the timer that removes unused container images each day, from the settings in platform.yaml.",
			LimitRequired: true,
			Vars: []Var{
				{Name: "image_cleanup_agent_src", Label: "Agent file on this machine", Kind: KindPath,
					Help: "Absolute path to the adaetum-agent binary, if it is not already on the server."},
			},
		},
	}
}

// Find returns a playbook by ID.
func Find(id string) (Playbook, bool) {
	for _, playbook := range Catalog() {
		if playbook.ID == id {
			return playbook, true
		}
	}
	return Playbook{}, false
}

func (p Playbook) variable(name string) (Var, bool) {
	for _, v := range p.Vars {
		if v.Name == name {
			return v, true
		}
	}
	return Var{}, false
}

// secretValues returns the values of the secret variables in a validated request,
// for scrubbing.
func (p Playbook) secretValues(values map[string]string) []string {
	var secrets []string
	for name, value := range values {
		if v, ok := p.variable(name); ok && v.Kind == KindSecret && len(value) >= 4 {
			secrets = append(secrets, value)
		}
	}
	sort.Slice(secrets, func(a, b int) bool { return len(secrets[a]) > len(secrets[b]) })
	return secrets
}
