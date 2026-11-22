package gitexport

import "net/url"

// kindsHint names every kind --git-host takes.
const kindsHint = "pass --git-host github|gitlab|bitbucket|gitea"

// Detect is the Git host kind that serves repoURL: override when set, else
// from the URL's host: github.com or $GH_HOST, gitlab.com or $GITLAB_HOST,
// bitbucket.org, codeberg.org or $GITEA_HOST.
func Detect(repoURL, override string, env func(string) string) (Kind, *Error) {
	switch k := Kind(override); k {
	case "":
	case KindGitHub, KindGitLab, KindBitbucket, KindGitea:
		return k, nil
	default:
		return "", &Error{Code: CodeGitHostUnknown, Message: "unknown Git host kind " + override + ": " + kindsHint}
	}
	u, err := url.Parse(repoURL)
	if err == nil && u.Host != "" {
		for _, c := range []struct {
			kind  Kind
			hosts []string
		}{
			{KindGitHub, []string{"github.com", env("GH_HOST")}},
			{KindGitLab, []string{"gitlab.com", env("GITLAB_HOST")}},
			{KindBitbucket, []string{"bitbucket.org"}},
			{KindGitea, []string{"codeberg.org", env("GITEA_HOST")}},
		} {
			for _, h := range c.hosts {
				if h == u.Host {
					return c.kind, nil
				}
			}
		}
	}
	return "", &Error{Code: CodeGitHostUnknown, Message: "Pacto cannot tell which Git host serves " + repoURL + ": " + kindsHint}
}
