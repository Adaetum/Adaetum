package providers

import (
	"context"

	"github.com/Adaetum/Adaetum/installer/engine"
)

// Simulate reuses the real provider question layouts but replaces credential
// validation, keychain writes, and provider API calls with successful examples.
func (s *Step) Simulate(ctx context.Context, _ *engine.Env, ui engine.UI) error {
	say := func(message string) { ui.Report(engine.Event{StepID: stepID, Message: message}) }
	cloudflare := cloudflareTokenScreen()
	cloudflare.Default = "cfat_demo_cloudflare_token"
	cloudflare.Intro += " Sample token for this rehearsal; no request is sent."
	if _, err := ui.Ask(ctx, cloudflare); err != nil {
		return err
	}
	say("Cloudflare account token accepted; example.com was discovered.")
	if _, err := ui.Ask(ctx, engine.Screen{
		ID: "save-cloudflare-token", Kind: engine.Confirm, Default: "yes",
		Prompt: "Save this Cloudflare setup token in your operating system credential store so a cancelled setup can resume?",
	}); err != nil {
		return err
	}
	if _, err := engine.Choose(ctx, ui, "cloudflare-zone", "Choose the public DNS zone and owning Cloudflare account", []string{"example.com — Adaetum demo account"}, "example.com — Adaetum demo account"); err != nil {
		return err
	}
	say("Cloudflare zone selected; DNS, tunnel, and R2 access accepted.")

	tailscale := tailscaleTokenScreen()
	tailscale.Default = "ts_demo_access_token"
	tailscale.Intro += " Sample token for this rehearsal; no request is sent."
	if _, err := ui.Ask(ctx, tailscale); err != nil {
		return err
	}
	say("One-day Tailscale access token accepted.")
	if _, err := ui.Ask(ctx, engine.Screen{
		ID: "save-tailscale-token", Kind: engine.Confirm, Default: "yes",
		Prompt: "Save this one-day Tailscale token in your operating system credential store so a cancelled setup can resume?",
	}); err != nil {
		return err
	}
	if _, err := engine.Choose(ctx, ui, "tailscale-tailnet", "Choose the Tailscale tailnet for this cluster", []string{"adaetum-demo.ts.net"}, "adaetum-demo.ts.net"); err != nil {
		return err
	}
	say("Tailnet selected; Adaetum tag ownership accepted.")
	say("Scoped node and Kubernetes operator OAuth clients created.")
	return nil
}
