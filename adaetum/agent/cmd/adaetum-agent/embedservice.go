package main

import (
	"fmt"
	"os"
)

// runEmbed points at the embedding service, which is its own program, not part of this
// binary. It lives in ../embed (module github.com/Adaetum/Adaetum/embed) so that its
// native dependency (ONNX Runtime, cgo) never touches the agent, which stays one static
// binary. See embed/README.md and docs/memory.md.
func runEmbed(_ []string) {
	fmt.Fprintln(os.Stderr, "the embedding service is a separate program: see embed/README.md (build ./cmd/adaetum-embed there)")
	os.Exit(3)
}
