// Package providers is the second setup stage: authorize Cloudflare and Tailscale,
// choose the public DNS zone and the tailnet, prepare Tailscale tag ownership, and
// create the scoped OAuth clients that nodes enrol with.
//
// It ports first_run_select_cloudflare_domain, first_run_select_tailscale_domain,
// first_run_capture_tailscale_oauth and their saved-credential helpers from
// tasks/scripts/first-run-preflight.sh, and replaces list-cloudflare-zones.py and
// list-tailscale-tailnets.py. Two checks that belong to the later bootstrap
// machinery (Cloudflare access validation, and the Tailscale policy and OAuth
// calls) still run in bootstrap-cloudflare.py and bootstrap-tailscale.py until
// those stages are ported.
//
// Secrets: every token enters through a secret screen, is passed to the scripts on
// standard input (never as an argument), is held in memory in engine.Env, and is
// kept in memory for the current setup run.
package providers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"runtime"
	"strings"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/platform"
	"github.com/Adaetum/Adaetum/installer/provider"
	"github.com/Adaetum/Adaetum/installer/secrets"
	"github.com/Adaetum/Adaetum/installer/sys"
)

const (
	stepID = "providers"

	keyCloudflare   = "cloudflare-api-token"
	keyTailscale    = "tailscale-api-token"
	keyOAuthID      = "tailscale-oauth-client-id"
	keyOAuthSecret  = "tailscale-oauth-client-secret"
	keyOperatorID   = "tailscale-operator-oauth-client-id"
	keyOperatorSec  = "tailscale-operator-oauth-client-secret"
	cloudflareTool  = "./tasks/scripts/bootstrap-cloudflare.py"
	tailscaleTool   = "./tasks/scripts/bootstrap-tailscale.py"
	clusterTag      = "tag:cluster"
	placeholderNet  = "example-tailnet.ts.net"
	maxNameAttempts = 5
)

