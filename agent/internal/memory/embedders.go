package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"strings"
)

// HashEmbedder is a stand-in embedder for development: it hashes words and word
// pairs into a fixed-size vector (the "hashing trick") and normalises it. It is
// LEXICAL, not semantic: "disk" will not match "storage". It is deterministic, fast,
// and needs no model or network, which makes it the right thing to build and test the
// queue, worker and search against. The real embedder is the dedicated pod behind
// HTTPEmbedder. A stored vector records which model made it, so switching never
// mixes the two spaces.
type HashEmbedder struct{ Dim int }

// Model implements Embedder.
func (h HashEmbedder) Model() string { return fmt.Sprintf("hash-lexical-%d", h.dim()) }

func (h HashEmbedder) dim() int {
	if h.Dim <= 0 {
		return 256
	}
	return h.Dim
}

// Embed implements Embedder.
func (h HashEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = h.one(text)
	}
	return out, nil
}

func (h HashEmbedder) one(text string) []float32 {
	vector := make([]float32, h.dim())
	words := wordPattern.FindAllString(strings.ToLower(text), -1)
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
	for i, word := range words {
		add(word, 1)
		if i+1 < len(words) {
			add(word+" "+words[i+1], 0.5)
		}
	}
	var norm float64
	for _, value := range vector {
		norm += float64(value) * float64(value)
	}
	if norm > 0 {
		scale := float32(1 / math.Sqrt(norm))
		for i := range vector {
			vector[i] *= scale
		}
	}
	return vector
}

// HTTPEmbedder calls the dedicated embedding service (a stateless pod). The
// contract is deliberately tiny so the pod and this client are easy to keep in step:
//
//	POST <URL>   {"texts": ["...", "..."]}
//	200          {"model": "minilm-l6-v2-int8", "vectors": [[...], [...]]}
//
// The pod itself is NOT BUILT (see docs/memory.md): Go, MiniLM-L6-v2 INT8 through
// ONNX Runtime, 1 to 2 CPU cores, one replica, no state, so Kubernetes can move it
// to another node if its host fails.
type HTTPEmbedder struct {
	URL       string
	ModelName string
	Client    *http.Client
}

// Model implements Embedder.
func (h HTTPEmbedder) Model() string { return h.ModelName }

// Embed implements Embedder.
func (h HTTPEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]any{"texts": texts})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("the embedding service could not be reached: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the embedding service answered %d", response.StatusCode)
	}
	var reply struct {
		Model   string      `json:"model"`
		Vectors [][]float32 `json:"vectors"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 32<<20)).Decode(&reply); err != nil {
		return nil, errors.New("the embedding service's reply was not understood")
	}
	if len(reply.Vectors) != len(texts) {
		return nil, fmt.Errorf("the embedding service returned %d vectors for %d texts", len(reply.Vectors), len(texts))
	}
	if reply.Model != "" && reply.Model != h.ModelName {
		return nil, fmt.Errorf("the embedding service is running model %q, not %q", reply.Model, h.ModelName)
	}
	for _, vector := range reply.Vectors {
		if len(vector) == 0 {
			return nil, errors.New("the embedding service returned an empty vector")
		}
	}
	return reply.Vectors, nil
}

// OpenAIEmbedder talks to any service that implements the OpenAI embeddings API:
//
//	POST <URL>  {"model": "...", "input": ["...", "..."]}
//	200         {"data": [{"index": 0, "embedding": [...]}, ...]}
//
// with the key as a bearer token. The key is never logged, stored or shown. Using this
// sends memory text to that service; the Embedding page asks for confirmation first.
type OpenAIEmbedder struct {
	URL       string
	ModelName string
	Key       string
	Client    *http.Client
}

// Model implements Embedder. The name is prefixed so vectors from an external service
// are never confused with the in-cluster model of the same name.
func (o OpenAIEmbedder) Model() string { return "openai:" + o.ModelName }

// Embed implements Embedder.
func (o OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": o.ModelName, "input": texts})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, o.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+o.Key)
	client := o.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("the embedding service could not be reached: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the embedding service answered %d", response.StatusCode)
	}
	var reply struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 32<<20)).Decode(&reply); err != nil {
		return nil, errors.New("the embedding service's reply was not understood")
	}
	if len(reply.Data) != len(texts) {
		return nil, fmt.Errorf("the embedding service returned %d vectors for %d texts", len(reply.Data), len(texts))
	}
	out := make([][]float32, len(texts))
	for _, item := range reply.Data {
		if item.Index < 0 || item.Index >= len(out) || len(item.Embedding) == 0 {
			return nil, errors.New("the embedding service returned an unusable vector")
		}
		out[item.Index] = item.Embedding
	}
	for _, vector := range out {
		if vector == nil {
			return nil, errors.New("the embedding service left a text without a vector")
		}
	}
	return out, nil
}
