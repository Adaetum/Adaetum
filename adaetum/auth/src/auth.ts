import { Database } from "bun:sqlite";
import { betterAuth } from "better-auth";
import { getMigrations } from "better-auth/db/migration";
import { AccountStore, ROLES, type Role } from "./accounts";

// The Better Auth instance and the few things done to people directly. Better Auth owns
// sign-in, sessions, password hashing and the cookie; this file only configures it.
// Nothing here is authentication code of ours.

export type Auth = Awaited<ReturnType<typeof createAuth>>["auth"];

export interface AuthOptions {
  db: Database;
  secret: string;
  // Used for cookie flags and generated links. Browsers reach every machine by a
  // different name, so origins are checked per request instead (see trustedOrigins).
  baseURL: string;
}

export async function createAuth({ db, secret, baseURL }: AuthOptions) {
  const options = {
    database: db,
    secret,
    baseURL,
    emailAndPassword: {
      enabled: true,
      // People are added by an owner (or the first-run setup code), never by open sign-up.
      disableSignUp: true,
      minPasswordLength: 12,
      maxPasswordLength: 128,
    },
    user: {
      additionalFields: {
        // owner, admin or member. Not settable by the person themselves.
        role: { type: "string", defaultValue: "member", input: false },
        // Sessions created before this moment (milliseconds) are signed out, on every
        // machine, when the next copy of the accounts arrives.
        sessionsValidAfter: { type: "number", defaultValue: 0, input: false },
      },
    },
    // The page is served by whichever machine the person opened, so the only origin to
    // trust is the one that matches the Host the request arrived on (same-origin).
    trustedOrigins: async (request?: Request) => {
      const origin = request?.headers.get("origin");
      const host = request?.headers.get("host");
      try {
        return origin && host && new URL(origin).host === host ? [origin] : [];
      } catch {
        return [];
      }
    },
  } as const;
  // Create or update the tables BEFORE building the instance: Better Auth checks the
  // tables when it is built and logs a scary (and false) "schema mismatch" error on a
  // fresh database otherwise.
  await (await getMigrations(options as never)).runMigrations();
  const auth = betterAuth(options as never);
  const store = new AccountStore(db);
  store.init();
  return { auth, store };
}

export class PeopleError extends Error {
  constructor(readonly code: "invalid" | "duplicate" | "not_found" | "last_owner", message: string) {
    super(message);
  }
}

export interface NewPerson {
  name: string;
  email: string;
  password: string;
  role: Role;
}

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

// createPerson adds someone with a password. The password is hashed by Better Auth.
export async function createPerson(auth: Auth, store: AccountStore, person: NewPerson) {
  const name = person.name.trim();
  const email = person.email.trim().toLowerCase();
  if (name === "" || name.length > 100) throw new PeopleError("invalid", "a name of 1 to 100 characters is needed");
  if (!EMAIL.test(email) || email.length > 254) throw new PeopleError("invalid", "that does not look like an email address");
  if (person.password.length < 12 || person.password.length > 128) throw new PeopleError("invalid", "the password needs 12 to 128 characters");
  if (!ROLES.includes(person.role)) throw new PeopleError("invalid", "that role does not exist");

  const ctx = await auth.$context;
  if (await ctx.internalAdapter.findUserByEmail(email)) throw new PeopleError("duplicate", "someone with that email already exists");
  const hash = await ctx.password.hash(person.password);
  const user = await ctx.internalAdapter.createUser({ name, email, emailVerified: true, role: person.role } as never, undefined as never);
  await ctx.internalAdapter.linkAccount({ userId: user.id, providerId: "credential", accountId: user.id, password: hash } as never);
  store.ensureEpoch();
  return { id: user.id as string, name, email, role: person.role };
}

export function listPeople(db: Database) {
  return db.query('SELECT id, name, email, role, createdAt FROM "user" ORDER BY createdAt, id').all() as {
    id: string; name: string; email: string; role: Role; createdAt: string;
  }[];
}

function requireUser(db: Database, id: string): { role: Role } {
  const row = db.query('SELECT role FROM "user" WHERE id = ?').get(id) as { role: Role } | null;
  if (!row) throw new PeopleError("not_found", "there is nobody with that id");
  return row;
}

// There must always be an owner, or nobody could manage accounts (and a copy without
// one is refused by every machine).
export function deletePerson(db: Database, store: AccountStore, id: string): void {
  const person = requireUser(db, id);
  if (person.role === "owner" && store.countOwners() <= 1) throw new PeopleError("last_owner", "the last owner cannot be removed");
  db.transaction(() => {
    db.run("DELETE FROM session WHERE userId = ?", [id]);
    db.run("DELETE FROM account WHERE userId = ?", [id]);
    db.run('DELETE FROM "user" WHERE id = ?', [id]);
  })();
}

export function setRole(db: Database, store: AccountStore, id: string, role: Role): void {
  if (!ROLES.includes(role)) throw new PeopleError("invalid", "that role does not exist");
  const person = requireUser(db, id);
  if (person.role === "owner" && role !== "owner" && store.countOwners() <= 1) throw new PeopleError("last_owner", "the last owner cannot be demoted");
  db.run('UPDATE "user" SET role = ?, updatedAt = ? WHERE id = ?', [role, new Date().toISOString(), id]);
}

// revokeSessions signs a person out here now, and on every other machine when the next
// copy of the accounts reaches it.
export function revokeSessions(db: Database, id: string): void {
  requireUser(db, id);
  db.transaction(() => {
    db.run('UPDATE "user" SET sessionsValidAfter = ?, updatedAt = ? WHERE id = ?', [Date.now(), new Date().toISOString(), id]);
    db.run("DELETE FROM session WHERE userId = ?", [id]);
  })();
}
