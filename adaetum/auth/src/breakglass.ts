import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import type { AccountStore } from "./accounts";
import { createPerson, revokeSessions, type Auth } from "./auth";
import type { Database } from "bun:sqlite";

// The break-glass owner: one account whose email and password are OpenBao's, delivered as
// two files (`email` and `password`) in a directory the platform mounts.
//
// It exists for the day everything else is broken, so it is always active:
//   - it is created (as an owner) the first time the files appear, on any fresh machine;
//   - its password always follows the file, so rotating it in OpenBao is how it changes, and
//     a person changing it themselves is put back at the next check;
//   - it cannot be removed or demoted from the app (see AccountStore.breakGlassEmail);
//   - it is not a "person" for first-run setup: the normal setup code still creates the
//     first real owner next to it.
// Nothing is created when the files are missing, so a machine without OpenBao simply has no
// break-glass account and behaves exactly as before.

export type Reconciled = "absent" | "created" | "updated" | "unchanged";

const NAME = "Break-glass owner";

function readFile(path: string): string {
  return existsSync(path) ? readFileSync(path, "utf8").trim() : "";
}

export async function reconcileBreakGlass(
  service: { auth: Auth; store: AccountStore; db: Database },
  directory: string,
): Promise<Reconciled> {
  const email = readFile(join(directory, "email")).toLowerCase();
  const password = readFile(join(directory, "password"));
  if (!email || !password) return "absent";

  const { auth, store, db } = service;
  // Mark the account protected before anything else, so even a failed attempt below
  // cannot leave it removable.
  store.breakGlassEmail = email;

  const ctx = await auth.$context;
  const found = await ctx.internalAdapter.findUserByEmail(email);
  if (!found) {
    await createPerson(auth, store, { name: NAME, email, password, role: "owner" });
    return "created";
  }

  let changed = false;
  const userId = found.user.id as string;
  if ((found.user as { role?: string }).role !== "owner") {
    db.run('UPDATE "user" SET role = ?, updatedAt = ? WHERE id = ?', ["owner", new Date().toISOString(), userId]);
    changed = true;
  }
  const credential = (await ctx.internalAdapter.findAccounts(userId)).find((account: { providerId: string }) => account.providerId === "credential");
  if (!credential?.password || !(await ctx.password.verify({ hash: credential.password, password }))) {
    await ctx.internalAdapter.updatePassword(userId, await ctx.password.hash(password));
    // A new password signs every old session out, here and on every other machine.
    revokeSessions(db, userId);
    changed = true;
  }
  if (changed) store.ensureEpoch();
  return changed ? "updated" : "unchanged";
}
