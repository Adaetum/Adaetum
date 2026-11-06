import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Provided by Node when Vite runs this file; declared here so the UI project does
// not need the Node type definitions just for one variable.
declare const process: { env: Record<string, string | undefined> };

// In development the UI talks to a locally running agent; the proxy keeps the
// browser on one origin so the agent needs no CORS handling.
export default defineConfig({
  plugins: [react()],
  // The build is embedded in the agent binary (internal/webui), so a host serves its
  // own interface with nothing else installed. The directory is git-ignored (any
  // directory named dist is); the Go package keeps a tracked placeholder beside it.
  build: { outDir: "../internal/webui/ui/dist", emptyOutDir: true },
  server: {
    // AGENT_URL points the dev server at another agent, for example a host agent
    // started with -listen 0.0.0.0:8443 (https, self-signed, hence secure: false).
    proxy: { "/api": { target: process.env.AGENT_URL ?? "http://127.0.0.1:8080", secure: false } },
  },
});
