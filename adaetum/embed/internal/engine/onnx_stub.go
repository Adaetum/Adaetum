//go:build !onnx

package engine

import "errors"

// ModelName is the embedding space the ONNX engine produces.
const ModelName = "minilm-l6-v2-int8"

// NewOnnx is unavailable in a build without the `onnx` tag. Build with
// `-tags onnx` (the container image does) to include it.
func NewOnnx(_, _, _ string, _ int) (Engine, error) {
	return nil, errors.New("this build does not include ONNX Runtime support; rebuild with -tags onnx, or use -engine hash for development")
}
