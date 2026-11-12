// Package runs is the agent's engine for multi-step work that has to survive
// failure: first boot of a node, and the day-2 jobs that repair or change it later
// (unseal OpenBao, re-sync Git, join a node, run the doctor).
//
// It replaces a chain of shell scripts that left "done" markers behind. Markers
// say what a script once did, not what is true now, so a retry could skip work that
// had since broken or repeat work that must not repeat. This engine instead asks
// the live system every time.
//
// # The model
//
// A Procedure is a named list of Steps. A Run is one execution of a Procedure,
// with an ID, inputs, and a journal of everything that happened. Every Step has:
//
//   - Check: is this already true (and healthy)? It reads the real system.
//   - Apply: make it true. Only called when Check says it is not. A step that only
//     waits for the system to get there by itself has no Apply.
//   - Verify: after Apply, wait for it to become true. Slow is fine; broken is not.
//   - Needs and Locks: what must be true first, and what the step changes.
//
// Because Check always runs first, running a Procedure again is safe: satisfied
// steps do nothing. Retrying a failure, resuming after an agent restart, and
// repairing later on demand are therefore all the same operation.
//
// # Idempotency kinds
//
// A step declares how safe it is to repeat (see Kind). Most are Convergent: apply
// can run any time. OnceOnly steps (initialising OpenBao, generating a key) are
// applied only when Check is definitely "no" and never when it cannot tell.
// Destructive steps are never applied without an explicit approval on the run.
//
// # Failure, waiting, and retry
//
// Waiting is most of what first boot does, so a wait reports why it is waiting.
// A step's Verify returns Ready, Slow (with a reason and a progress marker) or
// Broken (with a reason). The engine keeps waiting while the marker keeps changing,
// fails as stalled when it stops changing, and fails at once on Broken. Errors are
// classified (Transient, Broken, NeedsHuman): only Transient errors are retried
// automatically, with backoff.
//
// # The journal
//
// Everything is an append-only Record, recorded BEFORE the thing it describes
// runs, like the agent's launch record: if a record cannot be written, the step is
// not run. A run's current state is a fold over its records, so state can never
// disagree with history, and the same records feed the UI, the logs, and later
// analytics. Records carry reason CODES from a fixed list, never free-form text
// that might hold a secret, plus a short human Detail for the person watching.
//
// # What this package does not do
//
// It never talks to the outside world. Steps are given what they need (a
// Kubernetes client, an OpenBao client) by the code that builds a Procedure, and
// those adapters are the only place real I/O happens. That is what lets a whole
// first boot be tested against fakes. It also has no scripting language and no
// plugin system: Procedures are plain Go.
//
// Run inputs are journaled so a run can resume. Never put a secret in them; a step
// that needs one reads it from the secret authority itself.
package runs
