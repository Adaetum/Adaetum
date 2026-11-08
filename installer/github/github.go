// Package github holds the small, pure helpers for reasoning about GitHub remotes.
package github

import (
	"regexp"
	"strings"
)

// UpstreamRepo is the canonical public project. It is only ever a read-only
// remote: GitHub cannot make a private fork of a public repository, so cluster
// configuration and secrets live in a separate private repository.
const (
	UpstreamRepo = "adaetum/adaetum"
	UpstreamURL  = "https://github.com/Adaetum/Adaetum.git"
)

var (
	sshPrefix   = regexp.MustCompile(`^(ssh://)?git@github\.com[:/]`)
	httpsPrefix = regexp.MustCompile(`^https?://github\.com/`)
	gitSuffix   = regexp.MustCompile(`\.git$`)
)

// Normalize reduces any GitHub remote URL form to "github.com/owner/name" in
// lower case, so two spellings of the same repository compare equal.
func Normalize(url string) string {
	u := strings.TrimRight(strings.ToLower(strings.TrimSpace(url)), "/")
	u = sshPrefix.ReplaceAllString(u, "github.com/")
	u = httpsPrefix.ReplaceAllString(u, "github.com/")
	return gitSuffix.ReplaceAllString(u, "")
}

// Repo returns "owner/name" for a GitHub remote URL, or "" when it is not one.
func Repo(url string) string {
	n := Normalize(url)
	if !strings.HasPrefix(n, "github.com/") {
		return ""
	}
	n = strings.TrimPrefix(n, "github.com/")
	if strings.Contains(n, "/") && !strings.HasPrefix(n, "/") {
		return n
	}
	return ""
}

// IsUpstream reports whether url is the canonical public project.
func IsUpstream(url string) bool { return Normalize(url) == "github.com/"+UpstreamRepo }

// RepoExact is Repo with the owner and name's original capitalisation, for building
// a URL to push to. GitHub ignores case, but another remote spelling might not.
func RepoExact(url string) string {
	u := strings.TrimRight(strings.TrimSpace(url), "/")
	lower := strings.ToLower(u)
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "ssh://git@github.com/", "git@github.com:", "ssh://git@github.com:"} {
		if strings.HasPrefix(lower, prefix) {
			u = u[len(prefix):]
			u = strings.TrimSuffix(u, ".git")
			if strings.Contains(u, "/") && !strings.HasPrefix(u, "/") {
				return u
			}
			return ""
		}
	}
	return ""
}
