package main

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

const goodToken = "0123456789abcdef0123456789abcdef0123456789abcdef"

func ready() (cleanupConfig, system) {
	return cleanupConfig{allowChanges: true, tokenFile: "/etc/adaetum/agent-token"},
		system{euid: 0, hasRuntime: true, readToken: func(string) (string, fs.FileMode, error) { return goodToken, 0o600, nil }}
}

func TestChangesAreEnabledOnlyWhenEveryConditionHolds(t *testing.T) {
	cfg, sys := ready()
	capabilities, token := decideCapabilities("node-a", cfg, sys)
	if !capabilities.CanApply || token != goodToken || capabilities.Reason != "" {
		t.Fatalf("a fully configured agent must be able to apply: %+v", capabilities)
	}
}

func TestEachMissingConditionTurnsChangesOffWithAReason(t *testing.T) {
	cases := map[string]struct {
		mutate func(*cleanupConfig, *system)
		reason string
	}{
		"over ssh":      {func(c *cleanupConfig, s *system) { c.sshTarget = "ansible@node-a" }, "over SSH"},
		"not asked for": {func(c *cleanupConfig, s *system) { c.allowChanges = false }, "-allow-changes"},
		"not root":      {func(c *cleanupConfig, s *system) { s.euid = 1000 }, "not running as root"},
		"no runtime":    {func(c *cleanupConfig, s *system) { s.hasRuntime = false }, "does not run RKE2"},
		"no token file": {func(c *cleanupConfig, s *system) { c.tokenFile = "" }, "access token"},
		"unreadable": {func(c *cleanupConfig, s *system) {
			s.readToken = func(string) (string, fs.FileMode, error) { return "", 0, errors.New("no such file") }
		}, "could not be read"},
		"world readable": {func(c *cleanupConfig, s *system) {
			s.readToken = func(string) (string, fs.FileMode, error) { return goodToken, 0o644, nil }
		}, "readable only by root"},
		"group readable": {func(c *cleanupConfig, s *system) {
			s.readToken = func(string) (string, fs.FileMode, error) { return goodToken, 0o640, nil }
		}, "readable only by root"},
		"weak token": {func(c *cleanupConfig, s *system) {
			s.readToken = func(string) (string, fs.FileMode, error) { return "short", 0o600, nil }
		}, "at least"},
		"blank token": {func(c *cleanupConfig, s *system) {
			s.readToken = func(string) (string, fs.FileMode, error) { return "", 0o600, nil }
		}, "at least"},
	}
	for name, c := range cases {
		cfg, sys := ready()
		c.mutate(&cfg, &sys)
		capabilities, token := decideCapabilities("node-a", cfg, sys)
		if capabilities.CanApply || token != "" {
			t.Errorf("%s: changes must be off and the token withheld", name)
		}
		if !strings.Contains(capabilities.Reason, c.reason) {
			t.Errorf("%s: reason %q should mention %q", name, capabilities.Reason, c.reason)
		}
		if !strings.Contains(capabilities.Hint, "images plan") || !strings.Contains(capabilities.Hint, "images apply") {
			t.Errorf("%s: the hint must tell the operator what to run on the server: %q", name, capabilities.Hint)
		}
	}
}

func TestSSHModeIsRefusedEvenWhenEverythingElseIsReady(t *testing.T) {
	// The dev setup points an agent at a production host. It must stay read-only
	// even if someone also passes the flags and a valid token.
	cfg, sys := ready()
	cfg.sshTarget = "ansible@node-a"
	if capabilities, _ := decideCapabilities("node-a", cfg, sys); capabilities.CanApply {
		t.Fatal("an agent inspecting another host over SSH must never be able to change it")
	}
}
