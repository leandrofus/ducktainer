package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

var registryHTTP = &http.Client{Timeout: 8 * time.Second}

func registryGet(ctx context.Context, u string, hdr http.Header, user, pass string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	return registryHTTP.Do(req)
}

func registryJSON(resp *http.Response, out any) error {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry answered %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

// registryTags lists the tags of an image reference. Only Docker Hub and the
// registries configured in Settings are queried, so this cannot be used to
// reach arbitrary hosts.
func (s *Server) registryTags(w http.ResponseWriter, r *http.Request) {
	host, repo, _ := splitImage(strings.TrimSpace(r.URL.Query().Get("image")))
	if repo == "" {
		writeErr(w, badRequest("enter the image"))
		return
	}
	tags := []string{}
	var err error
	if host == "docker.io" {
		tags, err = hubTags(r.Context(), strings.TrimPrefix(repo, "docker.io/"))
	} else {
		var user, pass string
		var found bool
		for _, reg := range s.Cfg.Snapshot().Registry {
			server := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(reg.Server, "https://"), "http://"), "/")
			if server == host {
				user, pass, found = reg.Username, reg.Password, true
				break
			}
		}
		if !found {
			writeJSON(w, map[string]any{"tags": tags})
			return
		}
		tags, err = v2Tags(r.Context(), host, strings.TrimPrefix(repo, host+"/"), user, pass)
	}
	if err != nil {
		// suggestions are a convenience: an unreachable registry is not an error
		tags = []string{}
	}
	writeJSON(w, map[string]any{"tags": tags})
}

func hubTags(ctx context.Context, repo string) ([]string, error) {
	if !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}
	resp, err := registryGet(ctx, "https://hub.docker.com/v2/repositories/"+repo+"/tags?page_size=50&ordering=last_updated", nil, "", "")
	if err != nil {
		return nil, err
	}
	var out struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	}
	if err := registryJSON(resp, &out); err != nil {
		return nil, err
	}
	tags := make([]string, 0, len(out.Results))
	for _, t := range out.Results {
		tags = append(tags, t.Name)
	}
	return tags, nil
}

// v2Tags uses the registry v2 API, following the Bearer token challenge.
func v2Tags(ctx context.Context, host, repo, user, pass string) ([]string, error) {
	u := "https://" + host + "/v2/" + repo + "/tags/list"
	resp, err := registryGet(ctx, u, nil, user, pass)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("Www-Authenticate")
		resp.Body.Close()
		token, err := bearerToken(ctx, challenge, user, pass)
		if err != nil {
			return nil, err
		}
		resp, err = registryGet(ctx, u, http.Header{"Authorization": {"Bearer " + token}}, "", "")
		if err != nil {
			return nil, err
		}
	}
	var out struct {
		Tags []string `json:"tags"`
	}
	if err := registryJSON(resp, &out); err != nil {
		return nil, err
	}
	// the API lists alphabetically: newest-looking (highest) first, capped
	sort.Sort(sort.Reverse(sort.StringSlice(out.Tags)))
	if len(out.Tags) > 100 {
		out.Tags = out.Tags[:100]
	}
	return out.Tags, nil
}

func bearerToken(ctx context.Context, challenge, user, pass string) (string, error) {
	rest, ok := strings.CutPrefix(challenge, "Bearer ")
	if !ok {
		return "", fmt.Errorf("unsupported registry authentication")
	}
	params := map[string]string{}
	for _, part := range strings.Split(rest, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		params[k] = strings.Trim(v, `"`)
	}
	realm, err := url.Parse(params["realm"])
	if err != nil || realm.Scheme != "https" || realm.Host == "" {
		return "", fmt.Errorf("invalid registry realm")
	}
	q := realm.Query()
	if params["service"] != "" {
		q.Set("service", params["service"])
	}
	if params["scope"] != "" {
		q.Set("scope", params["scope"])
	}
	realm.RawQuery = q.Encode()
	resp, err := registryGet(ctx, realm.String(), nil, user, pass)
	if err != nil {
		return "", err
	}
	var out struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := registryJSON(resp, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		out.Token = out.AccessToken
	}
	return out.Token, nil
}

// registrySearch looks repositories up on Docker Hub.
func (s *Server) registrySearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	names := []string{}
	if q != "" {
		resp, err := registryGet(r.Context(), "https://hub.docker.com/v2/search/repositories/?page_size=10&query="+url.QueryEscape(q), nil, "", "")
		if err == nil {
			var out struct {
				Results []struct {
					Name string `json:"repo_name"`
				} `json:"results"`
			}
			if registryJSON(resp, &out) == nil {
				for _, x := range out.Results {
					names = append(names, x.Name)
				}
			}
		}
	}
	writeJSON(w, map[string]any{"repos": names})
}
