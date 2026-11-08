import { describe, expect, test } from "bun:test";
import { DEMO_EMAIL, DEMO_PASSWORD, seedDemo } from "./demo";
import { addPerson, machine, setUpOwner, signIn, state } from "./helpers";

describe("the development demo account", () => {
  test("is off unless asked for", async () => {
    const m = await machine();
    expect(await seedDemo(m, {})).toBe("skipped");
    expect((await state(m)).users).toBe(0);
  });

  test("makes one owner who can sign in, and only once", async () => {
    const m = await machine();
    expect(await seedDemo(m, { AUTH_DEMO: "1" })).toBe("created");
    expect(await signIn(m, DEMO_EMAIL, DEMO_PASSWORD)).toBeTruthy();
    expect((await state(m)).owners).toBe(1);
    expect(await seedDemo(m, { AUTH_DEMO: "1" })).toBe("skipped");
    expect((await state(m)).users).toBe(1);
  });

  test("never adds itself to a machine that already has people", async () => {
    const m = await machine();
    await setUpOwner(m);
    await addPerson(m, "sam@example.test");
    expect(await seedDemo(m, { AUTH_DEMO: "1" })).toBe("skipped");
    expect(await signIn(m, DEMO_EMAIL, DEMO_PASSWORD)).toBeNull();
  });

  test("is refused in production", async () => {
    const m = await machine();
    await expect(seedDemo(m, { AUTH_DEMO: "1", NODE_ENV: "production" })).rejects.toThrow(/development only/);
    expect((await state(m)).users).toBe(0);
  });
});
