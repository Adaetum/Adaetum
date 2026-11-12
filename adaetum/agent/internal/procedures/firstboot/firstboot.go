// Package firstboot is the "first-boot" procedure: turning a freshly installed machine
// into a one-node cluster. It is built from chunks, each its own procedure that runs and
// is tested alone, and this package only decides their ORDER:
//
//	host-readiness   a route out, a synchronized clock, enough disk
//	intake           the runtime payload is usable (old Phase 10)
//	local-secrets    the temporary secrets exist (old Phase 20)
//
// The order is a guide that can change as the rework goes (docs/firstboot-rework.md).
// More chunks are added after these, one at a time, as each old phase is rebuilt: the
// cluster (30), OpenBao (40), the control pair (50), the handoff (60), the GitOps proof
// (70), late reconciliation (90) and the burn (99). Until a phase is rebuilt here, the
// shell scripts still run it. This procedure covers only what exists so far, and says so
// in its description rather than pretending to be the whole of first boot.
//
// Every chunk checks before it acts, so running first-boot again on a half-built
// machine does only what is missing, which is what makes retry and resume safe.
package firstboot

import (
	"github.com/Adaetum/Adaetum/agent/internal/procedures/hostready"
	"github.com/Adaetum/Adaetum/agent/internal/procedures/intake"
	"github.com/Adaetum/Adaetum/agent/internal/procedures/localsecrets"
	"github.com/Adaetum/Adaetum/agent/internal/runs"
)

// Name is the procedure's name.
const Name = "first-boot"

// Files is what the file-based chunks need.
type Files interface {
	intake.Reader
	localsecrets.Files
}

// Chunks returns the chunks in the order first boot runs them. Each is also usable on its
// own, which is how it is tested.
func Chunks(host hostready.Host, files Files, random localsecrets.Random) []*runs.Procedure {
	return []*runs.Procedure{
		hostready.New(host),
		intake.New(files),
		localsecrets.New(files, random),
	}
}

// New returns first-boot, the chunks joined in order.
func New(host hostready.Host, files Files, random localsecrets.Random) (*runs.Procedure, error) {
	return runs.Compose(
		Name,
		"The first-boot chunks rebuilt so far, in order: host readiness, payload intake, local secrets. The later phases still run from the shell scripts.",
		Chunks(host, files, random)...,
	)
}
