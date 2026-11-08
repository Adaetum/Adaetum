import type { Database } from "bun:sqlite";

// The rules for moving the account list between machines. This file is the whole of
// the replication contract (docs/recovery-login.md); everything else in the service
// is Better Auth. It holds no network code: callers hand it a database and a snapshot.
//
// THE MODEL. Accounts have ONE owner, the cluster's primary. Hosts (replicas) receive
// copies and never originate changes, so there is nothing to merge. Sessions are not
// accounts: each machine keeps its own and they are never copied.
//
// WHY EVERY RULE BELOW EXISTS (each is a way a naive sync locks people out or lets the
// wrong data win; see "Ways this could break" in the doc):
//   - a snapshot with no people, or no owner, is refused: an empty, new pod must never
//     wipe a host's only owner;
//   - a snapshot from a different history (epoch) is refused once a machine has people:
//     a rebuilt pod starts a new history, and hosts must not obey it;
//   - an older or equal version is refused: a pod restored from an old backup must not
//     roll hosts back (resurrecting removed people, deleting new ones);
//   - a different column layout is refused (schema): version skew between machines must
//     fail loudly instead of copying the wrong columns;
//   - a primary that already has people takes no snapshots: otherwise any enrolled host
//     could overwrite every account.

// Bump when the column lists below change: machines on different layouts refuse each other.
export const SCHEMA = 1;

export const USER_COLUMNS = ["id", "name", "email", "emailVerified", "image", "createdAt", "updatedAt", "role", "sessionsValidAfter"] as const;
export const ACCOUNT_COLUMNS = [
  "id", "accountId", "providerId", "userId", "accessToken", "refreshToken", "idToken",
  "accessTokenExpiresAt", "refreshTokenExpiresAt", "scope", "password", "createdAt", "updatedAt",
] as const;

export const ROLES = ["owner", "admin", "member"] as const;
export type Role = (typeof ROLES)[number];
export type Mode = "primary" | "replica";

type Cell = string | number | null;
export type Row = Record<string, Cell>;

export interface Snapshot {
  schema: number;
  epoch: string;
  version: number;
  users: Row[];
  accounts: Row[];
}

export interface State {
  schema: number;
  // Empty until the first person exists or the first snapshot is applied.
  epoch: string;
  version: number;
  users: number;
  owners: number;
}

export type RejectReason =
  | "schema_mismatch"
  | "invalid"
  | "empty"
  | "no_owner"
  | "different_lineage"
  | "not_newer"
  | "primary_has_accounts";

export type ApplyResult =
  | { status: "applied"; version: number }
  | { status: "current"; version: number }
  | { status: "rejected"; reason: RejectReason; detail: string };

const MAX_PEOPLE = 5000;
const reject = (reason: RejectReason, detail: string): ApplyResult => ({ status: "rejected", reason, detail });

export class AccountStore {
  constructor(private readonly db: Database) {}

  // init adds the bookkeeping (a version counter, an epoch) and the triggers that bump the
  // version on ANY change to people or passwords, so no code path can change an account
  // and forget to bump it. Call after Better Auth has created its tables.
  init(): void {
    this.db.run("PRAGMA foreign_keys = ON");
    this.db.run("CREATE TABLE IF NOT EXISTS adaetum_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)");
    this.db.run("INSERT OR IGNORE INTO adaetum_meta (key, value) VALUES ('version', '0')");
    for (const table of ["user", "account"]) {
      for (const event of ["INSERT", "UPDATE", "DELETE"]) {
        this.db.run(
          `CREATE TRIGGER IF NOT EXISTS adaetum_bump_${table}_${event.toLowerCase()} AFTER ${event} ON "${table}" BEGIN
             UPDATE adaetum_meta SET value = CAST(value AS INTEGER) + 1 WHERE key = 'version';
           END`,
        );
      }
    }
  }

  private meta(key: string): string | null {
    const row = this.db.query("SELECT value FROM adaetum_meta WHERE key = ?").get(key) as { value: string } | null;
    return row?.value ?? null;
  }

  private setMeta(key: string, value: string): void {
    this.db.run("INSERT INTO adaetum_meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", [key, value]);
  }

  // ensureEpoch names this history the first time it is needed. Two machines that were
  // set up separately have different epochs and so can never overwrite each other.
  ensureEpoch(): string {
    let epoch = this.meta("epoch");
    if (!epoch) {
      epoch = crypto.randomUUID();
      this.setMeta("epoch", epoch);
    }
    return epoch;
  }

