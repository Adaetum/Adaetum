package engine

import (
	"context"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
)

var words = regexp.MustCompile(`[\p{L}\p{N}_-]+`)

// Hash is a development stand-in that needs no model or native library: it hashes
// words and word pairs into a fixed-size vector. It is LEXICAL, not semantic, and
// reports itself as such through its model name, so nothing downstream mistakes its
// vectors for MiniLM's. It lets the service run, and the pod's plumbing be tested, on
// any machine.
type Hash struct{ Size int }

// Model implements Engine.
func (h Hash) Model() string { return fmt.Sprintf("hash-lexical-%d", h.Dim()) }

// Dim implements Engine.
func (h Hash) Dim() int {
	if h.Size <= 0 {
		return 256
	}
	return h.Size
}

// Embed implements Engine.
func (h Hash) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		vector := make([]float32, h.Dim())
		tokens := words.FindAllString(strings.ToLower(text), -1)
		add := func(feature string, weight float32) {
			hash := fnv.New64a()
			hash.Write([]byte(feature))
			sum := hash.Sum64()
			sign := float32(1)
			if sum&1 == 1 {
				sign = -1
			}
			vector[(sum>>1)%uint64(len(vector))] += sign * weight
		}
		for j, token := range tokens {
			add(token, 1)
			if j+1 < len(tokens) {
				add(token+" "+tokens[j+1], 0.5)
			}
		}
		out[i] = Normalize(vector)
	}
	return out, nil
}

// Close implements Engine.
func (Hash) Close() error { return nil }
