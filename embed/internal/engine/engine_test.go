package engine

import (
	"context"
	"math"
	"testing"
)

func TestMeanPoolIgnoresPaddingAndNormalises(t *testing.T) {
	// One sequence of three positions, the last is padding with a wild value that must
	// not affect the result. dim = 2. Real tokens: (1,0) and (3,0): mean (2,0) -> (1,0).
	hidden := []float32{1, 0, 3, 0, 999, 999}
	mask := []int64{1, 1, 0}
	got := MeanPool(hidden, mask, 1, 3, 2)
	if math.Abs(float64(got[0][0])-1) > 1e-6 || math.Abs(float64(got[0][1])) > 1e-6 {
		t.Fatalf("padding leaked into the vector: %v", got[0])
	}
}

func TestMeanPoolHandlesABatchIndependently(t *testing.T) {
	hidden := []float32{1, 0, 0, 1, 0, 2, 5, 5} // batch 2, seq 2, dim 2
	mask := []int64{1, 1, 1, 0}
	got := MeanPool(hidden, mask, 2, 2, 2)
	// first: mean of (1,0),(0,1) = (.5,.5) -> unit; second: only (0,2) -> (0,1)
	if math.Abs(float64(got[1][1])-1) > 1e-6 || math.Abs(float64(got[1][0])) > 1e-6 {
		t.Fatalf("the second sequence must not see the first or its padding: %v", got[1])
	}
	want := float32(1 / math.Sqrt(2))
	if math.Abs(float64(got[0][0]-want)) > 1e-6 || math.Abs(float64(got[0][1]-want)) > 1e-6 {
		t.Fatalf("%v", got[0])
	}
}

func TestAnAllPaddingSequenceDoesNotDivideByZero(t *testing.T) {
	got := MeanPool([]float32{7, 7}, []int64{0}, 1, 1, 2)
	if got[0][0] != 0 || got[0][1] != 0 || math.IsNaN(float64(got[0][0])) {
		t.Fatalf("%v", got[0])
	}
}

func TestTheHashEngineIsDeterministicAndFavoursSharedWords(t *testing.T) {
	h := Hash{}
	v, _ := h.Embed(context.Background(), []string{"node-a disk is failing", "node-a disk is failing", "certificates renew yearly"})
	cos := func(a, b []float32) float64 {
		var d float64
		for i := range a {
			d += float64(a[i]) * float64(b[i])
		}
		return d
	}
	if cos(v[0], v[1]) < 0.999 {
		t.Fatal("the same text must give the same vector")
	}
	if cos(v[0], v[2]) > 0.5 {
		t.Fatal("unrelated texts must not look alike")
	}
}
