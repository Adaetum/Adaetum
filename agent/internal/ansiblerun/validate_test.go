package ansiblerun

import (
	"strings"
	"testing"
)

func hostAgent(limit string, vars map[string]string) Request {
	return Request{Playbook: "host-agent", Limit: limit, Check: true, Vars: vars}
}

func TestAGoodRequestBecomesTypedValuesAndAMaskedDisplay(t *testing.T) {
	v, err := Validate(hostAgent("node-a, node-b,node-a", map[string]string{
		"host_agent_binary_src":    "/opt/dist/adaetum-agent",
		"host_agent_allow_changes": "true",
		"host_agent_join_token":    "adj_supersecretvalue",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(v.Limit, ",") != "node-a,node-b" {
		t.Fatalf("servers are trimmed and de-duplicated: %v", v.Limit)
	}
	if v.Values["host_agent_allow_changes"] != true || v.Values["host_agent_binary_src"] != "/opt/dist/adaetum-agent" {
		t.Fatalf("a flag must reach Ansible as a boolean: %v", v.Values)
	}
	if v.Display["host_agent_join_token"] != "(hidden)" || strings.Contains(strings.Join(mapValues(v.Display), " "), "supersecret") {
		t.Fatalf("a secret must never appear in what is shown or recorded: %v", v.Display)
	}
	if len(v.Secrets) != 1 || v.Secrets[0] != "adj_supersecretvalue" {
		t.Fatalf("the secret must be kept for scrubbing: %v", v.Secrets)
	}
	if _, set := v.Values["host_agent_firewall_zone"]; set {
		t.Fatal("empty optional settings must be left out so the role's default applies")
	}
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func TestOnlyCataloguedPlaybooksCanBeLaunched(t *testing.T) {
	for _, id := range []string{"", "day2", "../playbooks/day2.yml", "host-agent.yml", "HOST-AGENT", "bootstrap"} {
		if _, err := Validate(Request{Playbook: id, Limit: "a"}); err == nil {
			t.Errorf("%q must not be launchable", id)
		}
	}
}

func TestAPlaybookThatChangesServersNeedsThemNamed(t *testing.T) {
	if _, err := Validate(hostAgent("", map[string]string{"host_agent_binary_src": "/x"})); err == nil {
		t.Fatal("an empty box must never mean every server")
	}
}

func TestOnlyExactServerNamesAreAcceptedInTheLimit(t *testing.T) {
	bad := []string{"all", "ALL", "localhost", "node-a,all", "*", "node-*", "!node-a", "node-a:node-b", "node-a&&node-b", "~node.*", "node a", "node-a;id", "--check", "-e", "node-a,", ",", "node-a\nnode-b", "$(id)", "node/a", "node-a[0]"}
	for _, limit := range bad {
		if v, err := Validate(hostAgent(limit, map[string]string{"host_agent_binary_src": "/x"})); err == nil {
			t.Errorf("limit %q must be refused (got %v)", limit, v.Limit)
		}
	}
	for _, limit := range []string{"node-50449", "node-a.tailnet.ts.net", "node_1,node-2"} {
		if _, err := Validate(hostAgent(limit, map[string]string{"host_agent_binary_src": "/x"})); err != nil {
			t.Errorf("limit %q is a plain server list: %v", limit, err)
		}
	}
}

func TestNotMoreThanFiftyServersAtOnce(t *testing.T) {
	var hosts []string
	for i := 0; i < 51; i++ {
		hosts = append(hosts, "n"+strings.Repeat("x", i))
	}
	if _, err := Validate(hostAgent(strings.Join(hosts, ","), nil)); err == nil {
		t.Fatal("a blast radius needs a ceiling")
	}
}

func TestUndeclaredVariablesAreRefusedNotIgnored(t *testing.T) {
	for _, name := range []string{"ansible_become_pass", "ansible_ssh_common_args", "host_agent_path", "host_agent_state_dir", "extra", "host_agent_owner"} {
		_, err := Validate(hostAgent("a", map[string]string{"host_agent_binary_src": "/x", name: "v"}))
		if err == nil {
			t.Errorf("%s is not a setting the page may change", name)
		}
	}
}

func TestEveryValueIsCheckedAgainstItsPattern(t *testing.T) {
	cases := map[string]string{
		"host_agent_binary_src":          "relative/path",
		"host_agent_binary_url":          "http://insecure.example/agent",
		"host_agent_binary_checksum":     "md5:abc",
		"host_agent_firewall_zone":       "Internal Zone",
		"host_agent_cluster_url":         "ftp://x",
		"host_agent_cluster_fingerprint": "not-a-fingerprint",
		"host_agent_allow_changes":       "maybe",
	}
	for name, value := range cases {
		if _, err := Validate(hostAgent("a", map[string]string{"host_agent_binary_src": "/x", name: value})); err == nil {
			t.Errorf("%s=%q must be refused", name, value)
		}
	}
}

func TestAFilePathMustBeAbsoluteCleanAndPlain(t *testing.T) {
	for _, path := range []string{"relative/agent", "/opt/../etc/shadow", "/opt/./agent", "/opt//agent", "/opt/agent/", "/opt/a b", "/opt/$(id)", "/opt/agent;rm", "~/agent", "../agent"} {
		if _, err := Validate(hostAgent("a", map[string]string{"host_agent_binary_src": path})); err == nil {
			t.Errorf("path %q must be refused", path)
		}
	}
	if _, err := Validate(hostAgent("a", map[string]string{"host_agent_binary_src": "/opt/dist/adaetum-agent-linux-amd64"})); err != nil {
		t.Errorf("a plain absolute path is fine: %v", err)
	}
}

func TestValuesCannotCarryNewlinesOrBeHuge(t *testing.T) {
	for _, value := range []string{"/x\n- name: injected", "/x\x00", "/" + strings.Repeat("a", 2000)} {
		if _, err := Validate(hostAgent("a", map[string]string{"host_agent_binary_src": value})); err == nil {
			t.Errorf("%q must be refused", value)
		}
	}
}

func TestADefaultAppliesWhenAFlagIsLeftBlank(t *testing.T) {
	v, err := Validate(hostAgent("a", map[string]string{"host_agent_binary_src": "/x"}))
	if err != nil || v.Values["host_agent_allow_changes"] != false {
		t.Fatalf("a blank flag takes its safe default: %v %v", v.Values, err)
	}
}

func TestTheCatalogIsInternallyConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Catalog() {
		if seen[p.ID] || p.File == "" || strings.Contains(p.File, "..") || strings.HasPrefix(p.File, "/") {
			t.Errorf("bad catalog entry %+v", p)
		}
		seen[p.ID] = true
		if p.File != "playbooks/"+p.ID+".yml" {
			t.Errorf("%s: the file should be playbooks/%s.yml", p.ID, p.ID)
		}
		for _, v := range p.Vars {
			if v.Pattern != "" {
				if _, err := Validate(Request{Playbook: p.ID, Limit: "a", Vars: map[string]string{v.Name: "ok"}}); err != nil && !strings.Contains(err.Error(), "expected form") {
					t.Errorf("%s.%s: its pattern does not compile: %v", p.ID, v.Name, err)
				}
			}
		}
	}
}
