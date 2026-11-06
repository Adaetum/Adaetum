import { Database } from "bun:sqlite";
import { timingSafeEqual } from "node:crypto";
import { AccountStore, ROLES, type Mode, type Role } from "./accounts";
import { createAuth, createPerson, deletePerson, listPeople, PeopleError, revokeSessions, setRole } from "./auth";
import { SetupCode } from "./setup";

// The HTTP face of the auth service. Two audiences, kept apart:
//
//   PUBLIC  /api/auth/*, /api/setup, /api/auth-state: reached by browsers THROUGH the Go
//           agent, which proxies only these paths. Everything else is invisible to them.
//   INTERNAL /internal/*: reached only by the Go agent on this machine, over a socket
//           only it can open, and with a shared secret as well. These move account
//           copies, change people, and set the mode.

export interface ServiceOptions {
  db: Database;
  secret: string;
  internalSecret: string;
  baseURL: string;
  mode?: Mode;
  // Where the first-run setup code is kept; null keeps it in memory only (tests).
  setupCodeFile?: string | null;
  now?: () => number;
}

// A replica copies accounts from the cluster and never changes them itself, so it offers
// only what signing in needs. An allowlist, so a Better Auth endpoint added by an upgrade
// is closed here until someone decides it is safe.
const REPLICA_ALLOWED = new Set(["/get-session", "/sign-in/email", "/sign-out", "/ok"]);
const MAX_BODY = 5 * 1024 * 1024;

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

