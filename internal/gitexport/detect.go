package gitexport

import "net/url"

// Detect is the Git host kind that serves repoURL: override when set, else
// from the URL's host (github.com, or $GH_HOST for an Enterprise Server).
func Detect(repoURL, override string, env func(string) string) (Kind, *Error) {
	switch override {
	case "":
	case string(KindGitHub):
		return KindGitHub, nil
	default:
		return "", &Error{Code: CodeGitHostUnknown, Message: "unknown Git host kind " + override + ": Pacto opens pull requests on github"}
	}
	u, err := url.Parse(repoURL)
	if err == nil && (u.Host == "github.com" || (u.Host != "" && u.Host == env("GH_HOST"))) {
		return KindGitHub, nil
	}
	return "", &Error{Code: CodeGitHostUnknown, Message: "Pacto cannot tell which Git host serves " + repoURL +
		": set GH_HOST to its host for a GitHub Enterprise Server"}
}
