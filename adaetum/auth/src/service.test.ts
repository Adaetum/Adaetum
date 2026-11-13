import { describe, expect, test } from "bun:test";
import { join } from "node:path";
import { PASSWORD, addPerson, call, copy, internal, machine, sessionOf, setUpOwner, signIn, state, tempDir } from "./helpers";

describe("first-run setup", () => {
  test("a fresh machine asks for setup and shows a code only on the machine", async () => {
    const m = await machine();
    expect((await call(m, "GET", "/api/auth-state")).data).toMatchObject({ enabled: true, mode: "primary", needsSetup: true, hasAccounts: false });
    const printed: string[] = [];
    m.announce((line) => printed.push(line));
    expect(printed[0]).toContain(m.setup.ensure());
    // The code is never in any public answer.
    expect(JSON.stringify((await call(m, "GET", "/api/auth-state")).data)).not.toContain(m.setup.ensure());
  });

  test("a wrong code is refused, and five wrong tries lock it for a minute", async () => {
    const m = await machine();
    const body = { name: "Mallory", email: "m@example.test", password: PASSWORD };
    for (let i = 0; i < 4; i++) expect((await call(m, "POST", "/api/setup", { ...body, code: "AAAA-BBBB-CCCC" })).status).toBe(403);
    expect((await call(m, "POST", "/api/setup", { ...body, code: "AAAA-BBBB-CCCC" })).status).toBe(429);
    // Even the right code is refused while locked.
    expect((await call(m, "POST", "/api/setup", { ...body, code: m.setup.ensure() })).status).toBe(429);
    m.clock.now += 61_000;
    expect((await call(m, "POST", "/api/setup", { ...body, code: m.setup.ensure() })).status).toBe(201);
  });

  test("the right code makes the first owner once, and then setup is closed", async () => {
    const m = await machine();
    await setUpOwner(m);
    expect((await call(m, "GET", "/api/auth-state")).data).toMatchObject({ needsSetup: false, hasAccounts: true });
    expect((await call(m, "POST", "/api/setup", { code: "AAAA-BBBB-CCCC", name: "X", email: "x@example.test", password: PASSWORD })).status).toBe(409);
    expect((await state(m)).owners).toBe(1);
  });

  test("two people racing with the right code create only one owner", async () => {
    const m = await machine();
    const code = m.setup.ensure();
    const attempt = (email: string) => call(m, "POST", "/api/setup", { code, name: "N", email, password: PASSWORD });
    const results = await Promise.all([attempt("a@example.test"), attempt("b@example.test")]);
    expect(results.map((r) => r.status).sort()).toEqual([201, 409]);
    expect((await state(m)).users).toBe(1);
  });

  test("a weak password or bad email is refused without using up the code", async () => {
    const m = await machine();
    const code = m.setup.ensure();
    expect((await call(m, "POST", "/api/setup", { code, name: "N", email: "n@example.test", password: "short" })).status).toBe(400);
    expect((await call(m, "POST", "/api/setup", { code, name: "N", email: "not-an-email", password: PASSWORD })).status).toBe(400);
    expect((await call(m, "POST", "/api/setup", { code, name: "N", email: "n@example.test", password: PASSWORD })).status).toBe(201);
  });

  test("a machine that joined a cluster offers no setup: accounts arrive from the cluster", async () => {
    const m = await machine("replica");
    expect((await call(m, "GET", "/api/auth-state")).data).toMatchObject({ mode: "replica", needsSetup: false, hasAccounts: false });
    expect((await call(m, "POST", "/api/setup", { code: m.setup.ensure(), name: "N", email: "n@example.test", password: PASSWORD })).status).toBe(409);
  });

  test("the same code is shown again after a restart", async () => {
    const dir = tempDir();
    const file = join(dir, "setup-code");
    const first = await machine("primary", { setupCodeFile: file });
    const code = first.setup.ensure();
    const second = await machine("primary", { setupCodeFile: file });
    expect(second.setup.ensure()).toBe(code);
    await setUpOwner(second);
    // Once used, the code is gone from disk.
    expect(await Bun.file(file).exists()).toBe(false);
  });
});

