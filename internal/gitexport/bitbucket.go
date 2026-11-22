package gitexport

import (
	"bytes"
	"context"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// bitbucket is Bitbucket Cloud's REST API 2.0 (api.bitbucket.org).
type bitbucket struct {
	c            *client
	api, repoAPI string // repoAPI: <api>/repositories/<workspace>/<slug>
}

func newBitbucket(u *url.URL, c *client) *bitbucket {
	api := "https://api.bitbucket.org/2.0"
	return &bitbucket{c: c, api: api, repoAPI: api + "/repositories/" + strings.Trim(u.Path, "/")}
}

// login is the token's user. A repository, project or workspace access
// token has no user and cannot read /user (403): its commits are made by the
// token's own bot account, so that is the login.
func (b *bitbucket) login(ctx context.Context) (string, *Error) {
	var u struct {
		Username string `json:"username"`
	}
	status, e := b.c.do(ctx, http.MethodGet, b.api+"/user", nil, &u)
	if status == http.StatusForbidden {
		return "a Bitbucket access token", nil
	}
	return u.Username, e
}

func (b *bitbucket) repo(ctx context.Context) (string, bool, *Error) {
	var r struct {
		MainBranch struct {
			Name string `json:"name"`
		} `json:"mainbranch"`
		IsPrivate bool `json:"is_private"`
	}
	_, e := b.c.do(ctx, http.MethodGet, b.repoAPI, nil, &r)
	return r.MainBranch.Name, r.IsPrivate, e
}

// commit posts every file in one multipart /src request: one commit on a
// new branch whose parent is base's head.
func (b *bitbucket) commit(ctx context.Context, branch, base string, files map[string][]byte, msg string) *Error {
	var ref struct {
		Target struct {
			Hash string `json:"hash"`
		} `json:"target"`
	}
	if _, e := b.c.do(ctx, http.MethodGet, b.repoAPI+"/refs/branches/"+url.PathEscape(base), nil, &ref); e != nil {
		return e
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	// ponytail: writes to a bytes.Buffer never fail
	for _, kv := range [][2]string{{"message", msg}, {"branch", branch}, {"parents", ref.Target.Hash}} {
		_ = w.WriteField(kv[0], kv[1])
	}
	for _, p := range slices.Sorted(maps.Keys(files)) {
		_ = w.WriteField(p, string(files[p]))
	}
	_ = w.Close()
	status, e := b.c.send(ctx, http.MethodPost, b.repoAPI+"/src", w.FormDataContentType(), buf.Bytes(), nil)
	if status == http.StatusConflict || status == http.StatusUnprocessableEntity {
		return branchExists(branch)
	}
	return e
}

func (b *bitbucket) pull(ctx context.Context, branch, base, title, body string) (string, *Error) {
	var pr struct {
		Links struct {
			HTML struct {
				Href string `json:"href"`
			} `json:"html"`
		} `json:"links"`
	}
	req := map[string]any{"title": title, "description": body,
		"source": map[string]any{"branch": map[string]string{"name": branch}}, "destination": map[string]any{"branch": map[string]string{"name": base}}}
	_, e := b.c.do(ctx, http.MethodPost, b.repoAPI+"/pullrequests", req, &pr)
	return pr.Links.HTML.Href, e
}
