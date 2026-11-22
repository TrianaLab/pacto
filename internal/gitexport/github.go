package gitexport

import (
	"context"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// github is GitHub's REST API: api.github.com for github.com, else an
// Enterprise Server's <host>/api/v3.
type github struct {
	c       *client
	repoAPI string // <api>/repos/<owner>/<repo>
	api     string
}

func newGitHub(u *url.URL, c *client) *github {
	api := u.Scheme + "://" + u.Host + "/api/v3"
	if u.Host == "github.com" {
		api = "https://api.github.com"
	}
	return &github{c: c, api: api, repoAPI: api + "/repos/" + strings.Trim(u.Path, "/")}
}

func (g *github) login(ctx context.Context) (string, *Error) {
	var u struct {
		Login string `json:"login"`
	}
	_, e := g.c.do(ctx, http.MethodGet, g.api+"/user", nil, &u)
	return u.Login, e
}

func (g *github) repo(ctx context.Context) (string, bool, *Error) {
	var r struct {
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
		Visibility    string `json:"visibility"`
	}
	_, e := g.c.do(ctx, http.MethodGet, g.repoAPI, nil, &r)
	// An Enterprise Server's internal repository is not public either.
	return r.DefaultBranch, r.Private || (r.Visibility != "" && r.Visibility != "public"), e
}

// commit makes one commit holding every file on top of base's head, then
// points the new branch at it.
func (g *github) commit(ctx context.Context, branch, base string, files map[string][]byte, msg string) *Error {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if _, e := g.c.do(ctx, http.MethodGet, g.repoAPI+"/git/ref/heads/"+base, nil, &ref); e != nil {
		return e
	}
	var head struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if _, e := g.c.do(ctx, http.MethodGet, g.repoAPI+"/git/commits/"+ref.Object.SHA, nil, &head); e != nil {
		return e
	}
	type entry struct {
		Path    string `json:"path"`
		Mode    string `json:"mode"`
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	var entries []entry
	for _, p := range slices.Sorted(maps.Keys(files)) {
		entries = append(entries, entry{Path: p, Mode: "100644", Type: "blob", Content: string(files[p])})
	}
	var tree, made struct {
		SHA string `json:"sha"`
	}
	if _, e := g.c.do(ctx, http.MethodPost, g.repoAPI+"/git/trees", map[string]any{"base_tree": head.Tree.SHA, "tree": entries}, &tree); e != nil {
		return e
	}
	body := map[string]any{"message": msg, "tree": tree.SHA, "parents": []string{ref.Object.SHA}}
	if _, e := g.c.do(ctx, http.MethodPost, g.repoAPI+"/git/commits", body, &made); e != nil {
		return e
	}
	status, e := g.c.do(ctx, http.MethodPost, g.repoAPI+"/git/refs", map[string]string{"ref": "refs/heads/" + branch, "sha": made.SHA}, nil)
	if status == http.StatusUnprocessableEntity || status == http.StatusConflict {
		return branchExists(branch)
	}
	return e
}

func (g *github) pull(ctx context.Context, branch, base, title, body string) (string, *Error) {
	var pr struct {
		URL string `json:"html_url"`
	}
	_, e := g.c.do(ctx, http.MethodPost, g.repoAPI+"/pulls", map[string]string{"title": title, "head": branch, "base": base, "body": body}, &pr)
	return pr.URL, e
}
