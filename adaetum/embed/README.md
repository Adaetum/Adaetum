# Adaetum embedding service

A small, **stateless** HTTP service that turns text into sentence vectors for Adaetum's
semantic memory. It runs as **its own pod**, separate from the agent, so its footprint
stays out of every host and the cluster agent, Kubernetes can move it to any healthy node,
and it can be replaced (another pod, or an external service) behind the same small
contract without touching anything else. The agent's pages for managing it are under
**Embedding** in the web interface.

- Model: all-MiniLM-L6-v2 quantised to INT8, run by ONNX Runtime, limited to one or two
  CPU cores. 384 dimensions, vectors are unit length (cosine similarity is a dot product).
- Tokenizer: BERT WordPiece, in pure Go (`internal/wordpiece`), checked against ids from
  the real vocabulary.
- One sequence per run, on purpose: the INT8 model's dynamic quantisation makes a vector
  depend on its batch, and stored vectors are compared with later queries, so a vector
  must depend only on its own text.

## Contract

```
POST /embed    {"texts": ["...", "..."]}
200            {"model": "minilm-l6-v2-int8", "dim": 384, "vectors": [[...], [...]]}
GET  /healthz  liveness     GET /readyz  readiness (embeds a test text)     GET /info
```

Limits: 64 texts per request, 16 KiB per text, 4 MiB per body; one embed at a time, a
caller that waits more than 10 s is told "busy". An optional bearer token
(`-token-file`) is a second lock behind the NetworkPolicy.

## Build and run

Two builds, so the default has no native dependency:

```bash
# Development: no model, no native library. A lexical stand-in, clearly named
# "hash-lexical-256", so its vectors are never mixed with MiniLM's.
go run ./cmd/adaetum-embed -engine hash -listen 127.0.0.1:8700

# Real model (needs cgo, ONNX Runtime 1.31.x for this binding, and the model files):
CGO_ENABLED=1 go build -tags onnx -o adaetum-embed ./cmd/adaetum-embed
./adaetum-embed -ort-lib /path/libonnxruntime.dylib -model model.onnx -vocab vocab.txt
```

Tests run without the model. To run the real-model checks:

```bash
EMBED_MODEL_DIR=/dir/with/model.onnx-and-vocab.txt \
EMBED_ORT_LIB=/path/libonnxruntime.dylib CGO_ENABLED=1 go test -tags onnx ./...
```

## What was verified, and what was not

Verified on macOS arm64 with the real INT8 model and ONNX Runtime 1.31.1: the service
answers over HTTP, and a sentence about a failing storage drive is clearly nearer a
sentence about hard-disk faults that shares no words (cosine about 0.36) than to unrelated
text (about 0.03). The tokenizer matches the real vocabulary's ids.

**Not verified:** the `Dockerfile` (no container runtime was available), the Linux x86_64
model and runtime files together (their SHA-256 are pinned, the files were downloaded and
hashed, but not run), and the manifests against a live cluster.

## Deploying

`deploy/` is a kustomize base (namespace `adaetum`, one replica, restricted pod security,
a NetworkPolicy that admits only the cluster agent and allows no egress). It is **not**
wired into `pods/` yet: that needs a published image, and the repository has no pipeline
that builds this one. Build the image, push it, set it with `kustomize edit set image`,
then `kubectl apply -k deploy`. The cluster agent finds it at
`http://adaetum-embed.adaetum.svc` (Embedding page: "The embedding service in the
cluster").
