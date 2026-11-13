import { Database } from "bun:sqlite";
import { chmodSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { reconcileBreakGlass } from "./breakglass";
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

// The break-glass owner comes from OpenBao as files (see breakglass.ts). It is checked now and
// every minute, since the mounted files change in place when the password is rotated. Only
// the machine that owns the accounts does this: a replica receives them from the cluster.
const breakGlassDir = process.env.AUTH_BREAKGLASS_DIR;
if (breakGlassDir) {
  const reconcile = async () => {
    if (service.mode() !== "primary") return;
    try {
      const result = await reconcileBreakGlass(service, breakGlassDir);
      if (result === "created" || result === "updated") console.error(`break-glass owner ${result}`);
    } catch (error) {
      console.error("cannot reconcile the break-glass owner:", error instanceof Error ? error.message : error);
    }
  };
  await reconcile();
  setInterval(reconcile, 60_000);
}

const socket = process.env.AUTH_SOCKET;
if (socket) {
  // A socket file outlives its process (a restart, a reboot, a crash), and listening on a
  // path that exists fails with EADDRINUSE. This service is the only thing that owns the
  // path and runs once per machine, so whatever is left there is stale.
  rmSync(socket, { force: true });
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
