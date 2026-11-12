// Package localsecrets is the "local-secrets" procedure: the old Phase 20. It creates
// the temporary, node-local secrets that must exist before OpenBao does, in a private
// directory, and never replaces one that is already there.
//
// It is a chunk of first boot that runs on its own (`adaetum-agent run local-secrets`)
// and is also part of the first-boot procedure.
//
// What it keeps from the script, deliberately:
//   - the same secret names and sizes, in the same encoding (N random bytes, base64),
//     so everything that reads them keeps working;
//   - an existing, non-empty secret is NEVER touched, so a rerun cannot desynchronize a
//     component from the credential it was installed with;
//   - an existing EMPTY secret is an error that stops for a person (it is almost always
//     a bug), unless the run was started with regenerate_empty=true;
//   - the directory is private (0700) and every file is 0600.
//
// What is different: files are written atomically, so a crash cannot leave an empty
// secret under its real name, and nothing about a secret's VALUE ever reaches the
// journal or the log. Only names do.
//
// It is not yet ported: chown to BOOTSTRAP_OWNER (the agent runs as root, so files are
// root's already).
package localsecrets

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/Adaetum/Adaetum/agent/internal/runs"
)

// Name is the procedure's name.
const Name = "local-secrets"

// Inputs a run may set. Both are optional.
const (
	// InputDir is the directory the secrets go in. Default /var/lib/bootstrap-secrets.
	// (Named "directory", not "secret_dir": the engine refuses input names that look like
	// they hold a secret, and this one holds a path, not a secret.)
	InputDir = "directory"
	// InputRegenerateEmpty lets an empty secret file be replaced ("true" or "1"). Without
	// it an empty file stops the run, because replacing it can desync a workload.
	InputRegenerateEmpty = "regenerate_empty"
)

const defaultDir = "/var/lib/bootstrap-secrets"

// Files is what the procedure needs from the machine's file system.
type Files interface {
	MkdirAll(path string, mode fs.FileMode) error
	Stat(path string) (fs.FileInfo, error)
	Read(path string) ([]byte, error)
	WriteAtomic(path string, data []byte, mode fs.FileMode) error
}

// Random returns n cryptographically random bytes.
type Random func(n int) ([]byte, error)

type spec struct {
	name string
	// bytes is how many random bytes (the script's "length"); zero for a literal.
	bytes   int
	literal string
	urlSafe bool // the ntfy publisher password: 32 URL-safe characters
}

// The list, in the old script's order. Names are what other components read.
var generated = []spec{
	{name: "rke2_token", bytes: 48},
	{name: "rancher_admin_password", bytes: 24},
	{name: "argocd_admin_password", bytes: 24},
	{name: "argocd_server_secret_key", bytes: 48},
	{name: "argocd_redis_password", bytes: 24},
	{name: "gitea_admin_password", bytes: 24},
	{name: "gitea_secret_key", bytes: 48},
	{name: "gitea_internal_token", bytes: 48},
	{name: "gitea_jwt_secret", bytes: 48},
	{name: "grafana_admin_password", bytes: 24},
	{name: "grafana_secret_key", bytes: 48},
	{name: "ntfy_admin_password", bytes: 24},
	{name: "ntfy_apprise_password", bytes: 32, urlSafe: true},
	// Homepage uses its own Grafana Viewer, independent of the administrator, so either
	// identity can rotate without widening access.
	{name: "homepage_grafana_password", bytes: 24},
	{name: "authentik_secret_key", bytes: 48},
	{name: "authentik_postgresql_password", bytes: 24},
	{name: "authentik_admin_password", bytes: 24},
	{name: "authentik_bootstrap_token", bytes: 32},
	// The break-glass owner's password (seeded into OpenBao later, where it is rotated).
	{name: "adaetum_owner_password", bytes: 24},
}

var literals = []spec{
	{name: "authentik_admin_username", literal: "akadmin"},
	{name: "adaetum_owner_email", literal: "break-glass@adaetum.local"},
}

var (
	safeDir    = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,200}$`)
	urlSafe32  = regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`)
	dirLockKey = "bootstrap-secrets"
)

// New returns the procedure, reading and writing through files and drawing randomness
// from random.
func New(files Files, random Random) *runs.Procedure {
	s := &secrets{files: files, random: random}
	return &runs.Procedure{
		Name:        Name,
		Description: "Creates the temporary secrets needed before OpenBao exists, in a private directory. Existing secrets are never replaced.",
		Steps: []runs.Step{
			{
				Name:  "directory",
				Locks: []string{dirLockKey},
				Check: s.directoryReady,
				Apply: s.makeDirectory,
			},
			{
				Name:  "generated",
				Needs: []string{"directory"},
				Locks: []string{dirLockKey},
				Check: s.generatedPresent,
				Apply: s.writeGenerated,
			},
			{
				Name:  "literals",
				Needs: []string{"generated"},
				Locks: []string{dirLockKey},
				Check: s.literalsPresent,
				Apply: s.writeLiterals,
			},
		},
	}
}

type secrets struct {
	files  Files
	random Random
}

func dirOf(env runs.Env) (string, error) {
	dir := defaultDir
	if value := env.Inputs[InputDir]; value != "" {
		dir = value
	}
	if !safeDir.MatchString(dir) || strings.Contains(dir, "..") {
		return "", runs.BrokenBecause(runs.CodeConfig, fmt.Sprintf("%s must be an absolute path of ordinary characters, not %q", InputDir, dir))
	}
	return dir, nil
}

func regenerateEmpty(env runs.Env) bool {
	switch strings.ToLower(env.Inputs[InputRegenerateEmpty]) {
	case "1", "true", "yes":
		return true
	}
	return false
}

