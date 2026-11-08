package github

import "testing"

func TestNormalizeAndRepo(t *testing.T) {
	cases := map[string]string{
		"https://github.com/Adaetum/Adaetum.git":    "adaetum/adaetum",
		"git@github.com:Adaetum/Adaetum.git":        "adaetum/adaetum",
		"ssh://git@github.com/Adaetum/Adaetum":      "adaetum/adaetum",
		"https://github.com/Alice/Adaetum-cluster/": "alice/adaetum-cluster",
		"https://example.com/x/y.git":               "",
		"https://github.com/onlyowner":              "",
	}
	for url, want := range cases {
		if got := Repo(url); got != want {
			t.Errorf("Repo(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestIsUpstream(t *testing.T) {
	if !IsUpstream("git@github.com:Adaetum/Adaetum.git") {
		t.Error("upstream not recognised")
	}
	if IsUpstream("https://github.com/alice/Adaetum-cluster.git") {
		t.Error("a private recovery repository was taken for upstream")
	}
}

func TestRepoExactKeepsCapitalisation(t *testing.T) {
	if got := RepoExact("https://github.com/Alice/Adaetum-Cluster.git"); got != "Alice/Adaetum-Cluster" {
		t.Fatalf("got %q", got)
	}
	if got := RepoExact("https://example.com/Alice/x.git"); got != "" {
		t.Fatalf("got %q", got)
	}
}
