package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// GateRuntime is the built-in claim gate: what the running version does
// against what its contract declares, as the node agent saw it.
const GateRuntime = "pacto.runtime"

// Window is the time a gate judges, with absolute bounds so a citation re-runs
// over exactly the same span.
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// GateInput is the gate protocol's input: one operation, one Stage and facet,
// the versions under check and the window to judge.
type GateInput struct {
	OperationUID string     `json:"operationUID"`
	Stage        StageRef   `json:"stage"`
	Service      ServiceRef `json:"service"`
	Facet        string     `json:"facet"`
	// Subjects are the digests under check: the version the operation applied.
	Subjects []string `json:"subjects"`
	// Version is the contract service.version of the version under check,
	// when the plan could read it.
	Version string `json:"version,omitempty"`
	Window  Window `json:"window"`
	// Contract is the digest-pinned contract the version ships and Suite the
	// check in it to run. Only the suite gate sets them, for its runner.
	Contract string `json:"contract,omitempty"`
	Suite    *Suite `json:"suite,omitempty"`
}

// Suite is one check a contract ships: its name, its file in the bundle and
// the sha256 of the file's bytes.
type Suite struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// Gate answers Pass, Fail or Unknown for one input, with a citation that can
// be re-run. A gate that cannot read what it needs answers Unknown.
type Gate interface {
	Check(ctx context.Context, in GateInput, params map[string]string) Verdict
}

// Provider types delivery.gates.providers may configure.
const (
	ProviderMira = "mira"
	ProviderHTTP = "http"
)

// ProviderConfig is one gate provider set in delivery.gates.providers. Its URL
// is operator configuration: a Stage only names a provider, and an agent can
// name nothing, so no request can point the promoter at a host of its choosing.
type ProviderConfig struct {
	Name string `json:"name"`
	Type string `json:"type"`
	URL  string `json:"url"`
	// AllowPlainHTTP lets a provider off loopback use http://. Nothing then
	// protects its answers in transit, so it is for test clusters only.
	AllowPlainHTTP bool `json:"allowPlainHTTP,omitempty"`
}

// ParseProviders decodes the --gate-providers flag (a JSON list).
func ParseProviders(s string) ([]ProviderConfig, error) {
	if s == "" {
		return nil, nil
	}
	var ps []ProviderConfig
	if err := json.Unmarshal([]byte(s), &ps); err != nil {
		return nil, fmt.Errorf("gate providers: %w", err)
	}
	return ps, nil
}

// NewGates builds the configured providers. Names starting with "pacto." are
// the built-in gates' and cannot be taken.
func NewGates(configs []ProviderConfig, client *http.Client) (map[string]Gate, error) {
	gates := map[string]Gate{}
	for _, c := range configs {
		u, err := url.Parse(c.URL)
		switch {
		case c.Name == "" || strings.HasPrefix(c.Name, "pacto."):
			return nil, fmt.Errorf("gate provider name %q: empty or reserved", c.Name)
		case gates[c.Name] != nil:
			return nil, fmt.Errorf("gate provider %q is configured twice", c.Name)
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
			return nil, fmt.Errorf("gate provider %q: url %q must be http(s)://host", c.Name, where(c.URL))
		case u.Scheme == "http" && !c.AllowPlainHTTP && !loopback(u.Hostname()):
			// A verdict lands in a signed record: whoever can forge the answer
			// in transit could get a Pass signed.
			return nil, fmt.Errorf("gate provider %q: url %q is plain http, so anyone on the network path could forge its verdicts: use https (http is allowed only on loopback, or with allowPlainHTTP on a test cluster)", c.Name, where(c.URL))
		case u.User != nil:
			// Verdict messages land in Operation status and signed records.
			return nil, fmt.Errorf("gate provider %q: the url must not carry a user or password", c.Name)
		}
		switch c.Type {
		case ProviderMira:
			gates[c.Name] = Mira{URL: c.URL, Client: client}
		case ProviderHTTP:
			gates[c.Name] = Webhook{URL: c.URL, Client: client}
		case ProviderEval:
			gates[c.Name] = Eval{URL: c.URL, Client: client}
		default:
			return nil, fmt.Errorf("gate provider %q: type %q is not mira, http or eval", c.Name, c.Type)
		}
	}
	return gates, nil
}

// loopback reports whether host is this machine.
func loopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || ip != nil && ip.IsLoopback()
}

// RunChecks runs a Stage's verify checks at once, so they take as long as
// the slowest one, not their sum. Every verdict cites its provider, the exact
// input and the params, so it can be run again. A Pass that cites nothing is
// Unknown: a verdict counts only if it can be re-run.
func RunChecks(ctx context.Context, gates map[string]Gate, checks []GateCheck, in GateInput) []Verdict {
	out := make([]Verdict, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Go(func() { out[i] = runCheck(ctx, gates, c, in) })
	}
	wg.Wait()
	return out
}

// CheckTimeout bounds one gate check, so a slow provider cannot stall the
// reconciles waiting on it. A check that runs out of time is Unknown, never Pass.
var CheckTimeout = time.Minute

// checkWithin runs one check, giving up on it after CheckTimeout even when the
// gate ignores its context. A gate that panics is Unknown: a bug in one
// provider must not take the whole promoter down.
func checkWithin(ctx context.Context, g Gate, in GateInput, params map[string]string) Verdict {
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	done := make(chan Verdict, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- Verdict{Result: Unknown, Message: fmt.Sprintf("the check failed: %v", r)}
			}
		}()
		done <- g.Check(ctx, in, params)
	}()
	select {
	case v := <-done:
		return v
	case <-ctx.Done():
		return Verdict{Result: Unknown, Message: fmt.Sprintf("the check did not answer within %s: %v", CheckTimeout, ctx.Err())}
	}
}

