//go:build onnx

package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/Adaetum/Adaetum/embed/internal/wordpiece"
)

// ModelName is the embedding space this engine produces. Anything stored with this
// name can be compared with anything else stored with it.
const ModelName = "minilm-l6-v2-int8"

const (
	// MaxSequence is the longest input in tokens. MiniLM-L6-v2 was trained at 128 and
	// supports 256; longer text is cut, which for notes and decisions is rarely hit.
	MaxSequence = 256
	hiddenSize  = 384
	// batchSize is 1 on purpose. The INT8 model quantises activations dynamically, so
	// its scale depends on everything in the tensor, padding included: the same text
	// embedded alone and inside a padded batch differs slightly (cosine about 0.99). A
	// vector must depend only on its own text, because stored vectors (embedded at
	// different times, in different batches) are compared with queries. One sequence per
	// run costs little at this service's size and makes the vectors reproducible.
	batchSize = 1
)

// Onnx runs MiniLM-L6-v2 (INT8) through ONNX Runtime. It needs the ONNX Runtime
// shared library at run time, which is why it is behind the `onnx` build tag: the
// default build has no native dependency at all.
type Onnx struct {
	tokenizer *wordpiece.Tokenizer
	session   *ort.DynamicAdvancedSession
	// ONNX Runtime sessions are safe to share, but one request at a time keeps memory
	// flat on the small CPU budget this service runs with.
	mu sync.Mutex
}

var initOnce sync.Once
var initErr error

// NewOnnx loads the runtime library, the model and the vocabulary. threads limits the
// CPU cores ONNX Runtime may use (the pod is limited to one or two).
func NewOnnx(libraryPath, modelPath, vocabPath string, threads int) (Engine, error) {
	tokenizer, err := wordpiece.Load(vocabPath)
	if err != nil {
		return nil, fmt.Errorf("loading the vocabulary: %w", err)
	}
	initOnce.Do(func() {
		ort.SetSharedLibraryPath(libraryPath)
		initErr = ort.InitializeEnvironment()
	})
	if initErr != nil {
		return nil, fmt.Errorf("starting ONNX Runtime (%s): %w", libraryPath, initErr)
	}
	options, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	defer options.Destroy()
	if threads < 1 {
		threads = 1
	}
	if err := options.SetIntraOpNumThreads(threads); err != nil {
		return nil, err
	}
	if err := options.SetInterOpNumThreads(1); err != nil {
		return nil, err
	}
	session, err := ort.NewDynamicAdvancedSession(modelPath,
		[]string{"input_ids", "attention_mask", "token_type_ids"}, []string{"last_hidden_state"}, options)
	if err != nil {
		return nil, fmt.Errorf("loading the model %s: %w", modelPath, err)
	}
	return &Onnx{tokenizer: tokenizer, session: session}, nil
}

// Model implements Engine.
func (o *Onnx) Model() string { return ModelName }

// Dim implements Engine.
func (o *Onnx) Dim() int { return hiddenSize }

// Close implements Engine.
func (o *Onnx) Close() error { return o.session.Destroy() }

// Embed implements Engine.
func (o *Onnx) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += batchSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(start+batchSize, len(texts))
		vectors, err := o.embedBatch(texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vectors...)
	}
	return out, nil
}

func (o *Onnx) embedBatch(texts []string) ([][]float32, error) {
	encoded := make([][]int64, len(texts))
	longest := 0
	for i, text := range texts {
		encoded[i] = o.tokenizer.Encode(text, MaxSequence)
		longest = max(longest, len(encoded[i]))
	}
	batch := len(texts)
	ids := make([]int64, batch*longest)
	mask := make([]int64, batch*longest)
	types := make([]int64, batch*longest) // all zeros: single-sentence input
	for i, tokens := range encoded {
		for j := 0; j < longest; j++ {
			if j < len(tokens) {
				ids[i*longest+j] = tokens[j]
				mask[i*longest+j] = 1
			} else {
				ids[i*longest+j] = o.tokenizer.PadID
			}
		}
	}

	shape := ort.NewShape(int64(batch), int64(longest))
	idsTensor, err := ort.NewTensor(shape, ids)
	if err != nil {
		return nil, err
	}
	defer idsTensor.Destroy()
	maskTensor, err := ort.NewTensor(shape, mask)
	if err != nil {
		return nil, err
	}
	defer maskTensor.Destroy()
	typesTensor, err := ort.NewTensor(shape, types)
	if err != nil {
		return nil, err
	}
	defer typesTensor.Destroy()

	o.mu.Lock()
	outputs := []ort.Value{nil} // nil: ONNX Runtime allocates it
	err = o.session.Run([]ort.Value{idsTensor, maskTensor, typesTensor}, outputs)
	o.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("running the model: %w", err)
	}
	defer outputs[0].Destroy()

	hidden, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, errors.New("the model's output was not a float tensor")
	}
	got := hidden.GetShape()
	if len(got) != 3 || got[0] != int64(batch) || got[1] != int64(longest) || got[2] != hiddenSize {
		return nil, fmt.Errorf("the model returned shape %v, expected [%d %d %d]", got, batch, longest, hiddenSize)
	}
	return MeanPool(hidden.GetData(), mask, batch, longest, hiddenSize), nil
}