describe("signing in", () => {
  test("the right password signs in; the wrong one, an unknown email and cross-origin posts do not", async () => {
    const m = await machine();
    await setUpOwner(m);
    expect(await signIn(m, "owner@example.test")).toBeTruthy();
    expect(await signIn(m, "owner@example.test", "wrong wrong wrong wrong")).toBeNull();
    expect(await signIn(m, "nobody@example.test")).toBeNull();
    // Better Auth skips its origin check when NODE_ENV is "test" (which `bun test` sets),
    // so the real behaviour is checked in a child process outside it.
    const child = Bun.spawnSync(["bun", "src/origin-check.ts"], { env: { ...process.env, NODE_ENV: "production" }, cwd: join(import.meta.dir, "..") });
    const seen = JSON.parse(child.stdout.toString().trim().split("\n").pop() ?? "{}");
    expect(seen.cross).toBe(403); // another site cannot make the browser sign someone in
    expect(seen.same).toBe(200);
  });

  test("open sign-up is closed", async () => {
    const m = await machine();
    await setUpOwner(m);
    expect((await call(m, "POST", "/api/auth/sign-up/email", { email: "x@example.test", password: PASSWORD, name: "X" })).status).toBeGreaterThanOrEqual(400);
    expect((await state(m)).users).toBe(1);
  });

  test("the session says who the person is and their role", async () => {
    const m = await machine();
    await setUpOwner(m);
    const cookie = (await signIn(m, "owner@example.test"))!;
    expect(await sessionOf(m, cookie)).toMatchObject({ user: { email: "owner@example.test", role: "owner" } });
    expect(await sessionOf(m, "better-auth.session_token=forged.value")).toBeNull();
  });

  test("signing out ends the session", async () => {
    const m = await machine();
    await setUpOwner(m);
    const cookie = (await signIn(m, "owner@example.test"))!;
    expect((await call(m, "POST", "/api/auth/sign-out", {}, { cookie })).status).toBe(200);
    expect(await sessionOf(m, cookie)).toBeNull();
  });
});

describe("the internal door", () => {
  test("it is invisible without the shared secret", async () => {
    const m = await machine();
    await setUpOwner(m);
    expect((await call(m, "GET", "/internal/state")).status).toBe(404);
    expect((await call(m, "GET", "/internal/state", undefined, { internal: "wrong-secret" })).status).toBe(404);
    expect((await call(m, "GET", "/internal/snapshot")).status).toBe(404);
    expect((await call(m, "GET", "/internal/people")).status).toBe(404);
    expect((await internal(m, "GET", "/internal/state")).status).toBe(200);
  });

  test("oversized and malformed bodies are refused", async () => {
    const m = await machine();
    await setUpOwner(m);
    expect((await call(m, "POST", "/internal/snapshot", undefined, { internal: true, raw: "{not json" })).status).toBe(400);
    expect((await call(m, "POST", "/internal/snapshot", undefined, { internal: true, raw: "x".repeat(5 * 1024 * 1024 + 1) })).status).toBe(413);
  });

  test("the mode survives a restart", async () => {
    const file = join(tempDir(), "auth.db");
    const first = await machine("primary", { file });
    expect((await internal(first, "POST", "/internal/mode", { mode: "replica" })).status).toBe(200);
    const second = await machine("primary", { file });
    expect(second.mode()).toBe("replica");
    expect((await internal(second, "POST", "/internal/mode", { mode: "banana" })).status).toBe(400);
  });
});

