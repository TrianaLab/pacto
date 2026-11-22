// Package gitexport opens a pull request that commits a declaration's
// pacto.yaml and pacto.inferred.yaml, with the person's own Git token, in the
// person's own process. Only the CLI and the stdio MCP server use it: Pacto
// never holds a Git credential of its own. Every Git host is reached through
// its REST API over net/http.
package gitexport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

// Kind is a Git host's API.
type Kind string

// KindGitHub is GitHub, github.com or an Enterprise Server.
const KindGitHub Kind = "github"

// The refusal codes.
const (
	CodeGitHostUnknown    = "GitHostUnknown"
	CodeGitTokenMissing   = "GitTokenMissing"
	CodePullRequestFailed = "PullRequestFailed"
)

// Error is a refusal: a stable code and what happened. It never holds the
// token.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }

func failed(format string, a ...any) *Error {
	return &Error{Code: CodePullRequestFailed, Message: fmt.Sprintf(format, a...)}
}

// Declaration is what the pull request commits: the service, its version or
// draft tag, the bundle (repo@digest), declared or draft, who signed (empty
// for an unsigned draft), for whom a draft was written and the rung that
// found the repository.
type Declaration struct {
	Service, Tag, Bundle, State, Signer, OnBehalfOf, Rung string
}

// Request is one pull request: the host kind, the repository URL (a
// record's normalized https://host/path), the folder the files go in, the
// files by name, the token and whether a person named this repository
// (a public repository needs that). Transport carries the requests (nil is
// http.DefaultTransport).
type Request struct {
	Kind        Kind
	URL, Folder string
	Files       map[string][]byte
	Token       string
	Confirmed   bool
	Declaration Declaration
	Transport   http.RoundTripper
}

// Opened is the pull request opened: its URL, the token's own login and
// the branch.
type Opened struct{ URL, Login, Branch string }

// host is one Git host's API: the repository's default branch and
// visibility, one commit holding every file on a new branch, the pull
// request, and the login the token belongs to.
type host interface {
	repo(ctx context.Context) (defaultBranch string, private bool, err *Error)
	commit(ctx context.Context, branch, base string, files map[string][]byte, msg string) *Error
	pull(ctx context.Context, branch, base, title, body string) (string, *Error)
	login(ctx context.Context) (string, *Error)
}

// requestTimeout bounds every request to the host.
var requestTimeout = 15 * time.Second

// maxBody bounds a file committed and a body read from the host.
const maxBody = 1 << 20

// errRedirect stops every redirect: Go keeps Authorization on a same-host
// or https to http redirect.
var errRedirect = errors.New("redirect refused")

// Branch is the branch a declaration's pull request is opened from.
func Branch(service, tag string) string { return "pacto/declare-" + service + "-" + tag }

// Open commits r's files into r.Folder on a new branch from the default
// branch, in one commit, and opens the pull request, as the token's user.
func Open(ctx context.Context, r Request) (*Opened, *Error) {
	u, err := url.Parse(r.URL)
	if err != nil || u.Host == "" {
		return nil, failed("%q is not a repository URL", r.URL)
	}
	if e := plainHTTP(u); e != nil {
		return nil, e
	}
	files := map[string][]byte{}
	for name, b := range r.Files {
		if len(b) > maxBody {
			return nil, failed("%s is over 1 MiB", name)
		}
		files[path.Join(r.Folder, name)] = b
	}
	c := &client{token: r.Token, auth: "Bearer " + r.Token,
		http: &http.Client{Transport: r.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errRedirect }}}
	h := newGitHub(u, c)
	return open(ctx, h, r, files)
}

// open runs h's sequence: the login first, so a token that reads nothing
// fails before any write.
func open(ctx context.Context, h host, r Request, files map[string][]byte) (*Opened, *Error) {
	login, e := h.login(ctx)
	if e != nil {
		return nil, e
	}
	def, private, e := h.repo(ctx)
	if e != nil {
		return nil, e
	}
	if !private && !r.Confirmed {
		return nil, failed("the repository is public: confirm with --repository %s", r.URL)
	}
	d := r.Declaration
	branch, title := Branch(d.Service, d.Tag), "chore(pacto): declare "+d.Service+" "+d.Tag
	if e := h.commit(ctx, branch, def, files, title); e != nil {
		return nil, e
	}
	pr, e := h.pull(ctx, branch, def, title, prBody(d))
	if e != nil {
		return nil, e
	}
	return &Opened{URL: pr, Login: login, Branch: branch}, nil
}

