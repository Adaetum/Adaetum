package catalog

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Docker Hub is a logo source for apps that ship as images. Its documented search has
// no pictures, so this uses the catalog search the Docker Hub website itself uses. That
// is not a published API and may change; if it does, the lookup finds nothing and the
// next source is tried.
//
// Only a logo the publisher uploaded to Docker's own logo store is accepted. The same
// answers also point at GitHub avatars and Gravatars, which can be a person's face, so
// those are ignored here (GitHub has its own, stricter, rule). The match must be named
// exactly like the app and come from an official or verified publisher, or be widely
// used, so a look-alike image does not lend its logo.

const (
	dockerLogoHost  = "djeqr6to3dedg.cloudfront.net" // Docker's logo store
	dockerLogoPath  = "/repo-logos/"
	dockerLogoStars = 100
)

func (c *Client) dockerLogoHost() string {
	if c.DockerLogoHost != "" {
		return c.DockerLogoHost
	}
	return dockerLogoHost
}

// DockerLogo returns the logo of the best exact-name image on Docker Hub.
func (c *Client) DockerLogo(ctx context.Context, name string) ([]byte, string, error) {
	if !safeSegment(name) || len(name) > 63 {
		return nil, "", errNoLogo
	}
	var answer struct {
		Results []struct {
			Name    string `json:"name"`
			Source  string `json:"source"`
			Stars   int    `json:"star_count"`
			Archive bool   `json:"archived"`
			Logo    struct {
				Large string `json:"large"`
			} `json:"logo_url"`
		} `json:"results"`
	}
	address := fmt.Sprintf("%s/api/search/v3/catalog/search?type=image&size=10&query=%s", c.dockerHub(), url.QueryEscape(name))
	if err := c.getJSONWith(ctx, address, map[string]string{"Search-Version": "v3"}, &answer); err != nil {
		return nil, "", err
	}
	best, stars := "", -1
	for _, r := range answer.Results {
		_, last, _ := strings.Cut(r.Name, "/")
		if last == "" {
			last = r.Name
		}
		trusted := r.Source != "" && r.Source != "community" || r.Stars >= dockerLogoStars
		if !strings.EqualFold(last, name) || r.Archive || !trusted || r.Logo.Large == "" || r.Stars <= stars {
			continue
		}
		best, stars = r.Logo.Large, r.Stars
	}
	if best == "" {
		return nil, "", errNoLogo
	}
	logo, err := url.Parse(best)
	if err != nil || logo.Host != c.dockerLogoHost() || !strings.HasPrefix(logo.Path, dockerLogoPath) ||
		(logo.Scheme != "https" && c.DockerLogoHost == "") {
		return nil, "", errNoLogo
	}
	return c.fetchImage(ctx, logo.String())
}