describe("people", () => {
  test("add, list, change role, and remove", async () => {
    const m = await machine();
    await setUpOwner(m);
    const member = await addPerson(m, "sam@example.test");
    const listed = (await internal(m, "GET", "/internal/people")).data.people.map((p: any) => [p.email, p.role]);
    expect(listed).toContainEqual(["sam@example.test", "member"]);
    expect((await internal(m, "POST", `/internal/people/${member.id}/role`, { role: "admin" })).status).toBe(200);
    expect(await sessionOf(m, (await signIn(m, "sam@example.test"))!)).toMatchObject({ user: { role: "admin" } });
    expect((await internal(m, "DELETE", `/internal/people/${member.id}`)).status).toBe(200);
    expect(await signIn(m, "sam@example.test")).toBeNull();
  });

  test("the same email twice (any case) is refused", async () => {
    const m = await machine();
    await setUpOwner(m);
    await addPerson(m, "sam@example.test");
    expect((await internal(m, "POST", "/internal/people", { name: "S", email: "SAM@example.test", password: PASSWORD, role: "member" })).status).toBe(400);
  });

  test("the last owner cannot be removed or demoted, but one of two can", async () => {
    const m = await machine();
    await setUpOwner(m);
    const owner = (await internal(m, "GET", "/internal/people")).data.people[0];
    expect((await internal(m, "DELETE", `/internal/people/${owner.id}`)).status).toBe(409);
    expect((await internal(m, "POST", `/internal/people/${owner.id}/role`, { role: "member" })).status).toBe(409);
    const second = await addPerson(m, "two@example.test", "owner");
    expect((await internal(m, "POST", `/internal/people/${owner.id}/role`, { role: "admin" })).status).toBe(200);
    expect((await internal(m, "DELETE", `/internal/people/${second.id}`)).status).toBe(409); // now the last owner
  });

  test("unknown people and roles are refused", async () => {
    const m = await machine();
    await setUpOwner(m);
    expect((await internal(m, "DELETE", "/internal/people/nobody")).status).toBe(404);
    expect((await internal(m, "POST", "/internal/people", { name: "S", email: "s@example.test", password: PASSWORD, role: "emperor" })).status).toBe(400);
  });

  test("a replica refuses to change people", async () => {
    const m = await machine("replica");
    expect((await internal(m, "POST", "/internal/people", { name: "S", email: "s@example.test", password: PASSWORD, role: "member" })).status).toBe(409);
  });
});

describe("the version counter", () => {
  test("every change to people or passwords moves it; sign-ins and sessions do not", async () => {
    const m = await machine();
    await setUpOwner(m);
    const v0 = (await state(m)).version;
    expect(v0).toBeGreaterThan(0);
    await signIn(m, "owner@example.test");
    await signIn(m, "owner@example.test");
    expect((await state(m)).version).toBe(v0); // sessions are not accounts
    const person = await addPerson(m, "sam@example.test");
    const v1 = (await state(m)).version;
    expect(v1).toBeGreaterThan(v0);
    await internal(m, "POST", `/internal/people/${person.id}/role`, { role: "admin" });
    const v2 = (await state(m)).version;
    expect(v2).toBeGreaterThan(v1);
    const cookie = (await signIn(m, "sam@example.test"))!;
    expect((await call(m, "POST", "/api/auth/change-password", { currentPassword: PASSWORD, newPassword: "another long password!" }, { cookie })).status).toBe(200);
    expect((await state(m)).version).toBeGreaterThan(v2);
    await internal(m, "DELETE", `/internal/people/${person.id}`);
    expect((await state(m)).version).toBeGreaterThan(v2);
  });
});

