package gitexport

import (
	"context"
	"encoding/base64"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// gitea is the Gitea (and Forgejo) REST API v1 on the repository's own
// host, Gitea 1.20 or later (the multi-file contents endpoint).
type gitea struct {
	c            *client
	api, repoAPI string // repoAPI: <api>/repos/<owner>/<repo>
}

func newGitea(u *url.URL, c *client) *gitea {
	api := u.Scheme + "://" + u.Host + "/api/v1"
	return &gitea{c: c, api: api, repoAPI: api + "/repos/" + strings.Trim(u.Path, "/")}
}

func (g *gitea) login(ctx context.Context) (string, *Error) {
	var u struct {
		Login string `json:"login"`
	}
	_, e := g.c.do(ctx, http.MethodGet, g.api+"/user", nil, &u)
	return u.Login, e
}

func (g *gitea) repo(ctx context.Context) (string, bool, *Error) {
	var r struct {
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
		Internal      bool   `json:"internal"`
	}
	_, e := g.c.do(ctx, http.MethodGet, g.repoAPI, nil, &r)
	return r.DefaultBranch, r.Private || r.Internal, e
}

// commit makes one commit on a new branch from base, creating each file
// base lacks and updating each file it has (by its blob sha).
func (g *gitea) commit(ctx context.Context, branch, base string, files map[string][]byte, msg string) *Error {
	var ops []map[string]string
	for _, p := range slices.Sorted(maps.Keys(files)) {
		var cur struct {
			SHA string `json:"sha"`
		}
		status, e := g.c.do(ctx, http.MethodGet, g.repoAPI+"/contents/"+escapeSegments(p)+"?ref="+url.QueryEscape(base), nil, &cur)
		op := map[string]string{"operation": "update", "path": p, "content": base64.StdEncoding.EncodeToString(files[p]), "sha": cur.SHA}
		switch {
		case status == http.StatusNotFound:
			op["operation"] = "create"
			delete(op, "sha")
		case e != nil:
			return e
		}
		ops = append(ops, op)
	}
	body := map[string]any{"branch": base, "new_branch": branch, "message": msg, "files": ops}
	status, e := g.c.do(ctx, http.MethodPost, g.repoAPI+"/contents", body, nil)
	if status == http.StatusConflict || status == http.StatusUnprocessableEntity {
		return branchExists(branch)
	}
	return e
}

func (g *gitea) pull(ctx context.Context, branch, base, title, body string) (string, *Error) {
	var pr struct {
		URL string `json:"html_url"`
	}
	_, e := g.c.do(ctx, http.MethodPost, g.repoAPI+"/pulls", map[string]string{"head": branch, "base": base, "title": title, "body": body}, &pr)
	return pr.URL, e
}

// escapeSegments escapes each segment of a slash path.
func escapeSegments(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}
