# syntax=docker/dockerfile:1
#
# The embedding service image: the Go binary, ONNX Runtime, and the MiniLM-L6-v2 INT8
# model, with every downloaded file checked against a pinned SHA-256. Build context is
# this directory:
#
#   docker build -t adaetum-embed:0.1.0 adaetum/embed/
#
# linux/amd64 only for now (the cluster's servers are x86_64; the INT8 model file used is
# the AVX2 build). An arm64 image needs ONNX Runtime's aarch64 build and the model's
# arm64 file, so it is a different set of pins, not a flag.
#
# NOT BUILT HERE: this Dockerfile has not been run (no container runtime was available
# where it was written). The same binary, model and runtime were run together on macOS;
# see embed/README.md.

FROM golang:1.26.3-bookworm@sha256:386d475a660466863d9f8c766fec64d7fdad3edac2c6a05020c09534d71edb4b AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# cgo is required by the ONNX Runtime binding; the -tags onnx build is the real one.
RUN CGO_ENABLED=1 go build -tags onnx -trimpath -ldflags "-s -w" -o /out/adaetum-embed ./cmd/adaetum-embed

FROM debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc AS assets
ARG ORT_VERSION=1.31.1
ARG ORT_SHA256=c849e36a1ef3e22d4ff217b9d1ce4a3cf5b9467b975610913ae959ad5b16bdc9
ARG MODEL_SHA256=b941bf19f1f1283680f449fa6a7336bb5600bdcd5f84d10ddc5cd72218a0fd21
ARG VOCAB_SHA256=07eced375cec144d27c900241f3e339478dec958f92fddbc551f295c992038a3
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl && rm -rf /var/lib/apt/lists/*
WORKDIR /assets
RUN set -eu; \
    curl -fsSL -o ort.tgz "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-linux-x64-${ORT_VERSION}.tgz"; \
    curl -fsSL -o model.onnx "https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/main/onnx/model_quint8_avx2.onnx"; \
    curl -fsSL -o vocab.txt "https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/main/vocab.txt"; \
    echo "${ORT_SHA256}  ort.tgz" | sha256sum -c -; \
    echo "${MODEL_SHA256}  model.onnx" | sha256sum -c -; \
    echo "${VOCAB_SHA256}  vocab.txt" | sha256sum -c -; \
    mkdir -p /opt/onnxruntime /models; \
    tar xzf ort.tgz --strip-components=1 -C /opt/onnxruntime; \
    mv model.onnx vocab.txt /models/

FROM debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc
RUN apt-get update && apt-get install -y --no-install-recommends libstdc++6 libgomp1 && rm -rf /var/lib/apt/lists/* \
 && useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin embed
COPY --from=assets /opt/onnxruntime/lib /opt/onnxruntime/lib
COPY --from=assets /models /models
COPY --from=build /out/adaetum-embed /usr/local/bin/adaetum-embed
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/adaetum-embed"]
CMD ["-listen", ":8080", "-threads", "2"]
