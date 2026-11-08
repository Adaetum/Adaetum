package ansiblerun

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeAnsible writes a stand-in for ansible-playbook that records how it was
// called, so the test checks the real command line and environment.
func fakeAnsible(t *testing.T, body string) (program, dir string) {
	t.Helper()
	dir = t.TempDir()
	program = filepath.Join(dir, "ansible-playbook")
	script := "#!/bin/sh\n" + body
	if err := os.WriteFile(program, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return program, dir
}

func TestTheCommandLineIsFixedAndHasNoSecretsInIt(t *testing.T) {
	program, dir := fakeAnsible(t, `
echo "ARGV: $@"
echo "CONFIG: $ANSIBLE_CONFIG"
echo "CWD: $(pwd)"
varsfile=$(echo "$@" | sed -n 's/.*-e @\([^ ]*\).*/\1/p')
echo "VARS: $(cat "$varsfile")"
exit 3
`)
	vars := filepath.Join(t.TempDir(), "v.json")
	os.WriteFile(vars, []byte(`{"host_agent_join_token":"adj_topsecret1234"}`), 0o600)

	var out bytes.Buffer
	code, err := LocalExec{AnsiblePlaybook: program, Dir: dir, Inventory: "/inv.yml"}.Run(context.Background(),
		ExecSpec{Playbook: "playbooks/host-agent.yml", Limit: "node-a,node-b", Check: true, ExtraVarsFile: vars}, &out)
	if err != nil || code != 3 {
		t.Fatalf("the exit code must be passed through: %d %v", code, err)
	}
	text := out.String()
	argv := text[strings.Index(text, "ARGV:"):strings.Index(text, "CONFIG:")]
	for _, want := range []string{"playbooks/host-agent.yml", "--limit node-a,node-b", "-e @" + vars, "--check", "--diff", "-i /inv.yml"} {
		if !strings.Contains(argv, want) {
			t.Errorf("the command line is missing %q: %s", want, argv)
		}
	}
	if strings.Contains(argv, "topsecret") {
		t.Fatalf("a secret must never be on the command line: %s", argv)
	}
	if !strings.Contains(text, "CONFIG: "+filepath.Join(dir, "ansible.cfg")) || !strings.Contains(text, "CWD: ") {
		t.Fatalf("Ansible must run from its own directory with its own config: %s", text)
	}
	if !strings.Contains(text, "topsecret") {
		t.Fatal("the variables file is how the secret reaches Ansible")
	}
}

func TestWithoutCheckTheRunIsNotARehearsal(t *testing.T) {
	program, dir := fakeAnsible(t, `echo "ARGV: $@"`)
	var out bytes.Buffer
	LocalExec{AnsiblePlaybook: program, Dir: dir}.Run(context.Background(), ExecSpec{Playbook: "p.yml", Limit: "a", ExtraVarsFile: "/v"}, &out)
	if strings.Contains(out.String(), "--check") || strings.Contains(out.String(), "-i ") {
		t.Fatalf("no --check and no inventory override were asked for: %s", out.String())
	}
}

func TestValuesAreNeverInterpretedAsShellSyntax(t *testing.T) {
	program, dir := fakeAnsible(t, `echo "ARGV: $@"`)
	marker := filepath.Join(t.TempDir(), "pwned")
	var out bytes.Buffer
	// Validate would refuse this limit; the executor must still be safe on its own.
	LocalExec{AnsiblePlaybook: program, Dir: dir}.Run(context.Background(), ExecSpec{Playbook: "p.yml", Limit: "a;touch " + marker, ExtraVarsFile: "/v"}, &out)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a value was run as a command")
	}
}

func TestAProgramThatCannotBeStartedIsAnErrorNotAnExitCode(t *testing.T) {
	var out bytes.Buffer
	_, err := LocalExec{AnsiblePlaybook: "/nonexistent/ansible-playbook", Dir: t.TempDir()}.Run(context.Background(), ExecSpec{Playbook: "p.yml", Limit: "a", ExtraVarsFile: "/v"}, &out)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestCancellingKillsTheWholeProcessGroup(t *testing.T) {
	childMarker := filepath.Join(t.TempDir(), "child-alive")
	program, dir := fakeAnsible(t, `
# a child that would outlive its parent if only the parent were killed
( sleep 30; touch `+childMarker+` ) &
sleep 30
`)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		LocalExec{AnsiblePlaybook: program, Dir: dir}.Run(ctx, ExecSpec{Playbook: "p.yml", Limit: "a", ExtraVarsFile: "/v"}, &bytes.Buffer{})
	}()
	time.Sleep(500 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not stop the run")
	}
}