  countUsers(): number {
    return (this.db.query('SELECT count(*) AS c FROM "user"').get() as { c: number }).c;
  }

  countOwners(): number {
    return (this.db.query("SELECT count(*) AS c FROM \"user\" WHERE role = 'owner'").get() as { c: number }).c;
  }

  state(): State {
    return {
      schema: SCHEMA,
      epoch: this.meta("epoch") ?? "",
      version: Number(this.meta("version") ?? "0"),
      users: this.countUsers(),
      owners: this.countOwners(),
    };
  }

  // snapshot is what a replica copies: people and their credentials, nothing else.
  // Null when there is nobody to copy (an empty list must never be offered).
  snapshot(): Snapshot | null {
    if (this.countUsers() === 0) return null;
    const epoch = this.ensureEpoch();
    const pick = (table: string, columns: readonly string[]) =>
      this.db.query(`SELECT ${columns.map((c) => `"${c}"`).join(", ")} FROM "${table}" ORDER BY id`).all() as Row[];
    return {
      schema: SCHEMA,
      epoch,
      version: Number(this.meta("version") ?? "0"),
      users: pick("user", USER_COLUMNS),
      accounts: pick("account", ACCOUNT_COLUMNS),
    };
  }

  // apply decides whether to take a snapshot and, if so, replaces the account list in one
  // transaction. `mode` is this machine's role: a primary that already has people refuses
  // every snapshot (only a machine with nobody yet may be seeded).
  //
  // `takeover` is the cluster-wins rule: a replica that was rebuilt, rejoined, or is
  // looking at a restored cluster drops its own list and adopts the cluster's, whatever
  // its history or version. It never relaxes what makes a copy safe to install (layout,
  // shape, not empty, has an owner), and a primary never takes one, so only a machine
  // that has already joined a cluster can be overwritten.
  apply(incoming: unknown, mode: Mode, takeover = false): ApplyResult {
    const parsed = parse(incoming);
    if ("status" in parsed) return parsed;
    const local = this.state();

    if (local.users > 0) {
      if (mode === "primary") return reject("primary_has_accounts", "this machine owns its accounts and takes no copies");
      if (takeover) {
        this.db.transaction(() => this.replace(parsed))();
        return { status: "applied", version: parsed.version };
      }
      if (parsed.epoch !== local.epoch) return reject("different_lineage", "the copy comes from a different account history than this machine's");
      if (parsed.version === local.version) return { status: "current", version: local.version };
      if (parsed.version < local.version) return reject("not_newer", `the copy (version ${parsed.version}) is older than this machine's (${local.version})`);
    }

    this.db.transaction(() => this.replace(parsed))();
    return { status: "applied", version: parsed.version };
  }

  private replace(snapshot: Snapshot): void {
    const db = this.db;
    // What each person's password looked like before, to spot a changed password.
    const before = new Map<string, string | null>();
    for (const row of db.query("SELECT userId, password FROM account WHERE providerId = 'credential'").all() as { userId: string; password: string | null }[]) {
      before.set(row.userId, row.password);
    }

    const userIds = new Set(snapshot.users.map((u) => u.id as string));
    const accountIds = new Set(snapshot.accounts.map((a) => a.id as string));
    // Children first: sessions, then accounts, then people that are gone.
    for (const row of db.query('SELECT id FROM "user"').all() as { id: string }[]) {
      if (!userIds.has(row.id)) {
        db.run("DELETE FROM session WHERE userId = ?", [row.id]);
        db.run("DELETE FROM account WHERE userId = ?", [row.id]);
        db.run('DELETE FROM "user" WHERE id = ?', [row.id]);
      }
    }
    for (const row of db.query("SELECT id FROM account").all() as { id: string }[]) {
      if (!accountIds.has(row.id)) db.run("DELETE FROM account WHERE id = ?", [row.id]);
    }

    // Upsert, never REPLACE: REPLACE deletes the row first, and that would cascade away
    // the sessions of everyone who is still allowed in.
    const upsert = (table: string, columns: readonly string[], rows: Row[]) => {
      const names = columns.map((c) => `"${c}"`).join(", ");
      const marks = columns.map(() => "?").join(", ");
      const updates = columns.filter((c) => c !== "id").map((c) => `"${c}" = excluded."${c}"`).join(", ");
      const statement = db.prepare(`INSERT INTO "${table}" (${names}) VALUES (${marks}) ON CONFLICT(id) DO UPDATE SET ${updates}`);
      for (const row of rows) statement.run(...columns.map((c) => row[c] ?? null));
    };
    upsert("user", USER_COLUMNS, snapshot.users);
    upsert("account", ACCOUNT_COLUMNS, snapshot.accounts);

    // Revoke what the cluster revoked: a changed password signs the person out
    // everywhere, and "sessions valid after" signs out anything older than that moment.
    for (const user of snapshot.users) {
      const id = user.id as string;
      const after = Number(user.sessionsValidAfter ?? 0);
      const password = snapshot.accounts.find((a) => a.userId === id && a.providerId === "credential")?.password ?? null;
      const changed = before.has(id) && before.get(id) !== password;
      for (const session of db.query("SELECT id, createdAt FROM session WHERE userId = ?").all(id) as { id: string; createdAt: string | number }[]) {
        const created = new Date(session.createdAt).getTime();
        if (changed || (after > 0 && !(created >= after))) db.run("DELETE FROM session WHERE id = ?", [session.id]);
      }
    }

    this.setMeta("epoch", snapshot.epoch);
    this.setMeta("version", String(snapshot.version));
  }
}

