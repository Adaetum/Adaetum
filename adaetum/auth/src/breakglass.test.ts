import { describe, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { reconcileBreakGlass } from "./breakglass";
import { call, internal, machine, PASSWORD, setUpOwner, signIn, tempDir } from "./helpers";

const GLASS = "break-glass@cluster.test";
const SECRET = "openbao-generated-password-1";

function files(email: string, password: string): string {
  const dir = tempDir();
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, "email"), email + "\n");
  writeFileSync(join(dir, "password"), password + "\n");
  return dir;
}

describe("the break-glass owner", () => {
  test("is created from the files, signs in, and is an owner", async () => {
    const m = await machine();
    expect(await reconcileBreakGlass(m, files(GLASS, SECRET))).toBe("created");
    expect(await signIn(m, GLASS, SECRET)).not.toBeNull();
    const people = (await internal(m, "GET", "/internal/people")).data.people ?? (await internal(m, "GET", "/internal/people")).data;
    expect(JSON.stringify(people)).toContain(GLASS);
    expect(JSON.stringify(people)).toContain('"owner"');
  });

  test("does nothing without the files", async () => {
    const m = await machine();
    expect(await reconcileBreakGlass(m, tempDir())).toBe("absent");
    expect(m.store.countUsers()).toBe(0);
  });

  test("a changed password in OpenBao replaces the old one and signs everyone out", async () => {
    const m = await machine();
    await reconcileBreakGlass(m, files(GLASS, SECRET));
    const cookie = await signIn(m, GLASS, SECRET);
    expect(cookie).not.toBeNull();
    expect(await reconcileBreakGlass(m, files(GLASS, SECRET))).toBe("unchanged");
    expect(await reconcileBreakGlass(m, files(GLASS, "a-rotated-password-2"))).toBe("updated");
    expect(await signIn(m, GLASS, SECRET)).toBeNull();
    expect(await signIn(m, GLASS, "a-rotated-password-2")).not.toBeNull();
  });

  test("cannot be removed or demoted from the app", async () => {
    const m = await machine();
    await reconcileBreakGlass(m, files(GLASS, SECRET));
    const people = (await internal(m, "GET", "/internal/people")).data;
    const list = Array.isArray(people) ? people : people.people;
    const id = list.find((p: any) => p.email === GLASS).id;
    expect((await internal(m, "DELETE", `/internal/people/${id}`)).status).toBe(409);
    expect((await internal(m, "POST", `/internal/people/${id}/role`, { role: "member" })).status).toBe(409);
  });

  test("does not count as a person: first-run setup still creates the first owner beside it", async () => {
    const m = await machine();
    await reconcileBreakGlass(m, files(GLASS, SECRET));
    expect((await call(m, "GET", "/api/auth-state")).data.needsSetup).toBe(true);
    await setUpOwner(m, "owner@example.test", PASSWORD);
    expect((await call(m, "GET", "/api/auth-state")).data.needsSetup).toBe(false);
    expect(m.store.countOwners()).toBe(2);
    expect(await signIn(m, "owner@example.test")).not.toBeNull();
    expect(await signIn(m, GLASS, SECRET)).not.toBeNull();
  });
});
