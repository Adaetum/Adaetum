package profile

import (
	"context"
	"strings"

	"github.com/Adaetum/Adaetum/installer/engine"
)

// Simulate presents the profile choices and publication approval without
// writing platform.yaml, rendering files, running hooks, or publishing commits.
func (s *Step) Simulate(ctx context.Context, env *engine.Env, ui engine.UI) error {
	say := func(message string) { ui.Report(engine.Event{StepID: stepID, Message: message}) }
	domain, err := engine.AskText(ctx, ui, "domain", "Public cluster domain (a domain you manage in Cloudflare)", "example.com")
	if err != nil {
		return err
	}
	overlay, err := engine.AskText(ctx, ui, "overlay-domain", "Tailscale tailnet domain (for example tail1234.ts.net)", "adaetum-demo.ts.net")
	if err != nil {
		return err
	}
	domain, overlay = strings.TrimSpace(domain), strings.TrimSpace(overlay)
	if domain == "" {
		domain = "example.com"
	}
	if overlay == "" {
		overlay = "adaetum-demo.ts.net"
	}
	env.SetValue(domainKey, domain)
	env.SetValue(overlayKey, overlay)
	say("Profile choices accepted for " + domain + " and " + overlay + ".")
	if err := engine.Approve(ctx, ui, "apply-profile", "Apply this public configuration to your private recovery repository?"); err != nil {
		return err
	}
	say("Adaetum defaults applied to the profile.")
	say("Derived files rendered; commit checks passed.")
	say("Profile commit published to the private recovery repository.")
	return nil
}