describe("copying accounts to another machine", () => {
  test("a new host receives the accounts and signs people in without the cluster", async () => {
    const pod = await machine("primary");
    await setUpOwner(pod);
    await addPerson(pod, "sam@example.test");
    const host = await machine("replica");
    expect((await copy(pod, host)).data.status).toBe("applied");
    expect(await signIn(host, "owner@example.test")).toBeTruthy();
    expect(await signIn(host, "sam@example.test")).toBeTruthy();
    expect(await signIn(host, "owner@example.test", "wrong wrong wrong wrong")).toBeNull();
    expect(await state(host)).toMatchObject({ epoch: (await state(pod)).epoch, version: (await state(pod)).version, users: 2 });
  });

  test("the first owner made on a host seeds an empty pod, and then both agree", async () => {
    const host = await machine("primary"); // installed before any cluster existed
    await setUpOwner(host);
    const pod = await machine("primary"); // new and empty
    expect((await copy(host, pod)).data.status).toBe("applied");
    expect(await signIn(pod, "owner@example.test")).toBeTruthy();
    await internal(host, "POST", "/internal/mode", { mode: "replica" });
    expect((await state(pod)).epoch).toBe((await state(host)).epoch);
    // From now on the pod is the owner: a new person added there reaches the host.
    await addPerson(pod, "sam@example.test");
    expect((await copy(pod, host)).data.status).toBe("applied");
    expect(await signIn(host, "sam@example.test")).toBeTruthy();
  });

  test("copying the same version again changes nothing", async () => {
    const pod = await machine();
    await setUpOwner(pod);
    const host = await machine("replica");
    await copy(pod, host);
    const before = await state(host);
    expect((await copy(pod, host)).data.status).toBe("current");
    expect(await state(host)).toEqual(before);
  });

  test("a new person appears on the host, and a removed one is gone along with their session", async () => {
    const pod = await machine();
    await setUpOwner(pod);
    const sam = await addPerson(pod, "sam@example.test");
    const host = await machine("replica");
    await copy(pod, host);
    const samCookie = (await signIn(host, "sam@example.test"))!;
    expect(await sessionOf(host, samCookie)).not.toBeNull();

    await addPerson(pod, "kim@example.test");
    await internal(pod, "DELETE", `/internal/people/${sam.id}`);
    expect((await copy(pod, host)).data.status).toBe("applied");
    expect(await signIn(host, "kim@example.test")).toBeTruthy();
    expect(await signIn(host, "sam@example.test")).toBeNull();
    expect(await sessionOf(host, samCookie)).toBeNull();
  });

  test("people who stay allowed keep their sessions across a copy", async () => {
    const pod = await machine();
    await setUpOwner(pod);
    const host = await machine("replica");
    await copy(pod, host);
    const cookie = (await signIn(host, "owner@example.test"))!;
    await addPerson(pod, "sam@example.test");
    await copy(pod, host);
    expect(await sessionOf(host, cookie)).not.toBeNull();
  });

  test("a changed password signs that person out of the host, and only them", async () => {
    const pod = await machine();
    await setUpOwner(pod);
    await addPerson(pod, "sam@example.test");
    const host = await machine("replica");
    await copy(pod, host);
    const owner = (await signIn(host, "owner@example.test"))!;
    const sam = (await signIn(host, "sam@example.test"))!;

    const podSam = (await signIn(pod, "sam@example.test"))!;
    await call(pod, "POST", "/api/auth/change-password", { currentPassword: PASSWORD, newPassword: "a brand new password!" }, { cookie: podSam });
    await copy(pod, host);
    expect(await sessionOf(host, sam)).toBeNull();
    expect(await sessionOf(host, owner)).not.toBeNull();
    expect(await signIn(host, "sam@example.test", "a brand new password!")).toBeTruthy();
    expect(await signIn(host, "sam@example.test")).toBeNull();
  });

  test("signing someone out everywhere reaches the host on the next copy", async () => {
    const pod = await machine();
    await setUpOwner(pod);
    const sam = await addPerson(pod, "sam@example.test");
    const host = await machine("replica");
    await copy(pod, host);
    const cookie = (await signIn(host, "sam@example.test"))!;
    await new Promise((r) => setTimeout(r, 5));
    expect((await internal(pod, "POST", `/internal/people/${sam.id}/revoke-sessions`)).status).toBe(200);
    expect((await copy(pod, host)).data.status).toBe("applied");
    expect(await sessionOf(host, cookie)).toBeNull();
    // They can sign in again, and that new session lasts.
    const again = (await signIn(host, "sam@example.test"))!;
    await new Promise((r) => setTimeout(r, 5));
    await addPerson(pod, "kim@example.test");
    await copy(pod, host);
    expect(await sessionOf(host, again)).not.toBeNull();
  });

  test("a role change reaches the host", async () => {
    const pod = await machine();
    await setUpOwner(pod);
    const sam = await addPerson(pod, "sam@example.test");
    const host = await machine("replica");
    await copy(pod, host);
    const cookie = (await signIn(host, "sam@example.test"))!;
    await internal(pod, "POST", `/internal/people/${sam.id}/role`, { role: "admin" });
    await copy(pod, host);
    expect(await sessionOf(host, cookie)).toMatchObject({ user: { role: "admin" } });
  });

  test("host sessions are never copied back to the pod", async () => {
    const pod = await machine();
    await setUpOwner(pod);
    const host = await machine("replica");
    await copy(pod, host);
    await signIn(host, "owner@example.test");
    const rows = (m: typeof host) => (m.db.query("SELECT count(*) c FROM session").get() as { c: number }).c;
    expect(rows(host)).toBe(1);
    expect(rows(pod)).toBe(0);
    const snapshot = (await internal(host, "GET", "/internal/snapshot")).data;
    expect(JSON.stringify(snapshot)).not.toContain("token");
  });

  test("a replica offers only what signing in needs", async () => {
    const pod = await machine();
    await setUpOwner(pod);
    const host = await machine("replica");
    await copy(pod, host);
    const cookie = (await signIn(host, "owner@example.test"))!;
    for (const path of ["/api/auth/change-password", "/api/auth/update-user", "/api/auth/change-email", "/api/auth/set-password", "/api/auth/delete-user", "/api/auth/reset-password"]) {
      expect((await call(host, "POST", path, { newPassword: "something long enough!" }, { cookie })).status).toBe(403);
    }
    expect((await call(host, "GET", "/api/auth/get-session", undefined, { cookie })).status).toBe(200);
    expect((await call(host, "POST", "/api/auth/sign-out", {}, { cookie })).status).toBe(200);
  });
});