func (s *secrets) directoryReady(_ context.Context, env runs.Env) (runs.Status, error) {
	dir, err := dirOf(env)
	if err != nil {
		return runs.Unknown, err
	}
	info, err := s.files.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return runs.Unsatisfied, nil
	case err != nil:
		return runs.Unknown, runs.BrokenBecause(runs.CodeConfig, "cannot read the secret directory: "+err.Error())
	case !info.IsDir():
		return runs.Unknown, runs.NeedsHuman(runs.CodeConfig, dir+" exists but is not a directory")
	case info.Mode().Perm() != 0o700:
		return runs.Unsatisfied, nil // exists but not private: Apply tightens it
	}
	return runs.Satisfied, nil
}

func (s *secrets) makeDirectory(_ context.Context, env runs.Env) error {
	dir, err := dirOf(env)
	if err != nil {
		return err
	}
	if err := s.files.MkdirAll(dir, 0o700); err != nil {
		return runs.BrokenBecause(runs.CodeConfig, "cannot create the secret directory: "+err.Error())
	}
	return nil
}

// fileState is what is on disk for one secret.
type fileState int

const (
	missing fileState = iota
	empty
	present
)

func (s *secrets) state(dir, name string) (fileState, error) {
	info, err := s.files.Stat(dir + "/" + name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return missing, nil
	case err != nil:
		return missing, runs.BrokenBecause(runs.CodeConfig, fmt.Sprintf("cannot read %s: %v", name, err))
	case info.Size() == 0:
		return empty, nil
	}
	return present, nil
}

// needs lists the secrets in specs that must be written, or stops for a person when a
// file is in a state the old script treated as an error.
func (s *secrets) needs(env runs.Env, specs []spec) ([]spec, error) {
	dir, err := dirOf(env)
	if err != nil {
		return nil, err
	}
	var todo []spec
	for _, sp := range specs {
		state, err := s.state(dir, sp.name)
		if err != nil {
			return nil, err
		}
		switch state {
		case missing:
			todo = append(todo, sp)
		case empty:
			// A literal is simply written over, as the script did. A generated secret that
			// is empty is almost always a bug and replacing it can desync a workload, so it
			// stops unless the person said to regenerate.
			if sp.literal != "" || regenerateEmpty(env) {
				todo = append(todo, sp)
				continue
			}
			return nil, runs.NeedsHuman(runs.CodeConfig, fmt.Sprintf("%s exists but is empty. Delete the empty file and run again, or start the run with %s=true to replace it", sp.name, InputRegenerateEmpty))
		default:
			if sp.urlSafe {
				data, err := s.files.Read(dir + "/" + sp.name)
				if err != nil {
					return nil, runs.BrokenBecause(runs.CodeConfig, fmt.Sprintf("cannot read %s: %v", sp.name, err))
				}
				if !urlSafe32.Match(data) {
					return nil, runs.NeedsHuman(runs.CodeConfig, fmt.Sprintf("%s exists but is not a valid 32-character URL-safe password; delete it and run again", sp.name))
				}
			}
		}
	}
	return todo, nil
}

func (s *secrets) generatedPresent(_ context.Context, env runs.Env) (runs.Status, error) {
	todo, err := s.needs(env, generated)
	if err != nil {
		return runs.Unknown, err
	}
	return statusOf(len(todo) == 0), nil
}

func (s *secrets) literalsPresent(_ context.Context, env runs.Env) (runs.Status, error) {
	todo, err := s.needs(env, literals)
	if err != nil {
		return runs.Unknown, err
	}
	return statusOf(len(todo) == 0), nil
}

func statusOf(ok bool) runs.Status {
	if ok {
		return runs.Satisfied
	}
	return runs.Unsatisfied
}

func (s *secrets) writeGenerated(_ context.Context, env runs.Env) error {
	todo, err := s.needs(env, generated)
	if err != nil {
		return err
	}
	dir, _ := dirOf(env)
	for _, sp := range todo {
		value, err := s.generate(sp)
		if err != nil {
			return runs.BrokenBecause(runs.CodeConfig, fmt.Sprintf("cannot generate %s: %v", sp.name, err))
		}
		if err := s.files.WriteAtomic(dir+"/"+sp.name, []byte(value), 0o600); err != nil {
			return runs.BrokenBecause(runs.CodeConfig, fmt.Sprintf("cannot write %s: %v", sp.name, err))
		}
	}
	return nil
}

func (s *secrets) writeLiterals(_ context.Context, env runs.Env) error {
	todo, err := s.needs(env, literals)
	if err != nil {
		return err
	}
	dir, _ := dirOf(env)
	for _, sp := range todo {
		if err := s.files.WriteAtomic(dir+"/"+sp.name, []byte(sp.literal), 0o600); err != nil {
			return runs.BrokenBecause(runs.CodeConfig, fmt.Sprintf("cannot write %s: %v", sp.name, err))
		}
	}
	return nil
}

// generate makes one secret's value the way the script did: N random bytes, base64. The
// ntfy publisher password is made URL-safe and cut to exactly 32 characters, because it
// travels in URLs.
func (s *secrets) generate(sp spec) (string, error) {
	raw, err := s.random(sp.bytes)
	if err != nil {
		return "", err
	}
	if len(raw) != sp.bytes {
		return "", fmt.Errorf("the random source returned %d bytes, wanted %d", len(raw), sp.bytes)
	}
	value := base64.StdEncoding.EncodeToString(raw)
	if sp.urlSafe {
		value = strings.NewReplacer("+", "-", "/", "_", "=", "").Replace(value)[:32]
	}
	return value, nil
}