// plainHTTP refuses an http:// repository off the loopback host: the token
// would travel in clear.
func plainHTTP(u *url.URL) *Error {
	if u.Scheme != "http" {
		return nil
	}
	h := u.Hostname()
	if ip := net.ParseIP(h); h == "localhost" || (ip != nil && ip.IsLoopback()) {
		return nil
	}
	return failed("plain HTTP would send your token in clear: use https://%s", strings.TrimPrefix(u.String(), "http://"))
}

// branchExists is the refusal for a branch a previous run left.
func branchExists(branch string) *Error {
	return failed("branch %s exists: a pull request for this declaration may be open", branch)
}

// prBody is the pull request's description. Every value that does not come
// from Pacto sits in a code span, so it cannot mention people or plant links.
func prBody(d Declaration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Declares %s %s, published as %s.\n\n", code(d.Service), code(d.Tag), code(d.Bundle))
	fmt.Fprintf(&b, "- State: %s\n", d.State)
	signer := "unsigned draft, proposer asserted"
	if d.Signer != "" {
		signer = code(d.Signer)
	}
	fmt.Fprintf(&b, "- Signed by: %s\n", signer)
	if d.OnBehalfOf != "" {
		fmt.Fprintf(&b, "- On behalf of: %s\n", code(d.OnBehalfOf))
	}
	fmt.Fprintf(&b, "- Repository found by: %s\n\n", code(d.Rung))
	b.WriteString("CI recomputes pacto.inferred.yaml on its next publish; declared values travel through the registry.\n")
	return b.String()
}

// code is s as a code span: backticks and line breaks dropped, at most 200
// runes.
func code(s string) string {
	s = strings.NewReplacer("`", "", "\n", " ", "\r", " ").Replace(s)
	return "`" + capRunes(s, 200) + "`"
}

func capRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// client sends the host's requests with the token, one bounded request at a
// time, and turns every failure into a refusal that never holds the token.
type client struct {
	http        *http.Client
	token, auth string
}

// do sends one request with a JSON body (when body is set) and decodes a
// 2xx answer into out (when set). It returns the status for the caller to
// read a conflict from.
func (c *client) do(ctx context.Context, method, rawURL string, body, out any) (int, *Error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body) // ponytail: maps and structs of strings always marshal
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return 0, failed("building %s %s: %v", method, rawURL, err)
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	switch {
	case errors.Is(err, errRedirect):
		return 0, failed("the host redirected; pass the final URL with --repository")
	case errors.Is(err, context.DeadlineExceeded):
		return 0, failed("the host did not answer %s %s within %s", method, req.URL.Path, requestTimeout)
	case err != nil:
		return 0, failed("%s %s: %s", method, req.URL.Path, c.redact(err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if len(data) > maxBody {
		return resp.StatusCode, failed("the host answered %s %s with more than 1 MiB", method, req.URL.Path)
	}
	if resp.StatusCode >= 300 {
		return resp.StatusCode, failed("the host answered %s %s with %d: %s", method, req.URL.Path, resp.StatusCode, c.hostMessage(data))
	}
	if out != nil && json.Unmarshal(data, out) != nil {
		return resp.StatusCode, failed("the host answered %s %s with a body that is not JSON", method, req.URL.Path)
	}
	return resp.StatusCode, nil
}

// hostMessage is the host's own reason, at most 200 runes, with the token
// removed should the host echo it.
func (c *client) hostMessage(data []byte) string {
	var m struct {
		Message string `json:"message"`
	}
	msg := strings.TrimSpace(string(data))
	if json.Unmarshal(data, &m) == nil && m.Message != "" {
		msg = m.Message
	}
	return capRunes(c.redact(msg), 200)
}

func (c *client) redact(s string) string {
	if c.token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.token, "<token>")
}
