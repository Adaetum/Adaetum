# Build the interface first so the same binary serves it in the pod and on hosts.
FROM node:24.11.1-bookworm-slim@sha256:48abc13a19400ca3985071e287bd405a1d99306770eb81d61202fb6b65cf0b57 AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# Vite's output path is relative to /web; copy from /internal below.
RUN npm run build

FROM golang:1.26.3-bookworm@sha256:386d475a660466863d9f8c766fec64d7fdad3edac2c6a05020c09534d71edb4b AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=web /internal/webui/ui/dist/ ./internal/webui/ui/dist/
ARG ADAETUM_VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.agentVersion=${ADAETUM_VERSION}" -o /out/adaetum-agent ./cmd/adaetum-agent

FROM debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* \
 && useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin adaetum
COPY --from=build /out/adaetum-agent /usr/local/bin/adaetum-agent
ENV TURSO_GO_CACHE_DIR=/var/lib/adaetum/cache
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/adaetum-agent"]
CMD ["cluster"]