var tailnetName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?\.ts\.net$`)

// Step is the providers stage.
type Step struct {
	Run      sys.Runner
	Dir      string
	Store    secrets.Store
	API      provider.Client
	Platform platform.Scripts
}

// New returns the stage for the checkout at dir.
func New(run sys.Runner, dir string) *Step {
	return &Step{Run: run, Dir: dir, Store: secrets.Keyring{}, API: provider.Client{}, Platform: platform.Scripts{Run: run, Dir: dir}}
}

func (s *Step) ID() string    { return stepID }
func (s *Step) Title() string { return "Cloudflare and Tailscale access" }

func python() string {
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

// Check never reports Done: the tokens must be in memory for the stages that
// follow, so this stage always has to run. It does not read the credential store,
// because on some systems merely reading it prompts the operator.
func (s *Step) Check(ctx context.Context, _ *engine.Env) (engine.Finding, error) {
	if res, err := s.Run.Run(ctx, s.Dir, python(), "--version"); err != nil || !res.OK() {
		return engine.Finding{Status: engine.Blocked, Detail: "python3 is required: Cloudflare and Tailscale access is validated by the existing bootstrap scripts."}, nil
	}
	if s.Store.Available() {
		return engine.Finding{Status: engine.Needed, Detail: "tokens will be reused from " + s.Store.Name() + " when they are still valid"}, nil
	}
	return engine.Finding{Status: engine.Needed, Detail: "this machine has no credential store, so tokens are asked for again on every run and never saved"}, nil
}

func (s *Step) Plan(ctx context.Context, env *engine.Env) ([]string, error) {
	finding, err := s.Check(ctx, env)
	if err != nil {
		return nil, err
	}
	return []string{
		"Use a Cloudflare account token (saved one if still valid) and choose your public DNS zone and its owning account.",
		"Use a one-day Tailscale access token and choose your tailnet.",
		"Prepare Tailscale tag ownership, then create the scoped node and Kubernetes operator OAuth clients.",
		"Keep provider credentials in memory for the remaining setup stages.",
		"Right now: " + finding.Detail + ".",
	}, nil
}

// flow carries what one Apply needs, so the provider parts read top to bottom.
type flow struct {
	s     *Step
	env   *engine.Env
	ui    engine.UI
	say   func(string)
	ns    string
	store secrets.Store
}

func (s *Step) Apply(ctx context.Context, env *engine.Env, ui engine.UI) error {
	origin := ""
	if res, err := s.Run.Run(ctx, s.Dir, "git", "remote", "get-url", "origin"); err == nil && res.OK() {
		origin = strings.TrimSpace(res.Stdout)
	}
	f := &flow{
		s: s, env: env, ui: ui, ns: secrets.Namespace(origin), store: s.Store,
		say: func(msg string) { ui.Report(engine.Event{StepID: stepID, Message: msg}) },
	}

	existing := platform.Profile{}
	if !env.Clean {
		if p, err := s.Platform.Show(ctx); err == nil && !p.Placeholder() {
			existing = p
		}
	}
	if err := f.cloudflare(ctx, existing); err != nil {
		return err
	}
	tailnet, err := f.tailscale(ctx, existing)
	if err != nil {
		return err
	}
	return f.oauth(ctx, tailnet)
}

// ---- credential store helpers ----------------------------------------------

// saved returns a stored credential, or "" when there is none, the store is
// unavailable, or the operator asked to start clean.
func (f *flow) saved(key string) string {
	if f.env.Clean || !f.store.Available() {
		return ""
	}
	v, err := f.store.Get(f.ns, key)
	if err != nil {
		return ""
	}
	return v
}

func (f *flow) forget(keys ...string) {
	for _, k := range keys {
		_ = f.store.Delete(f.ns, k)
	}
}

func (f *flow) secret(ctx context.Context, id, prompt string) (string, error) {
	v, err := f.ui.Ask(ctx, engine.Screen{ID: id, Kind: engine.Secret, Prompt: prompt})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(v), nil
}

// ---- Cloudflare -------------------------------------------------------------

// Cloudflare's account-token template requests the bootstrap permissions. The
// Tunnel group ID identifies the specific required permission.
func cloudflareTokenTemplateURL() string {
	permissions := `[{"key":"account_api_tokens","type":"read"},{"key":"account_api_tokens","type":"edit"},{"key":"workers_r2","type":"read"},{"key":"workers_r2","type":"edit"},{"key":"c07321b023e944ff818fec44d8203567","type":"edit"},{"key":"workers_scripts","type":"read"},{"key":"workers_scripts","type":"edit"},{"key":"zone","type":"read"},{"key":"dns","type":"read"},{"key":"dns","type":"edit"},{"key":"workers_routes","type":"read"},{"key":"workers_routes","type":"edit"}]`
	return "https://dash.cloudflare.com/?to=/:account/api-tokens&permissionGroupKeys=" + url.QueryEscape(permissions) + "&name=" + url.QueryEscape("Adaetum bootstrap")
}

// cloudflareTokenScreen opens the account-owned token template and collects its secret.
func cloudflareTokenScreen() engine.Screen {
	return engine.Screen{
		ID: "cloudflare-token", Kind: engine.Secret, Prompt: "Cloudflare token",
		Title: "Connect Cloudflare",
		Intro: "Sign in or create a Cloudflare account.",
		Steps: []engine.Instruction{
			{Text: "Open the template and select your account.", Link: cloudflareTokenTemplateURL()},
			{Text: "Review permissions; select your account and domain as the scope."},
			{Text: "Create the token and paste it below."},
		},
	}
}

func (f *flow) cloudflare(ctx context.Context, existing platform.Profile) error {

	var token string
	var zones []provider.Zone
	if tok := f.saved(keyCloudflare); tok != "" {
		f.say("Found a saved Cloudflare setup token in " + f.store.Name() + "; validating it before reuse.")
		if z, err := f.s.API.Zones(ctx, tok); err == nil {
			token, zones = tok, z
			f.say("Reusing the validated Cloudflare account token.")
		} else {
			f.say("The saved Cloudflare token is no longer valid for zone discovery and was removed.")
			f.forget(keyCloudflare)
		}
	}
	if token == "" {
		var err error
		if token, zones, err = f.askCloudflareToken(ctx); err != nil {
			return err
		}
	}

	zone, err := f.chooseZone(ctx, zones, existing["domain"])
	if err != nil {
		return err
	}

	if !f.cloudflareAccess(ctx, token, zone) {
		if f.store.Available() {
			// It passed discovery but not the whole contract; never leave it for a
			// later run to select again.
			f.forget(keyCloudflare)
		}
		if token, err = f.fixCloudflareAccess(ctx, token, zone); err != nil {
			return err
		}
	}

	f.say(fmt.Sprintf("Selected zone %s in Cloudflare account %s.", zone.Name, zone.AccountName))
	f.env.SetSecret("cloudflare_token", token)
	f.env.SetValue("domain", zone.Name)
	f.env.SetValue("cloudflare_account_id", zone.AccountID)
	f.env.SetValue("cloudflare_account_name", zone.AccountName)
	return nil
}

func (f *flow) askCloudflareToken(ctx context.Context) (string, []provider.Zone, error) {
	// The instructions, the paste box and the check are one screen: a token that
	// does not work is explained where it was pasted and can be replaced right there.
	var zones []provider.Zone
	token, err := engine.AskValid(ctx, f.ui, cloudflareTokenScreen(),
		func(answer string) (string, error) {
			tok := strings.TrimSpace(answer)
			if tok == "" {
				return "", errors.New("paste the token you just created")
			}
			z, err := f.s.API.Zones(ctx, tok)
			if err != nil {
				return "", err
			}
			zones = z
			return fmt.Sprintf("Cloudflare access validated: %d zone(s) found.", len(z)), nil
		})
	if err != nil {
		return "", nil, err
	}
	token = strings.TrimSpace(token)
	if strings.HasPrefix(token, "cfat_") {
		f.say("Cloudflare account API token detected.")
	} else {
		// A user-owned token works but is tied to one person's membership; agreeing
		// is a risk, so the default is no and a silent run must say yes explicitly.
		err := engine.ApproveScreen(ctx, f.ui, engine.Screen{
			ID: "cloudflare-non-account-token", Default: "no", Title: "Use this token anyway?",
			Intro: "It isn't a Cloudflare account token (cfat_), so it is tied to one person's membership.", YesLabel: "Use it", NoLabel: "Go back",
		})
		if err != nil {
			return "", nil, err
		}
	}
	return token, zones, nil
}

func (f *flow) chooseZone(ctx context.Context, zones []provider.Zone, saved string) (provider.Zone, error) {
	if saved != "" && !f.env.Clean {
		for _, z := range zones {
			if z.Name == saved {
				f.say("Reusing saved Cloudflare zone " + saved + " from platform.yaml.")
				return z, nil
			}
		}
		f.say("Saved Cloudflare zone " + saved + " is no longer available to this token; choose another zone.")
	}
	labels := make([]string, len(zones))
	for i, z := range zones {
		labels[i] = z.Label()
	}
	label, err := engine.Choose(ctx, f.ui, "cloudflare-zone", "Choose the public DNS zone and owning Cloudflare account", labels, "")
	if err != nil {
		return provider.Zone{}, err
	}
	for i, l := range labels {
		if l == label {
			return zones[i], nil
		}
	}
	return provider.Zone{}, errors.New("Cloudflare zone selection did not resolve an owning account")
}

// cloudflareAccess runs the full access check (Tunnel, R2, Workers, DNS for the
// chosen zone and account) in the existing bootstrap script.
func (f *flow) cloudflareAccess(ctx context.Context, token string, z provider.Zone) bool {
	res, err := f.s.Run.RunInput(ctx, f.s.Dir, token, python(), cloudflareTool, "--token-stdin",
		"--account-id", z.AccountID, "--zone-domain", z.Name, "--validate-access-only")
	return err == nil && res.OK()
}

func (f *flow) fixCloudflareAccess(ctx context.Context, token string, z provider.Zone) (string, error) {
	err := engine.ApproveScreen(ctx, f.ui, engine.Screen{
		ID: "cloudflare-permissions-updated", Title: "One more permission needed",
		Intro: "The token can't do everything setup needs.",
		Steps: []engine.Instruction{
			{Text: "Open the token and edit it (or create a replacement)", Link: "https://dash.cloudflare.com/" + z.AccountID + "/api-tokens/create"},
			{Text: "Check the account and domain permissions, including Cloudflare Tunnel → Write for the whole account"},
			{Text: "Save it, then check again"},
		},
		YesLabel: "Check again", NoLabel: "Cancel",
	})
	if err != nil {
		return "", err
	}
	if f.cloudflareAccess(ctx, token, z) {
		f.say("The existing Cloudflare token now has the access setup needs.")
		return token, nil
	}

	replacement, err := engine.AskValid(ctx, f.ui, engine.Screen{ID: "cloudflare-replacement-token", Kind: engine.Secret, Prompt: "Replacement token",
		Title: "Paste the replacement token", Intro: "It must cover " + z.Name + " in the same account, with every permission listed earlier."},
		func(answer string) (string, error) {
			tok := strings.TrimSpace(answer)
			if tok == "" {
				return "", errors.New("a replacement Cloudflare token is required to continue")
			}
			zones, err := f.s.API.Zones(ctx, tok)
			if err != nil {
				return "", fmt.Errorf("the replacement token cannot read the selected Cloudflare zone: %w", err)
			}
			found := false
			for _, c := range zones {
				if c.Name == z.Name && c.AccountID == z.AccountID {
					found = true
				}
			}
			if !found {
				return "", fmt.Errorf("the replacement token is not scoped to %s in the selected Cloudflare account", z.Name)
			}
			if !f.cloudflareAccess(ctx, tok, z) {
				return "", errors.New("the replacement token still lacks part of the Cloudflare account or zone permission set shown above")
			}
			return "The replacement token has the access setup needs.", nil
		})
	if err != nil {
		return "", err
	}
	replacement = strings.TrimSpace(replacement)
	return replacement, nil
}

// ---- Tailscale --------------------------------------------------------------

// tailscaleTokenScreen is the one screen where the operator creates and pastes the
// temporary Tailscale token.
func tailscaleTokenScreen() engine.Screen {
	return engine.Screen{
		ID: "tailscale-token", Kind: engine.Secret, Prompt: "Tailscale access token",
		Title: "Connect Tailscale",
		Intro: "Sign in or create a Tailscale account. The one-day token gives setup full API access until it expires. If your tailnet is empty, Adaetum briefly joins a temporary node to find its DNS name, then removes it.",
		Steps: []engine.Instruction{
			{Text: "Open Tailscale Keys and sign in.", Link: "https://login.tailscale.com/admin/settings/keys"},
			{Text: "Generate an API access token with a 1-day expiry."},
			{Text: "Copy the token and paste it below."},
		},
	}
}

func (f *flow) oauthPair(ctx context.Context, idKey, secretKey, flag, what string) (id, secret string) {
	id, secret = f.saved(idKey), f.saved(secretKey)
	if id == "" || secret == "" {
		return "", ""
	}
	f.say("Found a saved " + what + " in " + f.store.Name() + "; validating it before reuse.")
	res, err := f.s.Run.RunInput(ctx, f.s.Dir, id+"\n"+secret+"\n", python(), tailscaleTool, "--oauth-credentials-stdin", flag)
	if err == nil && res.OK() {
		f.say("Reusing the validated " + what + ".")
		return id, secret
	}
	// A half-saved OAuth client is never reusable, so both halves go together.
	f.say("The saved " + what + " is invalid and was removed.")
	f.forget(idKey, secretKey)
	return "", ""
}

func (f *flow) tailscale(ctx context.Context, existing platform.Profile) (string, error) {

	oauthID, oauthSecret := f.oauthPair(ctx, keyOAuthID, keyOAuthSecret, "--validate-oauth-only", "Tailscale OAuth client")
	opID, opSecret := f.oauthPair(ctx, keyOperatorID, keyOperatorSec, "--validate-operator-oauth-only", "Tailscale Kubernetes Operator OAuth client")
	f.env.SetSecret("tailscale_oauth_client_id", oauthID)
	f.env.SetSecret("tailscale_oauth_client_secret", oauthSecret)
	f.env.SetSecret("tailscale_operator_oauth_client_id", opID)
	f.env.SetSecret("tailscale_operator_oauth_client_secret", opSecret)

	var token string
	var tailnets []string
	if tok := f.saved(keyTailscale); tok != "" {
		f.say("Found a saved Tailscale setup token in " + f.store.Name() + "; validating it before reuse.")
		if t, err := f.s.API.Tailnets(ctx, tok); err == nil {
			token, tailnets = tok, t
			f.say("Reusing the validated Tailscale access token.")
		} else {
			f.say("The saved Tailscale token is invalid and was removed.")
			f.forget(keyTailscale)
		}
	}
	if token == "" {
		if oauthID != "" && opID != "" && existing["overlay_domain"] != "" {
			// A durable OAuth client replaces the expired one-day token on a rerun.
			tailnets = []string{existing["overlay_domain"]}
			f.say("The saved OAuth clients replace the expired temporary Tailscale token for this rerun.")
		} else {
			var err error
			if token, tailnets, err = f.askTailscaleToken(ctx); err != nil {
				return "", err
			}
		}
	}
	f.env.SetSecret("tailscale_token", token)

	// A new tailnet has no devices from which the public API can derive its
	// suffix. Briefly join an ephemeral setup node only when no saved name exists.
	if len(tailnets) == 0 && token != "" && (f.env.Clean || existing["overlay_domain"] == "" || existing["overlay_domain"] == placeholderNet) {
		f.say("Finding your tailnet name with a temporary Tailscale setup node…")
		name, discoveryErr := discoverEmptyTailnet(ctx, f.s.API, token, joinDiscoveryNode)
		if errors.Is(discoveryErr, errDiscoveryCleanup) {
			return "", discoveryErr
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if discoveryErr == nil {
			tailnets = []string{name}
		} else {
			f.say("Automatic tailnet discovery was unavailable; you can enter the name instead.")
		}
	}

	tailnet, err := f.chooseTailnet(ctx, tailnets, existing["overlay_domain"])
	if err != nil {
		return "", err
	}
	f.env.SetValue("overlay_domain", tailnet)

	f.say("Preparing Tailscale tags…")
	if oauthID != "" && oauthSecret != "" {
		res, err := f.s.Run.RunInput(ctx, f.s.Dir, oauthID+"\n"+oauthSecret+"\n", python(), tailscaleTool,
			"--oauth-credentials-stdin", "--tailnet", tailnet, "--cluster-tag", clusterTag, "--prepare-policy-only")
		if err != nil || !res.OK() {
			return "", errors.New("unable to validate Tailscale tag ownership with the saved OAuth client")
		}
	} else {
		res, err := f.s.Run.RunInput(ctx, f.s.Dir, token, python(), tailscaleTool,
			"--user-token-stdin", "--tailnet", tailnet, "--cluster-tag", clusterTag, "--prepare-policy-only")
		if err != nil || !res.OK() {
			return "", errors.New("unable to prepare Tailscale tag ownership; check that the access token can write the policy file")
		}
	}
	f.say("Tailscale tag ownership is ready.")
	return tailnet, nil
}

func (f *flow) askTailscaleToken(ctx context.Context) (string, []string, error) {
	var tailnets []string
	token, err := engine.AskValid(ctx, f.ui, tailscaleTokenScreen(),
		func(answer string) (string, error) {
			tok := strings.TrimSpace(answer)
			if tok == "" {
				return "", errors.New("paste the access token you just created")
			}
			t, err := f.s.API.Tailnets(ctx, tok)
			if err != nil {
				return "", fmt.Errorf("Tailscale did not accept that token; check it and the permissions listed above: %w", err)
			}
			tailnets = t
			return "Tailscale access validated.", nil
		})
	if err != nil {
		return "", nil, err
	}
	token = strings.TrimSpace(token)
	return token, tailnets, nil
}

func (f *flow) chooseTailnet(ctx context.Context, tailnets []string, saved string) (string, error) {
	if saved == placeholderNet {
		saved = ""
	}
	if saved != "" && !f.env.Clean {
		for _, t := range tailnets {
			if t == saved {
				f.say("Reusing saved Tailscale DNS name " + saved + " from platform.yaml.")
				return saved, nil
			}
		}
		if len(tailnets) == 0 {
			f.say("Tailscale returned no discoverable device DNS names; reusing saved DNS name " + saved + " from platform.yaml.")
			return saved, nil
		}
		f.say("Saved Tailscale DNS name " + saved + " is no longer available; choose another tailnet.")
	}
	if len(tailnets) == 1 {
		f.say("Using discovered Tailscale tailnet " + tailnets[0] + ".")
		return tailnets[0], nil
	}
	if len(tailnets) > 1 {
		return engine.Choose(ctx, f.ui, "tailscale-tailnet", "Choose the Tailscale tailnet for this cluster", tailnets, "")
	}

	f.say("Could not discover the tailnet DNS name; find it on Tailscale's DNS page.")
	for attempt := 0; attempt < maxNameAttempts; attempt++ {
		in, err := engine.AskText(ctx, f.ui, "tailscale-tailnet", "Tailnet name (ends in .ts.net; shown in Tailscale under DNS)", "")
		if err != nil {
			return "", err
		}
		name := strings.ToLower(strings.TrimSpace(in))
		name = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(name, "https://"), "http://"), "/")
		name = strings.Join(strings.Fields(name), "")
		if tailnetName.MatchString(name) {
			return name, nil
		}
		f.say("Enter the tailnet DNS name shown on Tailscale's DNS page, for example tailnet-a1b2.ts.net.")
	}
	return "", errors.New("no valid tailnet DNS name was given")
}

// ---- OAuth clients ----------------------------------------------------------

// oauth makes sure both scoped OAuth clients exist: the node enrolment client and
// the Kubernetes operator client. Each replaces the temporary user token for
// later, and is an application identity rather than one person's.
func (f *flow) oauth(ctx context.Context, tailnet string) error {
	if err := f.createClient(ctx, tailnet, "tailscale_oauth_client", "Tailscale OAuth client", "--create-oauth-client", true,
		"TAILSCALE_OAUTH_CLIENT_ID", "TAILSCALE_OAUTH_CLIENT_SECRET"); err != nil {
		return err
	}
	return f.createClient(ctx, tailnet, "tailscale_operator_oauth_client", "Tailscale Kubernetes Operator OAuth client", "--create-operator-oauth-client", false,
		"TAILSCALE_OPERATOR_OAUTH_CLIENT_ID", "TAILSCALE_OPERATOR_OAUTH_CLIENT_SECRET")
}

func (f *flow) createClient(ctx context.Context, tailnet, envPrefix, what, flag string, withTag bool, idVar, secretVar string) error {
	if f.env.Secret(envPrefix+"_id") != "" && f.env.Secret(envPrefix+"_secret") != "" {
		return nil
	}
	token := f.env.Secret("tailscale_token")
	if token == "" {
		return fmt.Errorf("no saved %s and no setup token to create one with; run setup again and give a Tailscale access token", what)
	}
	f.say("Creating the scoped " + what + "...")
	args := []string{tailscaleTool, "--user-token-stdin", "--tailnet", tailnet}
	if withTag {
		args = append(args, "--cluster-tag", clusterTag)
	}
	args = append(args, flag)
	res, err := f.s.Run.RunInput(ctx, f.s.Dir, token, python(), args...)
	if err != nil || !res.OK() {
		return fmt.Errorf("unable to create the %s; ensure the access token has OAuth Keys: Write", what)
	}
	var id, secret string
	for _, line := range strings.Split(res.Stdout, "\n") {
		k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), "=")
		switch {
		case ok && k == idVar:
			id = v
		case ok && k == secretVar:
			secret = v
		}
	}
	if id == "" || secret == "" {
		return fmt.Errorf("Tailscale created the %s without returning both credentials", what)
	}
	f.say("The " + what + " was created.")
	f.env.SetSecret(envPrefix+"_id", id)
	f.env.SetSecret(envPrefix+"_secret", secret)
	return nil
}
