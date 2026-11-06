// Run by service.test.ts in a child process with NODE_ENV=production: Better Auth turns
// its origin check off under NODE_ENV=test, so the real behaviour can only be seen outside it.
import { PASSWORD, call, machine, setUpOwner } from "./helpers";

const m = await machine();
await setUpOwner(m);
const body = { email: "owner@example.test", password: PASSWORD };
const cross = await call(m, "POST", "/api/auth/sign-in/email", body, { origin: "http://evil.example" });
const none = await call(m, "POST", "/api/auth/sign-in/email", body, { origin: null });
const same = await call(m, "POST", "/api/auth/sign-in/email", body);
console.log(JSON.stringify({ cross: cross.status, same: same.status, none: none.status }));
