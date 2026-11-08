//go:build onnx

package engine

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// The real model. Run with:
//
//	EMBED_MODEL_DIR=/dir/with/model.onnx+vocab.txt EMBED_ORT_LIB=/path/libonnxruntime.dylib \
//	  go test -tags onnx ./internal/engine
func TestTheRealModelPutsRelatedSentencesNearerThanUnrelatedOnes(t *testing.T) {
	dir, lib := os.Getenv("EMBED_MODEL_DIR"), os.Getenv("EMBED_ORT_LIB")
	if dir == "" || lib == "" {
		t.Skip("set EMBED_MODEL_DIR and EMBED_ORT_LIB to run against the real model")
	}
	e, err := NewOnnx(lib, filepath.Join(dir, "model.onnx"), filepath.Join(dir, "vocab.txt"), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if e.Dim() != 384 || e.Model() != ModelName {
		t.Fatalf("%d %s", e.Dim(), e.Model())
	}

	texts := []string{
		"The storage drive on the server is failing and needs to be replaced.", // 0
		"A hard disk has hardware faults and must be swapped out.",             // 1 same meaning, no shared key words
		"Certificates renew automatically every ninety days.",                  // 2 unrelated
		"How do I bake sourdough bread?",                                       // 3 unrelated
	}
	vectors, err := e.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	cos := func(a, b []float32) float64 {
		var d float64
		for i := range a {
			d += float64(a[i]) * float64(b[i])
		}
		return d
	}
	for i, v := range vectors {
		if len(v) != 384 {
			t.Fatalf("vector %d has %d dimensions", i, len(v))
		}
		var n float64
		for _, x := range v {
			n += float64(x) * float64(x)
		}
		if math.Abs(n-1) > 1e-3 {
			t.Fatalf("vector %d is not unit length: %v", i, n)
		}
	}
	related, unrelatedCert, unrelatedBread := cos(vectors[0], vectors[1]), cos(vectors[0], vectors[2]), cos(vectors[0], vectors[3])
	t.Logf("related=%.3f certs=%.3f bread=%.3f", related, unrelatedCert, unrelatedBread)
	if related < unrelatedCert+0.15 || related < unrelatedBread+0.15 {
		t.Fatalf("a sentence with the same meaning must be clearly nearer than unrelated ones: %.3f vs %.3f / %.3f", related, unrelatedCert, unrelatedBread)
	}

	// A vector must depend only on its own text, however the request was batched
	// (the INT8 model's dynamic quantisation would otherwise vary it slightly).
	alone, _ := e.Embed(context.Background(), texts[2:3])
	reordered, _ := e.Embed(context.Background(), []string{texts[3], texts[2], texts[0]})
	if cos(alone[0], vectors[2]) < 0.99999 || cos(reordered[1], vectors[2]) < 0.99999 {
		t.Fatalf("a vector changed with the batch it was in: %.6f %.6f", cos(alone[0], vectors[2]), cos(reordered[1], vectors[2]))
	}
	// Long input is cut, not an error.
	long := make([]byte, 0, 20000)
	for len(long) < 15000 {
		long = append(long, "disk failure node reboot "...)
	}
	if _, err := e.Embed(context.Background(), []string{string(long)}); err != nil {
		t.Fatalf("long text must be truncated, not fail: %v", err)
	}
}
