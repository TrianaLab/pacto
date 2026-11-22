package gitexport

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// tokenVar is an environment variable that holds a token for one host only.
type tokenVar struct{ name, host string }

// tokenVars are kind's token variables, each with the one host it is sent
// to.
func tokenVars(kind Kind, env func(string) string) []tokenVar {
	switch kind {
	case KindGitHub:
		return []tokenVar{{"GITHUB_TOKEN", "github.com"}, {"GH_TOKEN", "github.com"}, {"GH_ENTERPRISE_TOKEN", env("GH_HOST")}}
	}
	return nil
}

// Token is the person's own token for repoURL's host and where it comes
// from: the host's own environment variable, used only when the URL's host
// is the one that variable names, else git's credential helper for that
// host (runHelper), else a refusal naming the variables. So a repository
// URL a GitOps object or an image label names never receives a token meant
// for another host.
func Token(ctx context.Context, kind Kind, repoURL string, env func(string) string,
	runHelper func(ctx context.Context, stdin string) (string, error)) (token, source string, e *Error) {
	u, _ := url.Parse(repoURL) // ponytail: Detect parsed it
	vars := tokenVars(kind, env)
	var names []string
	for _, v := range vars {
		names = append(names, v.name)
		if t := env(v.name); t != "" && v.host != "" && v.host == u.Host {
			return t, "your " + v.name, nil
		}
	}
	if t := helperToken(ctx, u, runHelper); t != "" {
		return t, "your git credential helper", nil
	}
	return "", "", &Error{Code: CodeGitTokenMissing, Message: "no Git token for " + u.Host + ": set " + strings.Join(names, " or ") +
		" for that host, or store a token in a git credential helper"}
}

// helperToken asks the credential helper for u's host and path and keeps
// only the password. Any failure means no helper.
func helperToken(ctx context.Context, u *url.URL, runHelper func(context.Context, string) (string, error)) string {
	out, err := runHelper(ctx, "protocol="+u.Scheme+"\nhost="+u.Host+"\npath="+strings.TrimPrefix(u.Path, "/")+"\n\n")
	if err != nil {
		return ""
	}
	for line := range strings.Lines(out) {
		if v, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "password="); ok {
			return v
		}
	}
	return ""
}

// credentialFillArgs asks git's credential helper for a credential and
// never prompts: it never runs approve, store or reject.
var credentialFillArgs = []string{"-c", "credential.interactive=never", "credential", "fill"}

// helperTimeout bounds the credential helper.
var helperTimeout = 10 * time.Second

// CredentialFill runs git credential fill with stdin, under helperTimeout,
// in an environment where neither git nor a credential manager can open a
// prompt or a login window. A missing git binary is an error, read as no
// helper.
func CredentialFill(ctx context.Context, stdin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, helperTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", credentialFillArgs...)
	cmd.Env = helperEnv(os.Environ())
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	return string(out), err
}

// helperEnv is environ without the askpass programs, with prompts off.
func helperEnv(environ []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "GIT_ASKPASS", "SSH_ASKPASS", "GIT_TERMINAL_PROMPT", "GCM_INTERACTIVE":
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
}
