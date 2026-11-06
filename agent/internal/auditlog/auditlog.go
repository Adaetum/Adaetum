// Package auditlog is the append-only record the agent keeps of anything that acts on
// a server. One JSON object per line, written and synced before the action starts,
// in a file only the agent's owner can read.
package auditlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Log is an open audit file.
type Log struct {
	mu      sync.Mutex
	file    *os.File
	encoder *json.Encoder
}

// Open opens path for appending, creating it (and its directory) privately. A
// failure to open is the caller's cue to refuse to act: nothing should run as an
// administrator without a record that it did.
func Open(path string) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Log{file: file, encoder: json.NewEncoder(file)}, nil
}

// Record appends one entry and flushes it to disk, so it survives the machine
// failing a moment later.
func (l *Log) Record(entry any) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.encoder.Encode(entry); err != nil {
		return err
	}
	return l.file.Sync()
}

// Close closes the file.
func (l *Log) Close() error { return l.file.Close() }
