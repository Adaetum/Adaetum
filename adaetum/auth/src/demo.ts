import type { AccountStore } from "./accounts";
import { createPerson, type Auth } from "./auth";

// A placeholder owner account for DEVELOPMENT, so the sign-in flow can be tried without the
// first-run setup. It is opt-in (AUTH_DEMO=1), never runs under NODE_ENV=production, and only
// when nobody has an account yet. The values are public (they are in the README); they
// protect nothing, which is why a production start refuses them.

export const DEMO_EMAIL = "demo@adaetum.test";
export const DEMO_PASSWORD = "demo-password-123";

export async function seedDemo(service: { auth: Auth; store: AccountStore }, env: Record<string, string | undefined>): Promise<"created" | "skipped"> {
  if (env.AUTH_DEMO !== "1") return "skipped";
  if (env.NODE_ENV === "production") throw new Error("AUTH_DEMO=1 is for development only and is refused under NODE_ENV=production");
  if (service.store.countUsers() > 0) return "skipped";
  await createPerson(service.auth, service.store, { name: "Demo Owner", email: DEMO_EMAIL, password: DEMO_PASSWORD, role: "owner" });
  return "created";
}