export async function createService(options: ServiceOptions) {
  const { db } = options;
  const { auth, store } = await createAuth({ db, secret: options.secret, baseURL: options.baseURL });
  const setup = new SetupCode(options.setupCodeFile ?? null, options.now);

  const readMode = (): Mode => {
    const saved = db.query("SELECT value FROM adaetum_meta WHERE key = 'mode'").get() as { value: string } | null;
    return (saved?.value as Mode | undefined) ?? options.mode ?? "primary";
  };
  const writeMode = (mode: Mode) =>
    db.run("INSERT INTO adaetum_meta (key, value) VALUES ('mode', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", [mode]);
  let settingUp = false;

  const needsSetup = () => readMode() === "primary" && store.countUsers() === 0;

  async function publicAuth(request: Request, path: string): Promise<Response> {
    if (readMode() === "replica" && !REPLICA_ALLOWED.has(path)) {
      return json(403, { error: "accounts_are_changed_on_the_cluster" });
    }
    return auth.handler(request);
  }

  async function body(request: Request): Promise<unknown | Response> {
    const declared = Number(request.headers.get("content-length") ?? "0");
    if (declared > MAX_BODY) return json(413, { error: "too_large" });
    const text = await request.text();
    if (text.length > MAX_BODY) return json(413, { error: "too_large" });
    try {
      return JSON.parse(text);
    } catch {
      return json(400, { error: "not_json" });
    }
  }

  async function firstOwner(request: Request): Promise<Response> {
    if (readMode() !== "primary") return json(409, { error: "waiting_for_cluster" });
    if (store.countUsers() > 0) return json(409, { error: "already_set_up" });
    const input = await body(request);
    if (input instanceof Response) return input;
    const { code, name, email, password } = (input ?? {}) as Record<string, string>;
    if (typeof code !== "string") return json(400, { error: "code_needed" });
    // One setup at a time: two people racing with the right code must not both succeed.
    if (settingUp) return json(409, { error: "setup_in_progress" });
    settingUp = true;
    try {
      const checked = setup.check(code);
      if (checked === "locked") return json(429, { error: "too_many_tries" });
      if (checked !== "ok") return json(403, { error: "wrong_code" });
      await createPerson(auth, store, { name: String(name ?? ""), email: String(email ?? ""), password: String(password ?? ""), role: "owner" });
      setup.consume();
      return json(201, { ok: true });
    } catch (error) {
      if (error instanceof PeopleError) return json(400, { error: error.code, message: error.message });
      throw error;
    } finally {
      settingUp = false;
    }
  }

  async function internal(request: Request, path: string): Promise<Response> {
    const method = request.method;
    if (path === "/internal/state" && method === "GET") return json(200, { mode: readMode(), ...store.state() });
    if (path === "/internal/snapshot" && method === "GET") {
      const snapshot = store.snapshot();
      return snapshot ? json(200, snapshot) : json(404, { error: "nobody_to_copy" });
    }
    if (path === "/internal/snapshot" && method === "POST") {
      const input = await body(request);
      if (input instanceof Response) return input;
      // ?takeover=1 is how the agent says "the cluster's list replaces yours" (see apply).
      const result = store.apply(input, readMode(), new URL(request.url).searchParams.get("takeover") === "1");
      return json(result.status === "rejected" ? 422 : 200, result);
    }
    if (path === "/internal/mode" && method === "POST") {
      const input = (await body(request)) as { mode?: string };
      if (input instanceof Response) return input;
      if (input?.mode !== "primary" && input?.mode !== "replica") return json(400, { error: "mode_must_be_primary_or_replica" });
      writeMode(input.mode);
      return json(200, { mode: input.mode });
    }
    if (path === "/internal/people" && method === "GET") return json(200, { mode: readMode(), people: listPeople(db) });

    // Everything below changes accounts, which only the primary does.
    if (path.startsWith("/internal/people")) {
      if (readMode() !== "primary") return json(409, { error: "accounts_are_changed_on_the_cluster" });
      try {
        if (path === "/internal/people" && method === "POST") {
          const input = await body(request);
          if (input instanceof Response) return input;
          const { name, email, password, role } = (input ?? {}) as Record<string, string>;
          return json(201, await createPerson(auth, store, { name: String(name ?? ""), email: String(email ?? ""), password: String(password ?? ""), role: role as Role }));
        }
        const match = /^\/internal\/people\/([^/]+)(?:\/(role|revoke-sessions))?$/.exec(path);
        if (match) {
          const id = decodeURIComponent(match[1]);
          if (!match[2] && method === "DELETE") return deletePerson(db, store, id), json(200, { ok: true });
          if (match[2] === "revoke-sessions" && method === "POST") return revokeSessions(db, id), json(200, { ok: true });
          if (match[2] === "role" && method === "POST") {
            const input = (await body(request)) as { role?: string };
            if (input instanceof Response) return input;
            if (!ROLES.includes(input?.role as Role)) return json(400, { error: "invalid", message: "that role does not exist" });
            return setRole(db, store, id, input.role as Role), json(200, { ok: true });
          }
        }
      } catch (error) {
        if (error instanceof PeopleError) return json(error.code === "not_found" ? 404 : error.code === "last_owner" ? 409 : 400, { error: error.code, message: error.message });
        throw error;
      }
    }
    return json(404, { error: "not_found" });
  }

  const isInternalCaller = (request: Request) => {
    const given = Buffer.from(request.headers.get("x-adaetum-internal") ?? "");
    const expected = Buffer.from(options.internalSecret);
    return given.length === expected.length && given.length > 0 && timingSafeEqual(given, expected);
  };

  async function fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);
    const path = url.pathname;
    if (path === "/healthz") return new Response("ok");
    if (path.startsWith("/internal/")) {
      // Unknown to everyone but the agent: no hint that these routes exist.
      return isInternalCaller(request) ? internal(request, path) : json(404, { error: "not_found" });
    }
    if (path === "/api/auth-state" && request.method === "GET") {
      return json(200, { enabled: true, mode: readMode(), needsSetup: needsSetup(), hasAccounts: store.countUsers() > 0 });
    }
    if (path === "/api/setup" && request.method === "POST") return firstOwner(request);
    if (path.startsWith("/api/auth/")) return publicAuth(request, path.slice("/api/auth".length));
    return json(404, { error: "not_found" });
  }

  // announce prints the setup code where only someone at the machine sees it.
  function announce(log: (line: string) => void = console.error): void {
    if (needsSetup()) log(`Adaetum first-run setup code: ${setup.ensure()}  (enter it on the sign-in page to create the first owner)`);
  }

  return { fetch, auth, store, db, setup, announce, mode: readMode };
}