func runCheck(ctx context.Context, gates map[string]Gate, c GateCheck, in GateInput) Verdict {
	v := Verdict{Result: Unknown, Message: fmt.Sprintf("no gate provider named %q is configured", c.Provider)}
	// The suite and its pin reach the provider only from the typed fields,
	// the ones the pacto.suites pre-gate checked: such params are dropped.
	params := maps.Clone(c.Params)
	delete(params, "suite")
	delete(params, "suiteDigest")
	for k, val := range map[string]string{"suite": c.Suite, "suiteDigest": c.SuiteDigest} {
		if val == "" {
			continue
		}
		if params == nil {
			params = map[string]string{}
		}
		params[k] = val
	}
	if g := gates[c.Provider]; g != nil {
		v = checkWithin(ctx, g, in, params)
		if v.Result == Pass && len(v.Citation) == 0 {
			v.Result, v.Message = Unknown, "the gate passed but gave no citation to re-run: "+v.Message
		}
	}
	if v.Citation == nil {
		v.Citation = map[string]string{}
	}
	inJSON, _ := json.Marshal(in)
	pJSON, _ := json.Marshal(params)
	v.Citation["provider"], v.Citation["input"], v.Citation["params"] = c.Provider, string(inJSON), string(pJSON)
	v.Gate, v.Phase = c.Name, "verify"
	return v
}

// Worst is the combined result of verdicts: Fail over Unknown over Pass.
func Worst(vs []Verdict) Result {
	r := Pass
	for _, v := range vs {
		r = worse(r, v.Result)
	}
	return r
}

// Rerun runs a cited verdict again, the way the promoter re-checks a cause an
// agent cites. Only the cited window is taken from the citation, and it must
// lie inside base.Window (from when the version under check was applied until
// now): the gate, its params, the service and the versions under check are
// this Stage's own, so a citation cannot loosen a threshold, point at another
// service or borrow an incident from before this version ran. A gate this
// Stage does not run, or a citation that does not decode or fit, is Unknown.
func Rerun(ctx context.Context, gates map[string]Gate, checks []GateCheck, base GateInput, cited Verdict) Verdict {
	c := GateCheck{Name: cited.Gate, Provider: cited.Gate}
	if i := slices.IndexFunc(checks, func(c GateCheck) bool { return c.Name == cited.Gate }); i >= 0 {
		c = checks[i]
	} else if !strings.HasPrefix(cited.Gate, "pacto.") {
		return Verdict{Gate: cited.Gate, Phase: "verify", Result: Unknown, Message: "this Stage runs no gate named " + cited.Gate}
	}
	if raw := cited.Citation["input"]; raw != "" {
		var in GateInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			return Verdict{Gate: cited.Gate, Phase: "verify", Result: Unknown, Message: "the cited input does not decode: " + err.Error()}
		}
		if in.Window.From.Before(base.Window.From) || in.Window.To.After(base.Window.To) {
			return Verdict{Gate: cited.Gate, Phase: "verify", Result: Unknown, Message: fmt.Sprintf(
				"the cited window %s to %s is outside the time the version under check has run (%s to %s)",
				ts3339(in.Window.From), ts3339(in.Window.To), ts3339(base.Window.From), ts3339(base.Window.To))}
		}
		base.Window = in.Window
	}
	return runCheck(ctx, gates, c, base)
}

// maxAnswer bounds what the promoter reads from any gate provider.
const maxAnswer = 64 << 20

// postJSON posts body to u and decodes a 2xx answer into out, tolerating
// fields it does not know: a provider's answer is not a schema Pacto owns.
// Its errors name the provider by where(u) and never echo the answer's body:
// they end up in Operation status and signed records.
func postJSON(ctx context.Context, client *http.Client, u string, body, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("%s: the gate provider url does not parse", where(u))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err // its text repeats the url, query and all
	}
	if err != nil {
		return fmt.Errorf("%s did not answer: %w", where(u), err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s answered %d", where(u), resp.StatusCode)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s answered something that is not the expected JSON", where(u))
	}
	return nil
}

// where names a provider URL for messages: scheme, host and path, with no
// user, password or query.
func where(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return "the gate provider"
	}
	return p.Scheme + "://" + p.Host + p.Path
}

// Webhook is the plug-in format: any monitoring tool can be a gate behind a
// URL that takes {"input": GateInput, "params": {...}} and answers
// {"subject": "sha256:...", "result": "Pass|Fail|Unknown", "message": "...",
// "citation": {...}}. subject is the version it judged, one of the input's
// subjects: an answer about another version, or that names none, is Unknown.
type Webhook struct {
	URL    string
	Client *http.Client
}

// Check implements Gate.
func (w Webhook) Check(ctx context.Context, in GateInput, params map[string]string) Verdict {
	var a struct {
		Subject  string            `json:"subject"`
		Result   Result            `json:"result"`
		Message  string            `json:"message"`
		Citation map[string]string `json:"citation"`
	}
	err := postJSON(ctx, w.Client, w.URL, map[string]any{"input": in, "params": params}, &a)
	switch {
	case err != nil:
		return Verdict{Result: Unknown, Message: err.Error()}
	case a.Result != Pass && a.Result != Fail && a.Result != Unknown:
		return Verdict{Result: Unknown, Message: fmt.Sprintf("%s answered result %q", where(w.URL), a.Result)}
	case !slices.Contains(in.Subjects, a.Subject):
		return Verdict{Result: Unknown, Message: fmt.Sprintf("%s judged %q, not the version under check", where(w.URL), a.Subject)}
	}
	if a.Citation == nil {
		a.Citation = map[string]string{}
	}
	a.Citation["subject"] = a.Subject
	return Verdict{Result: a.Result, Message: a.Message, Citation: a.Citation}
}
