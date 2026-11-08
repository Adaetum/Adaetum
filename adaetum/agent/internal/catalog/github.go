package catalog

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// GitHub is the last place a logo is looked for. Most apps are developed there, and
// the avatar of the organisation behind a project is usually its logo.
//
// Two rules keep it from showing the wrong thing. The repository must be named exactly
// like the app and well known (a generic name matches hundreds of unrelated projects),
// and its owner must be an ORGANISATION: a person's avatar is their own face, not an
// app's logo, and the agent must never put a stranger's photo on a card. The picture is
// then fetched only from GitHub's avatar host.

const (
	githubURL    = "https://api.github.com"
	githubAvatar = "avatars.githubusercontent.com"
	// Higher than for charts: GitHub holds far more projects sharing any given name.
	githubStars = 1000
)

func (c *Client) github() string {
	if c.GitHubURL != "" {
		return c.GitHubURL
	}
	return githubURL
}

func (c *Client) avatarHost() string {
	if c.AvatarHost != "" {
		return c.AvatarHost
	}
	return githubAvatar
}

// GitHubLogo returns the avatar of the organisation behind the best-known repository
// named exactly like the app. No match means no logo, not a guess.
func (c *Client) GitHubLogo(ctx context.Context, name string) ([]byte, string, error) {
	if !safeSegment(name) || len(name) > 63 {
		return nil, "", errNoLogo
	}
	var answer struct {
		Items []struct {
			Name     string `json:"name"`
			Stars    int    `json:"stargazers_count"`
			Archived bool   `json:"archived"`
			Owner    struct {
				Type   string `json:"type"`
				Avatar string `json:"avatar_url"`
			} `json:"owner"`
		} `json:"items"`
	}
	address := fmt.Sprintf("%s/search/repositories?per_page=10&sort=stars&q=%s", c.github(), url.QueryEscape(name+" in:name"))
	if _, err := c.getJSON(ctx, address, &answer); err != nil {
		return nil, "", err
	}
	best, stars := "", -1
	for _, item := range answer.Items {
		if !strings.EqualFold(item.Name, name) || item.Archived || item.Owner.Type != "Organization" ||
			item.Stars < githubStars || item.Stars <= stars {
			continue
		}
		best, stars = item.Owner.Avatar, item.Stars
	}
	if best == "" {
		return nil, "", errNoLogo
	}
	avatar, err := url.Parse(best)
	if err != nil || avatar.Host != c.avatarHost() || (avatar.Scheme != "https" && c.AvatarHost == "") {
		return nil, "", errNoLogo
	}
	query := avatar.Query()
	query.Set("s", "128")
	avatar.RawQuery = query.Encode()
	return c.fetchImage(ctx, avatar.String())
}
