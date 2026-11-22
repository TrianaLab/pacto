package gitexport

import (
	"context"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// gitlab is GitLab's REST API v4 on the repository's own host. The project
// is addressed by its URL-encoded path, subgroups included.
type gitlab struct {
	c            *client
	api, project string // project: <api>/projects/<encoded path>
}

func newGitLab(u *url.URL, c *client) *gitlab {
	api := u.Scheme + "://" + u.Host + "/api/v4"
	return &gitlab{c: c, api: api, project: api + "/projects/" + url.PathEscape(strings.Trim(u.Path, "/"))}
}

func (g *gitlab) login(ctx context.Context) (string, *Error) {
	var u struct {
		Username string `json:"username"`
	}
	_, e := g.c.do(ctx, http.MethodGet, g.api+"/user", nil, &u)
	return u.Username, e
}

func (g *gitlab) repo(ctx context.Context) (string, bool, *Error) {
	var r struct {
		DefaultBranch string `json:"default_branch"`
		Visibility    string `json:"visibility"`
	}
	_, e := g.c.do(ctx, http.MethodGet, g.project, nil, &r)
	return r.DefaultBranch, r.Visibility != "public", e
}

// commit makes one commit on a new branch from base, creating each file
// base lacks and updating each file it has.
func (g *gitlab) commit(ctx context.Context, branch, base string, files map[string][]byte, msg string) *Error {
	var actions []map[string]string
	for _, p := range slices.Sorted(maps.Keys(files)) {
		status, e := g.c.send(ctx, http.MethodHead, g.project+"/repository/files/"+url.PathEscape(p)+"?ref="+url.QueryEscape(base), "", nil, nil)
		action := "update"
		switch {
		case status == http.StatusNotFound:
			action = "create"
		case e != nil:
			return e
		}
		actions = append(actions, map[string]string{"action": action, "file_path": p, "content": string(files[p])})
	}
	body := map[string]any{"branch": branch, "start_branch": base, "commit_message": msg, "actions": actions}
	status, e := g.c.do(ctx, http.MethodPost, g.project+"/repository/commits", body, nil)
	// GitLab answers an existing branch with 400 "A branch called ... already exists".
	if e != nil && (status == http.StatusConflict || status == http.StatusUnprocessableEntity || strings.Contains(e.Message, "already exists")) {
		return branchExists(branch)
	}
	return e
}

func (g *gitlab) pull(ctx context.Context, branch, base, title, body string) (string, *Error) {
	var mr struct {
		URL string `json:"web_url"`
	}
	_, e := g.c.do(ctx, http.MethodPost, g.project+"/merge_requests",
		map[string]string{"source_branch": branch, "target_branch": base, "title": title, "description": body}, &mr)
	return mr.URL, e
}
