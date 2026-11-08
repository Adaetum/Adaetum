// Package engine turns text into sentence vectors for the embedding service.
package engine

import (
	"context"
	"math"
)

// Engine embeds text.
type Engine interface {
	// Model names the embedding space the vectors belong to. The agent records it with
	// every stored vector and never compares vectors from different models.
	Model() string
	// Dim is the vector length.
	Dim() int
	// Embed returns one L2-normalised vector per text, in order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Close releases native resources.
	Close() error
}

// MeanPool averages the token vectors of each sequence, ignoring padding, then
// L2-normalises the result: the pooling the sentence-transformers all-MiniLM-L6-v2
// model was trained with.
//
// hidden is the model's last_hidden_state, flattened [batch][seq][dim]; mask is the
// attention mask, flattened [batch][seq] (1 for a real token, 0 for padding).
func MeanPool(hidden []float32, mask []int64, batch, seq, dim int) [][]float32 {
	out := make([][]float32, batch)
	for b := 0; b < batch; b++ {
		vector := make([]float32, dim)
		var count float32
		for s := 0; s < seq; s++ {
			if mask[b*seq+s] == 0 {
				continue
			}
			count++
			base := (b*seq + s) * dim
			for d := 0; d < dim; d++ {
				vector[d] += hidden[base+d]
			}
		}
		if count > 0 {
			for d := range vector {
				vector[d] /= count
			}
		}
		out[b] = Normalize(vector)
	}
	return out
}

// Normalize scales a vector to unit length (a zero vector stays zero).
func Normalize(vector []float32) []float32 {
	var sum float64
	for _, value := range vector {
		sum += float64(value) * float64(value)
	}
	if sum == 0 {
		return vector
	}
	scale := float32(1 / math.Sqrt(sum))
	for i := range vector {
		vector[i] *= scale
	}
	return vector
}
