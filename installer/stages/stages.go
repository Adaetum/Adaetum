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
func ISO(root string, run sys.Runner) []engine.Step { return ISOFromSource(root, root, run) }

// ISOFromSource uses an isolated checkout for changes while finding existing
// installer media and retaining the resulting ISO in the launch checkout.
func ISOFromSource(root, source string, run sys.Runner) []engine.Step {
	home, _ := os.UserHomeDir()
	mediaStep := media.New(run, root, home)
	mediaStep.Lib.ExtraDirs = []string{source}
	renderStep := render.New(run, root)
	renderStep.LogRoot = source
	artifactsStep := artifacts.New(run, root)
	artifactsStep.LogRoot = source
	finalStep := finalize.New(root, home)
	finalStep.OutputRoot = source
	return []engine.Step{
		repository.New(run, root),
		providers.New(run, root),
		profile.New(run, root),
		mediaStep,
		validate.New(run, root),
		renderStep,
		artifactsStep,
		finalStep,
	}
}
