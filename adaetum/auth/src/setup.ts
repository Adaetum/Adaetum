import { chmodSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import { timingSafeEqual } from "node:crypto";

// The first-run setup code. A fresh machine would otherwise offer "create the first
// owner" to whoever reaches it first, so creating that owner needs a code that is shown
// only on the machine itself (its console and a file only root can read).

const LETTERS = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"; // no 0/O or 1/I to misread
const MAX_WRONG = 5;
const LOCK_MS = 60_000;

function matches(given: string, expected: Buffer): boolean {
  const candidate = Buffer.from(given);
  return candidate.length === expected.length && timingSafeEqual(candidate, expected);
}

export type CodeCheck = "ok" | "wrong" | "locked";

export class SetupCode {
  private code: string | null = null;
  private wrong = 0;
  private lockedUntil = 0;

  constructor(private readonly file: string | null, private readonly now: () => number = Date.now) {}

  // ensure makes (or reloads) the code. First boot may already have put the operator's
  // recovery password in the file, so any saved value of 8 or more characters is used as is.
  // Reloading keeps the same code across restarts, so what an operator saw in the log
  // still works.
  ensure(): string {
    if (this.code) return this.code;
    if (this.file && existsSync(this.file)) {
      const saved = readFileSync(this.file, "utf8").trim();
      if (saved.length >= 8) return (this.code = saved);
    }
    const random = crypto.getRandomValues(new Uint8Array(12));
    const raw = Array.from(random, (b) => LETTERS[b % LETTERS.length]).join("");
    this.code = `${raw.slice(0, 4)}-${raw.slice(4, 8)}-${raw.slice(8, 12)}`;
    if (this.file) {
      mkdirSync(dirname(this.file), { recursive: true, mode: 0o700 });
      writeFileSync(this.file, this.code + "\n", { mode: 0o600 });
      chmodSync(this.file, 0o600);
    }
    return this.code;
  }

  // check compares in constant time, and after a few wrong tries refuses everything for
  // a minute: a code that can be guessed at network speed is not a code.
  check(presented: string): CodeCheck {
    if (this.now() < this.lockedUntil) return "locked";
    const expected = Buffer.from(this.ensure());
    // A generated code is upper case and typed by hand, so it is compared case-insensitively.
    // A seeded code (the operator's recovery password, written at first boot) is compared exactly
    // first, since its case matters and must not be loosened.
    if (matches(presented.trim(), expected) || matches(presented.trim().toUpperCase(), expected)) {
      this.wrong = 0;
      return "ok";
    }
    if (++this.wrong >= MAX_WRONG) {
      this.wrong = 0;
      this.lockedUntil = this.now() + LOCK_MS;
      return "locked";
    }
    return "wrong";
  }

  // isGenerated says whether the code is one this service made, as opposed to a password
  // seeded into the file. Only a generated code may be shown in a log or on a console.
  isGenerated(): boolean {
    return /^[A-Z2-9]{4}-[A-Z2-9]{4}-[A-Z2-9]{4}$/.test(this.ensure());
  }

  // consume removes the code once it has been used.
  consume(): void {
    this.code = null;
    if (this.file) rmSync(this.file, { force: true });
  }
}
