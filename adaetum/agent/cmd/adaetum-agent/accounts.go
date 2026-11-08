package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/accounts"
	"github.com/Adaetum/Adaetum/agent/internal/identity"
	"github.com/Adaetum/Adaetum/agent/internal/membership"
)

// People signing in with their own accounts. The accounts live in a separate auth service
// (auth/); this file connects the agent to it. Without -auth-socket nothing here is active
// and the agent behaves exactly as before (the access token alone). See
// docs/recovery-login.md.

type authFlags struct {
	socket     *string
	secretFile *string
}

func addAuthFlags(flags *flag.FlagSet) *authFlags {
	return &authFlags{
		socket:     flags.String("auth-socket", "", "socket of the auth service (accounts and sign-in); off when empty, leaving the access token as the only login"),
		secretFile: flags.String("auth-secret-file", "", "file holding the secret shared with the auth service (mode 600); required with -auth-socket"),
	}
}

// client returns the auth service client, or nil when accounts are not configured. A
// misconfiguration stops the agent with a reason rather than starting without the login
// the operator asked for.
func (a *authFlags) client() *accounts.Client {
	if *a.socket == "" {
		return nil
	}
	secret, reason := checkToken(*a.secretFile, realSystem())
	if reason != "" {
		fmt.Fprintln(os.Stderr, "-auth-socket needs -auth-secret-file, but", strings.Replace(reason, "access token", "shared secret", -1))
		os.Exit(2)
	}
	return accounts.NewUnixClient(*a.socket, secret)
}

// combinedGuard returns the login for people: the access token, an account, or both. It is
// nil when neither is configured (a loopback development agent with no login at all).
func combinedGuard(token string, auth *accounts.Client) func(http.Handler) http.Handler {
	if auth == nil {
		if token == "" {
			return nil
		}
		return bearerGuard(token)
	}
	return (&accounts.Guard{Token: token, Client: auth}).Middleware
}

// requireLogin puts the guard in front of every API request EXCEPT the sign-in pages
// themselves, which must work before anyone is signed in. The web interface's own files
// and the liveness check stay open, as before: the pages hold no host data.
func requireLogin(next http.Handler, guard func(http.Handler) http.Handler) http.Handler {
	guarded := guard(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		isAPI := path == "/api" || strings.HasPrefix(path, "/api/")
		public := path == "/api/auth-state" || path == "/api/setup" || strings.HasPrefix(path, "/api/auth/")
		if isAPI && !public {
			guarded.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// mountAccounts adds the sign-in routes (or, with no auth service, the one answer that
// tells the page to ask for the access token only) and the people routes.
func mountAccounts(mux *http.ServeMux, auth *accounts.Client, guard func(http.Handler) http.Handler) {
	if auth == nil {
		accounts.RegisterPublic(mux, accounts.Disabled())
		return
	}
	accounts.RegisterPublic(mux, auth.PublicProxy())
	(&accounts.PeopleAPI{Client: auth, Guard: guard}).Register(mux)
}

// personName is who the record should name for a change: the signed-in person's email, or
// "" when the access token was used (the record then says "operator", as it always has).
func personName(r *http.Request) string {
	if who, ok := accounts.FromContext(r.Context()); ok && !who.Token {
		return who.Email
	}
	return ""
}

// startAccountSync keeps a joined host's copy of the accounts current. A host that has not
// joined a cluster owns its accounts and does nothing here.
func startAccountSync(ctx context.Context, dataDir, hostID string, auth *accounts.Client) {
	if auth == nil || dataDir == "" {
		return
	}
	link, joined, err := membership.LoadLink(dataDir)
	if err != nil || !joined {
		return
	}
	key, err := identity.LoadOrCreateHostKey(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot load this host's key, so accounts will not be copied from the cluster:", err)
		return
	}
	upstream := &accounts.HTTPUpstream{Link: link, HostID: hostID, Key: key}
	go accounts.RunReplica(ctx, auth, upstream, 10*time.Second, func(o accounts.Outcome, err error) {
		switch {
		case err != nil && !errorsIsUpstream(err):
			fmt.Fprintln(os.Stderr, "accounts:", err)
		case err != nil:
			fmt.Fprintln(os.Stderr, "accounts: the cluster cannot be reached, so sign-in uses the last copy of the accounts")
		default:
			fmt.Fprintln(os.Stderr, "accounts:", o)
		}
	})
}

func errorsIsUpstream(err error) bool { return errors.Is(err, accounts.ErrUpstream) }
