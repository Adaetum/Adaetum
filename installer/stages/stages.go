// Package stages is the ordered list of setup stages for the ISO install path: the
// one that builds a Rocky installer ISO before any cluster exists. The on-host
// install path is a later phase and will get its own list.
package stages

import (
	"os"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/stages/artifacts"
	"github.com/Adaetum/Adaetum/installer/stages/finalize"
	"github.com/Adaetum/Adaetum/installer/stages/media"
	"github.com/Adaetum/Adaetum/installer/stages/profile"
	"github.com/Adaetum/Adaetum/installer/stages/providers"
	"github.com/Adaetum/Adaetum/installer/stages/render"
	"github.com/Adaetum/Adaetum/installer/stages/repository"
	"github.com/Adaetum/Adaetum/installer/stages/validate"
	"github.com/Adaetum/Adaetum/installer/sys"
)

// ISO is the install path that builds a Rocky installer ISO before any cluster
// exists. The on-host install path is a later phase and gets its own list.
func ISO(root string, run sys.Runner) []engine.Step {
	home, _ := os.UserHomeDir()
	return []engine.Step{
		repository.New(run, root),
		providers.New(run, root),
		profile.New(run, root),
		media.New(run, root, home),
		validate.New(run, root),
		render.New(run, root),
		artifacts.New(run, root),
		finalize.New(root, home),
	}
}
