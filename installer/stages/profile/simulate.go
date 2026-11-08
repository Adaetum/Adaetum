package profile

import (
	"context"
	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/platform"
)

// Simulate presents the profile choices and publication approval without
// writing platform.yaml, rendering files, running hooks, or publishing commits.
func (s *Step) Simulate(ctx context.Context, env *engine.Env, ui engine.UI) error {
	say := func(message string) { ui.Report(engine.Event{StepID: stepID, Message: message}) }
	domain, err := choose(ctx, env, ui, domainKey, "domain", "Public cluster domain (a domain you manage in Cloudflare)", platform.Profile{})
	if err != nil {
		return err
	}
	overlay, err := choose(ctx, env, ui, overlayKey, "overlay-domain", "Tailscale tailnet domain (for example tail1234.ts.net)", platform.Profile{})
	if err != nil {
		return err
	}
	say("Profile choices accepted for " + domain + " and " + overlay + ".")
	say("Adaetum defaults applied to the profile.")
	say("Derived files rendered; commit checks passed.")
	say("Profile commit published to the private recovery repository.")
	return nil
}