// parse checks a snapshot before anything is touched. It refuses what could lock people
// out (empty, no owner) and what is malformed, with a reason a person can act on.
function parse(value: unknown): Snapshot | ApplyResult {
  if (typeof value !== "object" || value === null) return reject("invalid", "the copy is not an object");
  const s = value as Record<string, unknown>;
  if (s.schema !== SCHEMA) return reject("schema_mismatch", `the copy uses account layout ${String(s.schema)}, this machine uses ${SCHEMA}`);
  if (typeof s.epoch !== "string" || s.epoch === "" || !Number.isSafeInteger(s.version) || (s.version as number) < 0) {
    return reject("invalid", "the copy has no usable history id or version");
  }
  if (!Array.isArray(s.users) || !Array.isArray(s.accounts)) return reject("invalid", "the copy has no people or credentials list");
  if (s.users.length === 0) return reject("empty", "the copy has nobody in it");
  if (s.users.length > MAX_PEOPLE || s.accounts.length > MAX_PEOPLE * 4) return reject("invalid", "the copy is larger than any real account list");

  const checkRows = (rows: unknown[], columns: readonly string[], what: string): ApplyResult | null => {
    for (const row of rows) {
      if (typeof row !== "object" || row === null) return reject("invalid", `a ${what} row is not an object`);
      const keys = Object.keys(row);
      if (keys.length !== columns.length || !columns.every((c) => keys.includes(c))) {
        return reject("schema_mismatch", `a ${what} row does not have exactly this machine's columns`);
      }
      for (const cell of Object.values(row as Row)) {
        if (cell !== null && typeof cell !== "string" && typeof cell !== "number") return reject("invalid", `a ${what} row holds something that is not text or a number`);
      }
    }
    return null;
  };
  const bad = checkRows(s.users, USER_COLUMNS, "person") ?? checkRows(s.accounts, ACCOUNT_COLUMNS, "credential");
  if (bad) return bad;

  const users = s.users as Row[];
  const accounts = s.accounts as Row[];
  const ids = new Set<string>();
  const emails = new Set<string>();
  for (const user of users) {
    const id = user.id, email = user.email;
    if (typeof id !== "string" || id === "" || typeof email !== "string" || email === "") return reject("invalid", "a person has no id or email");
    if (!ROLES.includes(user.role as Role)) return reject("invalid", `a person has the unknown role ${String(user.role)}`);
    if (ids.has(id)) return reject("invalid", "two people share an id");
    if (emails.has(email.toLowerCase())) return reject("invalid", "two people share an email");
    ids.add(id);
    emails.add(email.toLowerCase());
  }
  if (!users.some((u) => u.role === "owner")) return reject("no_owner", "the copy has no owner, so nobody could manage accounts");
  const accountIds = new Set<string>();
  for (const account of accounts) {
    if (typeof account.id !== "string" || account.id === "" || accountIds.has(account.id)) return reject("invalid", "a credential has no id or repeats one");
    if (typeof account.userId !== "string" || !ids.has(account.userId)) return reject("invalid", "a credential belongs to nobody in the copy");
    accountIds.add(account.id);
  }
  return { schema: SCHEMA, epoch: s.epoch, version: s.version as number, users, accounts };
}
