// Package envfile reads and updates the gitignored .env file that setup renders.
// The file holds secrets, so writes are atomic (a half-written file never replaces
// a good one) and owner-only.
package envfile

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Read parses KEY=VALUE lines. A missing file is empty, not an error.
func Read(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if k, v, ok := strings.Cut(line, "="); ok && k != "" && !strings.HasPrefix(k, "#") {
			out[k] = strings.TrimSpace(v)
		}
	}
	return out
}

// Upsert sets one key, replacing its line in place and keeping every other line.
func Upsert(path, key, value string) error {
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}
	replaced := false
	for i, l := range lines {
		if strings.HasPrefix(l, key+"=") {
			lines[i], replaced = key+"="+value, true
			break
		}
	}
	if !replaced {
		lines = append(lines, key+"="+value)
	}
	return writeAtomic(path, strings.Join(lines, "\n")+"\n")
}

// Write replaces the whole file with the given pairs, in order.
func Write(path string, pairs [][2]string) error {
	var b strings.Builder
	for _, p := range pairs {
		b.WriteString(p[0] + "=" + p[1] + "\n")
	}
	return writeAtomic(path, b.String())
}

func writeAtomic(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".env-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if err := os.Chmod(name, 0o600); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}
