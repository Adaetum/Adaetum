package checkout

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Adaetum/Adaetum/installer/envfile"
)

// Workspace is a disposable checkout. All Git mutations and rendered secrets
// stay here; the launch checkout remains the operator's source of code.
type Workspace struct{ Root, dir string }

func (w *Workspace) Close() error { return os.RemoveAll(w.dir) }

func Prepare(ctx context.Context, source string) (_ *Workspace, err error) {
	dir, err := os.MkdirTemp("", "adaetum-setup-")
	if err != nil {
		return nil, err
	}
	w := &Workspace{Root: filepath.Join(dir, "source"), dir: dir}
	// Existing backup keys are needed to avoid orphaning previous emergency kits.
	// Only these values are copied into the private, short-lived checkout.
	defer func() {
		if err != nil {
			return
		}
		old := envfile.Read(filepath.Join(source, ".env"))
		for _, key := range []string{"BOOTSTRAP_BACKUP_PASSPHRASE", "BOOTSTRAP_BACKUP_PASSPHRASE_B64"} {
			if value := old[key]; value != "" {
				if e := envfile.Upsert(filepath.Join(w.Root, ".env"), key, value); e != nil {
					err = fmt.Errorf("preserve backup passphrase: %w", e)
					_ = w.Close()
					return
				}
			}
		}
	}()
	defer func() {
		if err != nil {
			_ = w.Close()
		}
	}()
	run := func(args ...string) (string, error) {
		c := exec.CommandContext(ctx, "git", args...)
		c.Dir = source
		out, e := c.CombinedOutput()
		if e != nil {
			return "", fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(string(out)), e)
		}
		return strings.TrimSpace(string(out)), nil
	}
	_, gitErr := os.Lstat(filepath.Join(source, ".git"))
	if gitErr != nil && !os.IsNotExist(gitErr) {
		return nil, fmt.Errorf("inspect source checkout: %w", gitErr)
	}
	if gitErr == nil {
		dirty, e := run("status", "--porcelain", "--untracked-files=no")
		if e != nil {
			return nil, e
		}
		if dirty != "" {
			return nil, fmt.Errorf("the source checkout has uncommitted tracked changes; commit or stash them before setup so the selected source can be copied safely")
		}
		branch, e := run("branch", "--show-current")
		if e != nil {
			return nil, e
		}
		if branch == "" {
			return nil, fmt.Errorf("setup requires a named source branch")
		}
		origin, _ := run("remote", "get-url", "origin")
		if _, e = run("clone", "--no-hardlinks", "--no-local", "--branch", branch, "--single-branch", source, w.Root); e != nil {
			return nil, e
		}
		if origin != "" {
			c := exec.CommandContext(ctx, "git", "remote", "set-url", "origin", origin)
			c.Dir = w.Root
			if out, e := c.CombinedOutput(); e != nil {
				return nil, fmt.Errorf("configure isolated checkout: %s: %w", strings.TrimSpace(string(out)), e)
			}
		}
		// Git does not copy repository-local identity into a clone. Preserve the
		// source identity, or use a setup-only identity when none is configured.
		for _, setting := range [][2]string{{"user.name", "Adaetum Setup"}, {"user.email", "setup@adaetum.local"}} {
			value, e := run("config", "--get", setting[0])
			if e != nil || value == "" {
				value = setting[1]
			}
			c := exec.CommandContext(ctx, "git", "config", "--local", setting[0], value)
			c.Dir = w.Root
			if out, e := c.CombinedOutput(); e != nil {
				return nil, fmt.Errorf("configure isolated checkout identity: %s: %w", strings.TrimSpace(string(out)), e)
			}
		}
		return w, nil
	}
	if err = os.MkdirAll(w.Root, 0700); err != nil {
		return nil, err
	}
	err = filepath.WalkDir(source, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(source, path)
		if e != nil {
			return e
		}
		if rel == "." {
			return nil
		}
		name := d.Name()
		if d.IsDir() && (name == ".git" || name == ".adaetum" || name == ".logs" || name == "node_modules" || name == "dist" || name == ".cache") {
			return filepath.SkipDir
		}
		if !d.IsDir() && (name == ".env" || strings.HasSuffix(name, ".iso")) {
			return nil
		}
		target := filepath.Join(w.Root, rel)
		info, e := d.Info()
		if e != nil {
			return e
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("source archive contains an unsupported file: %s", rel)
		}
		in, e := os.Open(path)
		if e != nil {
			return e
		}
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if e != nil {
			_ = in.Close()
			return e
		}
		_, e = io.Copy(out, in)
		inputErr := in.Close()
		ce := out.Close()
		if e != nil {
			return e
		}
		if inputErr != nil {
			return inputErr
		}
		return ce
	})
	if err != nil {
		return nil, err
	}
	return w, nil
}

// RetainInstallers saves any completed installer even when a later setup stage
// fails or a targeted run omits finalization. The workspace itself is disposable.
func (w *Workspace) RetainInstallers(outputRoot string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(w.Root, "dist", "*-ks.iso"))
	if err != nil {
		return nil, err
	}
	var retained []string
	for _, source := range matches {
		info, err := os.Stat(source)
		if err != nil {
			return retained, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		destDir := filepath.Join(outputRoot, "dist")
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return retained, err
		}
		in, err := os.Open(source)
		if err != nil {
			return retained, err
		}
		out, err := os.CreateTemp(destDir, ".adaetum-installer-*.partial")
		if err != nil {
			_ = in.Close()
			return retained, err
		}
		name := out.Name()
		_, copyErr := io.Copy(out, in)
		inErr := in.Close()
		outErr := out.Close()
		if copyErr != nil || inErr != nil || outErr != nil {
			_ = os.Remove(name)
			return retained, fmt.Errorf("could not retain installer ISO")
		}
		if err := os.Chmod(name, 0644); err != nil {
			_ = os.Remove(name)
			return retained, err
		}
		dest := filepath.Join(destDir, filepath.Base(source))
		if err := os.Rename(name, dest); err != nil {
			_ = os.Remove(name)
			return retained, err
		}
		retained = append(retained, dest)
	}
	return retained, nil
}
