import { Database } from "bun:sqlite";
import { chmodSync, existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { seedDemo } from "./demo";
import { createService } from "./service";
import type { Mode } from "./accounts";

// Entry point: reads its settings from the environment (set by the unit file or pod),
// opens its own database file, and listens on a socket only the Go agent can open (or,
// for development only, a loopback port).

function secretFromFile(path: string, create: boolean): string {
  if (existsSync(path)) return readFileSync(path, "utf8").trim();
  if (!create) throw new Error(`${path} does not exist`);
  mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
  const value = Buffer.from(crypto.getRandomValues(new Uint8Array(32))).toString("base64url");
  writeFileSync(path, value + "\n", { mode: 0o600 });
  return value;
}

// Everything this service writes (the database holds password hashes) is private to the
// user it runs as, whatever the system default is.
process.umask(0o077);

const stateDir = process.env.AUTH_STATE_DIR ?? ".";
mkdirSync(stateDir, { recursive: true, mode: 0o700 });
const db = new Database(process.env.AUTH_DB ?? join(stateDir, "auth.db"), { create: true });
db.run("PRAGMA journal_mode = WAL");
db.run("PRAGMA busy_timeout = 5000");

// This machine's own cookie-signing key. Sessions are never copied between machines, so
// it is never shared either.
const secret = process.env.AUTH_SECRET ?? secretFromFile(process.env.AUTH_SECRET_FILE ?? join(stateDir, "secret"), true);
const internalSecret = process.env.AUTH_INTERNAL_SECRET ?? secretFromFile(process.env.AUTH_INTERNAL_SECRET_FILE ?? join(stateDir, "internal-secret"), true);

const service = await createService({
  db,
  secret,
  internalSecret,
  baseURL: process.env.AUTH_BASE_URL ?? "http://localhost",
  mode: (process.env.AUTH_MODE as Mode | undefined) ?? "primary",
  setupCodeFile: join(stateDir, "setup-code"),
});

if ((await seedDemo(service, process.env)) === "created") {
  console.error("DEVELOPMENT ONLY: made the demo owner account (see auth/README.md). Never enable AUTH_DEMO on a real machine.");
}

const socket = process.env.AUTH_SOCKET;
if (socket) {
  Bun.serve({ unix: socket, fetch: service.fetch });
  chmodSync(socket, 0o600);
  console.error(`auth service listening on ${socket}`);
} else {
  // Development only: a loopback port, so the service can be tried without the agent.
  const port = Number(process.env.AUTH_PORT ?? 8787);
  Bun.serve({ hostname: "127.0.0.1", port, fetch: service.fetch });
  console.error(`auth service listening on http://127.0.0.1:${port} (development; use AUTH_SOCKET in production)`);
}
service.announce();
