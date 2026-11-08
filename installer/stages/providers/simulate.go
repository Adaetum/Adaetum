package providers

import (
	"context"

	"github.com/Adaetum/Adaetum/installer/engine"
)

// Simulate reuses the real provider question layouts but replaces credential
// validation, keychain writes, and provider API calls with successful examples.
func (s *Step) Simulate(ctx context.Context, env *engine.Env, ui engine.UI) error {
	say := func(message string) { ui.Report(engine.Event{StepID: stepID, Message: message}) }
	cloudflare := cloudflareTokenScreen()
	cloudflare.Default = "cfat_demo_cloudflare_token"
	if _, err := ui.Ask(ctx, cloudflare); err != nil {
		return err
	}
	say("Cloudflare account token accepted; example.com was discovered.")
	if _, err := engine.Choose(ctx, ui, "cloudflare-zone", "Choose the public DNS zone and owning Cloudflare account", []string{"example.com — Adaetum demo account"}, "example.com — Adaetum demo account"); err != nil {
		return err
	}
	env.SetValue("domain", "example.com")
	env.SetValue("cloudflare_account_id", "example-account")
	env.SetValue("cloudflare_account_name", "Adaetum demo account")
	say("Cloudflare zone selected; DNS, tunnel, and R2 access accepted.")

	tailscale := tailscaleTokenScreen()
	tailscale.Default = "ts_demo_access_token"
	if _, err := ui.Ask(ctx, tailscale); err != nil {
		return err
	}
	say("One-day Tailscale access token accepted.")
	env.SetValue("overlay_domain", "adaetum-demo.ts.net")
	say("Using discovered Tailscale tailnet adaetum-demo.ts.net.")
	say("Tailnet selected; Adaetum tag ownership accepted.")
	say("Scoped node and Kubernetes operator OAuth clients created.")
	return nil
}
