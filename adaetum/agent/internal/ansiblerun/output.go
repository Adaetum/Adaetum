package ansiblerun

import (
	"strings"
	"sync"
)

const (
	maxLines      = 5000
	maxLineLength = 4096
)

// outputBuffer keeps the latest lines of a run's output, with absolute line
// numbers so a client polling for "what is new" stays correct even after old lines
// are dropped. It is also an io.Writer that scrubs secret values: Ansible is told
// not to print them, but a failure message or a module's echo might, and the
// output is shown in the browser and kept in memory.
type outputBuffer struct {
	secrets []string

	mu      sync.Mutex
	lines   []string
	base    int // absolute number of lines[0]
	partial strings.Builder
	dropped bool
}

func newOutputBuffer(secrets []string) *outputBuffer { return &outputBuffer{secrets: secrets} }

// Write implements io.Writer. Output is split into lines before scrubbing, so a
// secret split across two writes is still caught.
func (b *outputBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	text := b.partial.String() + string(data)
	b.partial.Reset()
	for {
		newline := strings.IndexByte(text, '\n')
		if newline < 0 {
			break
		}
		b.add(strings.TrimRight(text[:newline], "\r"))
		text = text[newline+1:]
	}
	b.partial.WriteString(text)
	return len(data), nil
}

// Flush adds a final line that had no trailing newline.
func (b *outputBuffer) Flush() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.partial.Len() > 0 {
		b.add(b.partial.String())
		b.partial.Reset()
	}
}

func (b *outputBuffer) add(line string) {
	for _, secret := range b.secrets {
		line = strings.ReplaceAll(line, secret, "***")
	}
	if len(line) > maxLineLength {
		line = line[:maxLineLength] + " …"
	}
	b.lines = append(b.lines, line)
	if len(b.lines) > maxLines {
		drop := len(b.lines) - maxLines
		b.lines = b.lines[drop:]
		b.base += drop
		b.dropped = true
	}
}

// From returns the lines numbered `after` and later.
func (b *outputBuffer) From(after int) (lines []string, first, next int, dropped bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if after < b.base {
		after = b.base
	}
	start := after - b.base
	if start > len(b.lines) {
		start = len(b.lines)
	}
	// Never nil: it is sent as JSON, where null would break a client that loops over it.
	return append(make([]string, 0, len(b.lines)-start), b.lines[start:]...), b.base + start, b.base + len(b.lines), b.dropped
}
