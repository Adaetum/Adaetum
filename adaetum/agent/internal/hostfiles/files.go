// Package hostfiles is the run engine's adapter for files on this machine: reading them
// and writing them safely. Procedures that create or check files (the local secrets,
// the runtime payload check) take the small interfaces they need and are tested with
// an in-memory fake; this is the real one.
//
// It never runs a command. Its one careful operation is WriteAtomic, which exists for
// secrets: a file is written under a temporary name with its final permissions and
// renamed into place, so a crash can never leave a half-written or empty secret under the
// real name (the exact failure the old Phase 20 script had to guard against).
package hostfiles

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// FS is the machine's own file system.
type FS struct{}

// Read returns a whole file.
func (FS) Read(path string) ([]byte, error) { return os.ReadFile(path) }

// Stat describes a file. A missing file's error satisfies errors.Is(err, fs.ErrNotExist).
func (FS) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }

// MkdirAll creates a directory and any parents, and makes sure the directory itself has
// exactly mode, which MkdirAll alone would not (it applies the umask and leaves an
// existing directory as it was).
func (FS) MkdirAll(path string, mode fs.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

// WriteAtomic writes data to path with exactly mode, replacing any file already there
// in one step. The temporary file is created in the same directory with the final
// permissions from the start, so the content is never readable more widely than it
// will end up, and it is removed if anything fails.
func (FS) WriteAtomic(path string, data []byte, mode fs.FileMode) (err error) {
	temp, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer func() {
		if err != nil {
			temp.Close()
			os.Remove(name)
		}
	}()
	if err = temp.Chmod(mode); err != nil {
		return err
	}
	if _, err = temp.Write(data); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", name, err)
	}
	return os.Rename(name, path)
}
