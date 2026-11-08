// Command adaetum-embed is the embedding service: a small, stateless HTTP server that
// turns text into sentence vectors for Adaetum's semantic memory.
//
// It runs as its own pod, separate from the agent. Reasons: its CPU and memory
// footprint (ONNX Runtime and a 23 MB model) stay out of every host agent and out of
// the cluster agent; Kubernetes can reschedule it onto any healthy node because it
// holds no state; and because the agent only talks to it through one small HTTP
// contract, it can be replaced by another service (another pod, or an external API)
// without touching anything else.
//
// The model is MiniLM-L6-v2 quantised to INT8, run by ONNX Runtime, limited to one or
// two CPU cores. The binary is built twice: with `-tags onnx` for the image (needs the
// ONNX Runtime library), and without for development, where `-engine hash` runs a
// lexical stand-in that needs nothing.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Adaetum/Adaetum/embed/internal/engine"
	"github.com/Adaetum/Adaetum/embed/internal/server"
)

func main() {
	listen := flag.String("listen", ":8080", "address to listen on")
	kind := flag.String("engine", "onnx", "onnx (MiniLM-L6-v2 INT8) or hash (a lexical development stand-in)")
	ortLib := flag.String("ort-lib", "/opt/onnxruntime/lib/libonnxruntime.so", "path to the ONNX Runtime shared library")
	model := flag.String("model", "/models/model.onnx", "path to the ONNX model")
	vocab := flag.String("vocab", "/models/vocab.txt", "path to the WordPiece vocabulary")
	threads := flag.Int("threads", 2, "CPU cores ONNX Runtime may use")
	tokenFile := flag.String("token-file", "", "file holding a bearer token callers must present (optional; mode 600)")
	maxBatch := flag.Int("max-batch", server.DefaultMaxBatch, "most texts in one request")
	flag.Parse()

	var eng engine.Engine
	switch *kind {
	case "onnx":
		var err error
		if eng, err = engine.NewOnnx(*ortLib, *model, *vocab, *threads); err != nil {
			log.Fatalf("cannot start the model: %v", err)
		}
	case "hash":
		eng = engine.Hash{}
		log.Println("WARNING: running the lexical hash engine. Its vectors are not semantic and use a different model name, so they are never mixed with MiniLM's.")
	default:
		log.Fatalf("unknown -engine %q", *kind)
	}
	defer eng.Close()

	token := ""
	if *tokenFile != "" {
		data, err := os.ReadFile(*tokenFile)
		if err != nil {
			log.Fatalf("cannot read the token file: %v", err)
		}
		if token = strings.TrimSpace(string(data)); len(token) < 16 {
			log.Fatal("the token must be at least 16 characters")
		}
	}

	handler := (&server.Server{Engine: eng, Kind: *kind, Token: token, MaxBatch: *maxBatch, Concurrency: 1}).Handler()
	httpServer := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()

	fmt.Fprintf(os.Stderr, "adaetum-embed: %s, %d dimensions, listening on %s\n", eng.Model(), eng.Dim(), *listen)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
