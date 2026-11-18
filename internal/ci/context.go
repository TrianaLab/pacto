// Package ci plans every service of a repository for a CI run: it detects the
// CI platform and the registry, each with its source, and renders the plan.
// It reads the environment only through the getenv Detect is given.
package ci

// Value is a detected value and where it came from.
type Value struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}

// String is "<value> (from <source>)", or "none" when nothing was detected.
func (v Value) String() string {
	if v.Value == "" {
		return "none"
	}
	return v.Value + " (from " + v.Source + ")"
}

// Context is what the CI platform says about the run. Owner, ServerURL and
// RegistryImage are the raw values ResolveRegistry reads.
type Context struct {
	Platform      string // "github", "gitlab" or "" (none)
	Base          Value  // context only: it decides nothing
	OIDC          bool   // a token could be requested; plan mode never does
	Summary       string // GitHub's step summary file
	ArtifactDir   string // GitLab's project directory, where pacto-plan.md goes
	Owner         string
	ServerURL     string
	RegistryImage string
}

// ciVariables are the only variables Detect reads.
var ciVariables = []string{
	"GITHUB_ACTIONS", "GITHUB_REPOSITORY_OWNER", "GITHUB_SERVER_URL", "GITHUB_BASE_REF", "GITHUB_STEP_SUMMARY",
	"ACTIONS_ID_TOKEN_REQUEST_URL", "GITLAB_CI", "CI_REGISTRY_IMAGE", "CI_MERGE_REQUEST_DIFF_BASE_SHA", "CI_PROJECT_DIR",
}

// Detect reads the CI context from getenv; nil is an empty environment, so
// only a caller that passes os.Getenv ever sees the runner's variables. It
// reads every listed variable once and nothing else: no person's name.
func Detect(getenv func(string) string) Context {
	env := make(map[string]string, len(ciVariables))
	for _, k := range ciVariables {
		if getenv != nil {
			env[k] = getenv(k)
		}
	}
	switch {
	case env["GITHUB_ACTIONS"] == "true":
		return Context{
			Platform: "github", Base: contextOnly(env["GITHUB_BASE_REF"], "GITHUB_BASE_REF"),
			OIDC: env["ACTIONS_ID_TOKEN_REQUEST_URL"] != "", Summary: env["GITHUB_STEP_SUMMARY"],
			Owner: env["GITHUB_REPOSITORY_OWNER"], ServerURL: env["GITHUB_SERVER_URL"],
		}
	case env["GITLAB_CI"] == "true":
		sha := env["CI_MERGE_REQUEST_DIFF_BASE_SHA"]
		return Context{
			Platform: "gitlab", Base: contextOnly(sha[:min(7, len(sha))], "CI_MERGE_REQUEST_DIFF_BASE_SHA"),
			ArtifactDir: env["CI_PROJECT_DIR"], RegistryImage: env["CI_REGISTRY_IMAGE"],
		}
	}
	return Context{}
}

// contextOnly is the base value v from variable name, printed as context.
func contextOnly(v, name string) Value {
	if v == "" {
		return Value{}
	}
	return Value{Value: v, Source: name + ", context only"}
}