describe("refusing bad copies", () => {
  async function pair() {
    const pod = await machine();
    await setUpOwner(pod);
    await addPerson(pod, "sam@example.test");
    const host = await machine("replica");
    await copy(pod, host);
    const good = (await internal(pod, "GET", "/internal/snapshot")).data;
    return { pod, host, good };
  }
  const refuse = async (host: any, snapshot: unknown, reason: string) => {
    const before = await state(host);
    const result = await internal(host, "POST", "/internal/snapshot", snapshot);
    expect(result.status).toBe(422);
    expect(result.data.reason).toBe(reason);
    expect(await state(host)).toEqual(before); // nothing changed
    expect(await signIn(host, "owner@example.test")).toBeTruthy(); // and people can still get in
  };

  test("a copy with nobody in it is refused, so a new empty pod cannot wipe a host", async () => {
    const { host, good } = await pair();
    await refuse(host, { ...good, version: good.version + 5, users: [], accounts: [] }, "empty");
    expect((await internal(await machine(), "GET", "/internal/snapshot")).status).toBe(404); // an empty machine offers nothing
  });

  test("a copy with no owner is refused", async () => {
    const { host, good } = await pair();
    const demoted = good.users.map((u: any) => ({ ...u, role: "member" }));
    await refuse(host, { ...good, version: good.version + 1, users: demoted }, "no_owner");
  });

  test("a copy from a different history is refused, even if newer", async () => {
    const { host, good } = await pair();
    await refuse(host, { ...good, epoch: crypto.randomUUID(), version: good.version + 100 }, "different_lineage");
  });

  test("an older copy is refused, so a restored backup cannot roll a host back", async () => {
    const { pod, host } = await pair();
    const old = (await internal(pod, "GET", "/internal/snapshot")).data;
    await addPerson(pod, "kim@example.test");
    await copy(pod, host);
    await refuse(host, old, "not_newer");
    expect(await signIn(host, "kim@example.test")).toBeTruthy(); // kim was not rolled away
  });

  test("takeover lets the cluster's list replace a host's, whatever its history or version", async () => {
    const { host, good } = await pair();
    const other = { ...good, epoch: crypto.randomUUID(), version: 1 };
    const result = await internal(host, "POST", "/internal/snapshot?takeover=1", other);
    expect(result.status).toBe(200);
    expect(result.data.status).toBe("applied");
    expect((await state(host)).epoch).toBe(other.epoch);
  });

  test("takeover still refuses an empty or ownerless list, a bad layout, and a primary", async () => {
    const { host, good } = await pair();
    const takeover = (machine: any, snapshot: unknown) => internal(machine, "POST", "/internal/snapshot?takeover=1", snapshot);
    expect((await takeover(host, { ...good, users: [], accounts: [] })).data.reason).toBe("empty");
    expect((await takeover(host, { ...good, users: good.users.map((u: any) => ({ ...u, role: "member" })) })).data.reason).toBe("no_owner");
    expect((await takeover(host, { ...good, schema: 99 })).data.reason).toBe("schema_mismatch");
    const primary = await machine();
    await setUpOwner(primary);
    expect((await takeover(primary, good)).data.reason).toBe("primary_has_accounts");
    expect(await signIn(host, "owner@example.test")).toBeTruthy();
  });

  test("a different column layout is refused", async () => {
    const { host, good } = await pair();
    await refuse(host, { ...good, schema: 99, version: good.version + 1 }, "schema_mismatch");
    await refuse(host, { ...good, version: good.version + 1, users: good.users.map((u: any) => ({ ...u, extra: 1 })) }, "schema_mismatch");
    const { sessionsValidAfter, ...fewer } = good.users[0];
    await refuse(host, { ...good, version: good.version + 1, users: [fewer, ...good.users.slice(1)] }, "schema_mismatch");
  });

  test("malformed copies are refused with a reason", async () => {
    const { host, good } = await pair();
    const next = good.version + 1;
    await refuse(host, "not an object", "invalid");
    await refuse(host, { ...good, version: next, epoch: "" }, "invalid");
    await refuse(host, { ...good, version: -1 }, "invalid");
    await refuse(host, { ...good, version: next, accounts: [{ ...good.accounts[0], userId: "ghost" }] }, "invalid");
    await refuse(host, { ...good, version: next, users: [good.users[0], { ...good.users[1], email: good.users[0].email.toUpperCase() }] }, "invalid");
    await refuse(host, { ...good, version: next, users: good.users.map((u: any) => ({ ...u, role: "emperor" })) }, "invalid");
    await refuse(host, { ...good, version: next, users: [good.users[0], good.users[0]] }, "invalid");
    await refuse(host, { ...good, version: next, users: good.users.map((u: any) => ({ ...u, name: { nested: true } })) }, "invalid");
  });

  test("a primary that already has people takes no copies, so a host cannot overwrite the pod", async () => {
    const { pod, host } = await pair();
    await addPerson(host.db ? pod : pod, "kim@example.test");
    const forged = (await internal(host, "GET", "/internal/snapshot")).data;
    const before = await state(pod);
    const result = await internal(pod, "POST", "/internal/snapshot", { ...forged, version: before.version + 50 });
    expect(result.status).toBe(422);
    expect(result.data.reason).toBe("primary_has_accounts");
    expect(await state(pod)).toEqual(before);
  });

  test("a recovery password put in the file is the setup code, matched exactly, and never logged", async () => {
    const dir = tempDir();
    const file = join(dir, "setup-code");
    const password = "Correct-Horse-Battery-staple 42";
    await Bun.write(file, password + "\n");
    const m = await machine("primary", { setupCodeFile: file });
    expect(m.setup.ensure()).toBe(password);
    expect(m.setup.isGenerated()).toBe(false);
    const printed: string[] = [];
    m.announce((line) => printed.push(line));
    expect(printed.join("\n")).not.toContain(password);
    const body = { name: "N", email: "n@example.test", password: PASSWORD };
    // The case matters for a password: the same letters in upper case are wrong.
    expect((await call(m, "POST", "/api/setup", { ...body, code: password.toUpperCase() })).status).toBe(403);
    expect((await call(m, "POST", "/api/setup", { ...body, code: password })).status).toBe(201);
  });
});
