import { Database } from "bun:sqlite";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createService } from "./service";
import type { Mode } from "./accounts";

// Test helpers: a "machine" is one auth service with its own database, like one host or
// the pod. Requests go straight to its fetch handler, the same code the socket serves.

export const INTERNAL = "test-internal-secret";
export const PASSWORD = "correct horse battery staple";
const ORIGIN = "http://machine.test";

export type Machine = Awaited<ReturnType<typeof createService>> & { clock: { now: number } };

export async function machine(mode: Mode = "primary", options: { file?: string; setupCodeFile?: string | null } = {}): Promise<Machine> {
  const clock = { now: 1_700_000_000_000 };
  const service = await createService({
    db: new Database(options.file ?? ":memory:"),
    secret: "test-secret-test-secret-test-secret-0123456789",
    internalSecret: INTERNAL,
    baseURL: ORIGIN,
    mode,
    setupCodeFile: options.setupCodeFile ?? null,
    now: () => clock.now,
  });
  return Object.assign(service, { clock });
}

export function tempDir(): string {
  return mkdtempSync(join(tmpdir(), "adaetum-auth-"));
}

interface CallOptions {
  internal?: boolean | string;
  cookie?: string;
  origin?: string | null;
  raw?: string;
}

export async function call(m: Machine, method: string, path: string, body?: unknown, options: CallOptions = {}) {
  const headers: Record<string, string> = { host: "machine.test" };
  if (body !== undefined || options.raw !== undefined) headers["content-type"] = "application/json";
  if (options.internal) headers["x-adaetum-internal"] = options.internal === true ? INTERNAL : options.internal;
  if (options.cookie) headers.cookie = options.cookie;
  // Browsers send Origin on same-origin POSTs; Better Auth checks it.
  if (options.origin !== null && method !== "GET") headers.origin = options.origin ?? ORIGIN;
  const response = await m.fetch(new Request(ORIGIN + path, { method, headers, body: options.raw ?? (body === undefined ? undefined : JSON.stringify(body)) }));
  const text = await response.text();
  let data: any = text;
  try { data = JSON.parse(text); } catch { /* plain text */ }
  return { status: response.status, data, response };
}

export const internal = (m: Machine, method: string, path: string, body?: unknown) => call(m, method, path, body, { internal: true });

// setUpOwner runs the real first-run flow with the code the machine printed.
export async function setUpOwner(m: Machine, email = "owner@example.test", password = PASSWORD) {
  const code = m.setup.ensure();
  const result = await call(m, "POST", "/api/setup", { code, name: "Owner", email, password });
  if (result.status !== 201) throw new Error(`setup failed: ${JSON.stringify(result.data)}`);
}

export async function addPerson(m: Machine, email: string, role = "member", password = PASSWORD) {
  const result = await internal(m, "POST", "/internal/people", { name: email.split("@")[0], email, password, role });
  if (result.status !== 201) throw new Error(`add failed: ${JSON.stringify(result.data)}`);
  return result.data as { id: string };
}

// signIn returns the session cookie, or null when the machine refuses.
export async function signIn(m: Machine, email: string, password = PASSWORD): Promise<string | null> {
  const result = await call(m, "POST", "/api/auth/sign-in/email", { email, password });
  if (result.status !== 200) return null;
  const set = result.response.headers.getSetCookie().find((c) => c.startsWith("better-auth.session_token"));
  return set ? set.split(";")[0] : null;
}

export async function sessionOf(m: Machine, cookie: string): Promise<{ user: { email: string; role: string } } | null> {
  const result = await call(m, "GET", "/api/auth/get-session", undefined, { cookie });
  return result.data && typeof result.data === "object" && result.data.user ? result.data : null;
}

// copy moves the accounts from one machine to another the way the Go agent does:
// read the snapshot, hand it over. Returns the receiver's answer.
export async function copy(from: Machine, to: Machine) {
  const snapshot = await internal(from, "GET", "/internal/snapshot");
  if (snapshot.status !== 200) return snapshot;
  return internal(to, "POST", "/internal/snapshot", snapshot.data);
}

export const state = async (m: Machine) => (await internal(m, "GET", "/internal/state")).data as { mode: string; epoch: string; version: number; users: number; owners: number };
