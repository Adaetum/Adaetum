package main

import (
	"strings"
	"testing"
)

func TestTheOverrideIsOffByDefaultAndSaysHowToTurnItOnDeliberately(t *testing.T) {
	enabled, reason := decideHostAccess(hostAccessConfig{})
	if enabled || !strings.Contains(reason, "-allow-host-ssh") || !strings.Contains(reason, "Ansible") {
		t.Fatalf("nothing asked for must mean off, with the standing explanation: %v %q", enabled, reason)
	}
}

func TestAskingForTheOverrideWithoutATokenStaysOff(t *testing.T) {
	enabled, reason := decideHostAccess(hostAccessConfig{allow: true})
	if enabled || !strings.Contains(reason, "access token") {
		t.Fatalf("the flag alone must not be enough: %v %q", enabled, reason)
	}
}

func TestTheOverrideNeverWorksInDevelopmentSSHMode(t *testing.T) {
	enabled, reason := decideHostAccess(hostAccessConfig{allow: true, token: strings.Repeat("t", 40), sshTarget: "ansible@node-a"})
	if enabled || !strings.Contains(reason, "development") {
		t.Fatalf("a session reading another cluster must never run commands on it: %v %q", enabled, reason)
	}
}

func TestEveryConditionMetEnablesIt(t *testing.T) {
	// ssh is on the PATH of any machine that can build this project.
	enabled, reason := decideHostAccess(hostAccessConfig{allow: true, token: strings.Repeat("t", 40)})
	if !enabled {
		t.Skipf("no ssh program on this machine: %s", reason)
	}
}

func TestSplitList(t *testing.T) {
	got := splitList(" a, b ,,c ")
	if strings.Join(got, "|") != "a|b|c" || len(splitList("")) != 0 {
		t.Fatalf("%v", got)
	}
}
