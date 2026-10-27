package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// ProviderEval is a suite runner: it runs one of the checks a version ships.
const ProviderEval = "eval"

// PredicateEval is the predicate of the statement a suite runner signs.
const PredicateEval = "delivery.pacto.trianalab.io/eval/v1"

// maxRunnerAnswer bounds a runner's signed answer: it is kept in the verdict,
// in Operation status and in the signed record.
const maxRunnerAnswer = 64 << 10

// EvalStatement is what a suite runner signs: an in-toto Statement v1 whose
// one subject is the version it ran the suite on.
type EvalStatement struct {
	Type          string     `json:"_type"`
	Subject       []Subject  `json:"subject"`
	PredicateType string     `json:"predicateType"`
	Predicate     EvalResult `json:"predicate"`
}

// EvalResult is delivery.pacto.trianalab.io/eval/v1: the operation that
// asked (the input's operationUID), the request it answers (the input's
// challenge), which suite ran (its sha256), where the run can be read again,
// how many cases passed and, when the runner can tell, the model that
// answered.
type EvalResult struct {
	Operation     string `json:"operation"`
	Challenge     string `json:"challenge"`
	Suite         string `json:"suite"`
	Run           string `json:"run"`
	Passed        int    `json:"passed"`
	Total         int    `json:"total"`
	ObservedModel string `json:"observedModel,omitempty"`
	Message       string `json:"message,omitempty"`
}

// Version is the digest of the statement's one subject, "" unless it has
// exactly one with a sha256.
func (s EvalStatement) Version() string {
	if len(s.Subject) != 1 || s.Subject[0].Digest["sha256"] == "" {
		return ""
	}
	return "sha256:" + s.Subject[0].Digest["sha256"]
}

// OpenEvalAnswer checks a runner's answer is one DSSE-signed eval statement
// that verify accepts, and returns it with the runner that signed it.
func OpenEvalAnswer(bundle []byte, verify RunnerVerifier) (EvalStatement, string, error) {
	var st EvalStatement
	if _, err := openStatement(bundle, PredicateEval, &st); err != nil {
		return EvalStatement{}, "", err
	}
	runner, err := verify(bundle)
	if err != nil {
		return EvalStatement{}, "", fmt.Errorf("signature: %w", err)
	}
	return st, runner, nil
}

// CheckRunner checks again the signed answer an eval verdict of record p
// keeps (its runnerBundle citation), the way an auditor reads a record: the
// runner's signature under verify; that the runner answered this record's
// operation about the version it moved to (from, for a cause re-run before
// the move); that the verdict cites what the
// runner signed; and that its result is the one the signed counts give under
// the Stage check's minPassRate. It returns the runner that signed it.
func CheckRunner(v Verdict, p RecordPredicate, verify RunnerVerifier) (string, error) {
	raw := v.Citation["runnerBundle"]
	if raw == "" {
		return "", errors.New("the eval verdict keeps no answer signed by its runner")
	}
	st, runner, err := OpenEvalAnswer([]byte(raw), verify)
	if err != nil {
		return "", err
	}
	if err := citesSigned(v, p, st, runner); err != nil {
		return runner, err
	}
	a := st.Predicate
	params, why := checkParams(v, p)
	if why != "" {
		return runner, errors.New(why)
	}
	rate, why := passRate(params)
	if why != "" {
		return runner, errors.New(why)
	}
	if a.Total <= 0 || a.Passed < 0 || a.Passed > a.Total {
		return runner, fmt.Errorf("%s counted %d of %d cases passed, which no verdict can rest on", runner, a.Passed, a.Total)
	}
	want := rateResult(a, rate)
	if params["requireObservedModel"] == "true" && a.ObservedModel == "" {
		want = worse(want, Unknown)
	}
	// The record does not keep the contract's model versions, so a model the
	// runner named may only have made the result worse, never better.
	if v.Result != want && (a.ObservedModel == "" || worse(v.Result, want) != v.Result) {
		return runner, fmt.Errorf("the verdict says %s, but %d of %d cases passed under minPassRate %s give %s",
			v.Result, a.Passed, a.Total, strconv.FormatFloat(rate, 'g', 10, 64), want)
	}
	return runner, nil
}

// citesSigned checks that runner answered record p's operation about the
// version it moved to (from, for a cause re-run before the move) and that the
// verdict v cites what the runner signed.
func citesSigned(v Verdict, p RecordPredicate, st EvalStatement, runner string) error {
	a, about, what := st.Predicate, "", "moved to"
	if p.Intent.Params != nil {
		about = p.Intent.Params.To
		if v.Phase == "pre" { // a cited cause re-run before the move, on the version it moved from
			about, what = p.Intent.Params.From, "moved from"
		}
	}
	switch {
	case a.Operation != p.Intent.UID:
		return fmt.Errorf("%s signed an answer for operation %q, but this record is operation %q", runner, a.Operation, p.Intent.UID)
	case st.Version() != about:
		return fmt.Errorf("%s ran the suite on %q, but this record %s %q", runner, st.Version(), what, about)
	}
	for k, signed := range map[string]string{"subject": st.Version(), "operation": a.Operation, "suite": a.Suite, "run": a.Run,
		"passed": strconv.Itoa(a.Passed), "total": strconv.Itoa(a.Total), "observedModel": a.ObservedModel} {
		if v.Citation[k] != signed {
			return fmt.Errorf("the verdict cites %s %q, but %s signed %q", k, v.Citation[k], runner, signed)
		}
	}
	return nil
}

// checkParams are the params the eval verdict v ran with: the Stage check of
// its name in the policy the recorder copied, or, in a record that keeps no
// policy, the params the verdict cites. why says the policy has no such check.
func checkParams(v Verdict, p RecordPredicate) (params map[string]string, why string) {
	if p.Policy == nil {
		_ = json.Unmarshal([]byte(v.Citation["params"]), &params) // none read is the defaults
		return params, ""
	}
	if i := slices.IndexFunc(p.Policy.Gates.Verify.Checks, func(c GateCheck) bool { return c.Name == v.Gate }); i >= 0 {
		return providerParams(p.Policy.Gates.Verify.Checks[i]), ""
	}
	return nil, "the record's Stage runs no check named " + v.Gate + ", so its minPassRate cannot be read"
}

// passRate is the minPassRate param, 1 (every case passes) when unset, or why
// it is not a number above 0 and up to 1.
func passRate(params map[string]string) (float64, string) {
	s, ok := params["minPassRate"]
	if !ok {
		return 1, ""
	}
	r, err := strconv.ParseFloat(s, 64)
	if err != nil || !(r > 0 && r <= 1) { // the negation also refuses NaN
		return 0, fmt.Sprintf("minPassRate %q is not a number above 0 and up to 1", s)
	}
	return r, ""
}

// rateResult is Pass when a's pass rate reaches rate, else Fail.
func rateResult(a EvalResult, rate float64) Result {
	// Divide, never multiply: 7/100 rounds to the parsed 0.07, 0.07*100 does not round to 7.
	if float64(a.Passed)/float64(a.Total) >= rate {
		return Pass
	}
	return Fail
}

// EvalVerdicts are the verdicts of record p that an eval runner must have
// signed: a Pass or Fail whose Stage check names a suite, pins one or asks
// for the model, whose provider is one of providers (the runner keys'
// providers in the auditor's trust config), or whose citation names a suite
// or a runner. An Unknown has no answer to keep.
func EvalVerdicts(p RecordPredicate, providers []string) []Verdict {
	var checks []GateCheck
	if p.Policy != nil {
		checks = p.Policy.Gates.Verify.Checks
	}
	var out []Verdict
	for _, v := range p.Verdicts {
		if v.Result != Pass && v.Result != Fail {
			continue
		}
		eval := v.Citation["runnerBundle"] != "" || v.Citation["runner"] != "" || v.Citation["suite"] != "" || v.Citation["suiteName"] != ""
		if i := slices.IndexFunc(checks, func(c GateCheck) bool { return c.Name == v.Gate }); i >= 0 {
			c := checks[i]
			eval = eval || c.Suite != "" || c.SuiteDigest != "" || c.RequireObservedModel || slices.Contains(providers, c.Provider)
		}
		if eval {
			out = append(out, v)
		}
	}
	return out
}

// Eval asks a suite runner to run one of the checks the version under check
// ships (the contract's checks[]): the one the Stage check's suite names, or the only
// one when the contract ships exactly one. The runner only counts; the Stage
// decides with the minPassRate param (default 1: every case passes). The
// runner gets the same {"input", "params"} as a Webhook, with the input also
// naming the contract the version ships and the suite in it, by name, path
// and sha256 ({"contract", "suite": {"name", "path", "digest"}}). It answers
// with a Sigstore bundle: a DSSE envelope over an EvalStatement, signed by a
// runner key the provider trusts. An answer nobody trusted signed, about
// another version or another suite, with no cases or with no run to cite is
// Unknown, never Pass or Fail: whoever ran the exam must sign its result, and
// a verdict on a suite nobody can tie to the bundle verifies nothing. The
// verdict keeps the signed answer, so the record carries the runner's own
// signature beside the recorder's.
type Eval struct {
	URL    string
	Client Doer
	// Verify checks the runner's signature. Without it every answer is
	// Unknown.
	Verify RunnerVerifier
	// Revision reads the contract the version under check ships. Without
	// it, or when the contract cannot be read, the gate answers Unknown.
	Revision func(context.Context, GateInput) Revision
}

// Check implements Gate.
func (e Eval) Check(ctx context.Context, in GateInput, params map[string]string) Verdict {
	minRate, why := passRate(params)
	if why != "" {
		return Verdict{Result: Unknown, Message: why}
	}
	if in.Challenge == "" {
		return Verdict{Result: Unknown, Message: "the check carries no challenge for the runner to answer, so an old answer could be replayed"}
	}
	models, msg := e.suite(ctx, &in, params["suite"])
	if msg != "" {
		return Verdict{Result: Unknown, Message: msg}
	}
	if pin := params["suiteDigest"]; pin != "" && pin != in.Suite.Digest {
		return Verdict{Result: Unknown, Message: pinMismatch(in.Suite, pin)}
	}
	if e.Verify == nil {
		return Verdict{Result: Unknown, Message: "the provider trusts no runner key, so no answer can be checked"}
	}
	var raw json.RawMessage
	if err := postJSON(ctx, e.Client, e.URL, map[string]any{"input": in, "params": params}, &raw); err != nil {
		return Verdict{Result: Unknown, Message: err.Error()}
	}
	var signed bytes.Buffer
	_ = json.Compact(&signed, raw) // postJSON decoded it, so it is JSON
	if signed.Len() > maxRunnerAnswer {
		return Verdict{Result: Unknown, Message: fmt.Sprintf("the runner's answer is %d bytes, more than the %d a verdict keeps", signed.Len(), maxRunnerAnswer)}
	}
	st, runner, err := OpenEvalAnswer(signed.Bytes(), e.Verify)
	if err != nil {
		return Verdict{Result: Unknown, Message: "the runner's answer is not signed by a runner this provider trusts: " + err.Error()}
	}
	a, subject := st.Predicate, st.Version()
	if why := answerProblem(in, a, subject); why != "" {
		return Verdict{Result: Unknown, Message: why}
	}
	v := Verdict{Result: rateResult(a, minRate), Citation: map[string]string{
		"run": a.Run, "suite": a.Suite, "suiteName": in.Suite.Name, "suitePath": in.Suite.Path, "contract": in.Contract, "subject": subject,
		"passed": strconv.Itoa(a.Passed), "total": strconv.Itoa(a.Total), "runner": runner, "runnerBundle": signed.String(), "operation": a.Operation,
	}}
	v.Message = fmt.Sprintf("%d of %d cases passed, %s needed", a.Passed, a.Total, strconv.FormatFloat(minRate*100, 'g', 10, 64)+"%")
	if a.Message != "" {
		v.Message += ": " + a.Message
	}
	if a.ObservedModel != "" {
		v.Citation["observedModel"] = a.ObservedModel
	}
	if r, why := modelCheck(a.ObservedModel, models, params["requireObservedModel"] == "true"); why != "" {
		v.Result, v.Message = worse(v.Result, r), v.Message+"; "+why
	}
	return v
}

// answerProblem says why the runner's answer a about subject does not answer
// the check in, or is empty when it does.
func answerProblem(in GateInput, a EvalResult, subject string) string {
	switch {
	case !slices.Contains(in.Subjects, subject):
		return fmt.Sprintf("the runner ran the suite on %q, not on the version under check", subject)
	case a.Operation != in.OperationUID:
		return fmt.Sprintf("the runner answered operation %q, not %q", a.Operation, in.OperationUID)
	case a.Challenge != in.Challenge:
		return "the runner's answer does not echo this check's challenge: it answers another request, so it may be an old answer replayed"
	case a.Run == "":
		return "the runner named no run to cite"
	case a.Suite != in.Suite.Digest:
		return fmt.Sprintf("the runner ran the suite %q, not %s, the %s the version ships", a.Suite, in.Suite.Digest, in.Suite.Path)
	case a.Total <= 0 || a.Passed < 0 || a.Passed > a.Total:
		return fmt.Sprintf("the runner counted %d of %d cases passed", a.Passed, a.Total)
	}
	return ""
}

// modelCheck compares the model the runner saw answer with the versions of
// the contract's external dependencies. A model none of them names fails; a
// Stage that asks which model answered (required) gets Unknown when the
// runner did not say or the contract names no version to compare with.
func modelCheck(observed string, models []string, required bool) (Result, string) {
	switch {
	case observed != "" && len(models) > 0 && !slices.Contains(models, observed):
		return Fail, "the runner saw the model " + observed + " answer, but the contract declares " + strings.Join(models, ", ")
	case !required || observed != "" && len(models) > 0:
		return Pass, ""
	case observed == "":
		return Unknown, "the Stage asks which model answered, and the runner did not say"
	}
	return Unknown, "the Stage asks which model answered, but the contract declares no external dependency with a version to compare " + observed + " with"
}

// suite sets in.Contract and in.Suite to the contract the version under
// check ships and the suite in it the gate runs, or says why it cannot.
// pickSuite is the check a Stage check's suite name picks from the checks a
// contract ships: the one of that name, or with no name the only one. why
// says why there is none.
func pickSuite(checks []contract.Check, name, ref string) (c contract.Check, why string) {
	i := slices.IndexFunc(checks, func(c contract.Check) bool { return c.Name == name })
	switch {
	case name == "" && len(checks) == 1:
		i = 0
	case name == "" && len(checks) == 0:
		return c, "the contract " + ref + " ships no checks, so there is no suite to run"
	case name == "":
		return c, "the contract " + ref + " ships more than one check: name the one to run in the Stage check's suite"
	case i < 0:
		return c, "the Stage runs the suite " + name + ", but the contract " + ref + " ships no check of that name"
	}
	return checks[i], ""
}

func (e Eval) suite(ctx context.Context, in *GateInput, name string) (models []string, why string) {
	if e.Revision == nil || len(in.Subjects) == 0 {
		return nil, "the suite gate cannot read the contract the version ships"
	}
	rev := e.Revision(ctx, *in)
	if rev.Bundle == nil {
		return nil, "reading the contract the version ships: " + rev.Err
	}
	s, why := readSuite(rev, name)
	if why != "" {
		return nil, why
	}
	in.Contract, in.Suite = rev.ContractRef, s
	for _, d := range rev.Bundle.Contract.Dependencies { // readSuite found a check, so there is a contract
		if d.External && d.Version != "" {
			models = append(models, d.Version)
		}
	}
	return models, ""
}

// readSuite is the suite a Stage check's suite name picks from the checks
// rev's contract ships, with the sha256 of every file in its directory
// (contract.SuiteDigest), or why there is none.
func readSuite(rev Revision, name string) (*Suite, string) {
	var checks []contract.Check
	if rev.Bundle.Contract != nil {
		checks = rev.Bundle.Contract.Checks
	}
	c, why := pickSuite(checks, name, rev.ContractRef)
	if why != "" {
		return nil, why
	}
	if rev.Bundle.FS == nil {
		return nil, "the bundle " + rev.ContractRef + " carries no files to read the suite " + c.File + " from"
	}
	digest, err := contract.SuiteDigest(rev.Bundle.FS, c.File)
	if err != nil {
		return nil, fmt.Sprintf("reading the suite %s in %s: %v", c.File, rev.ContractRef, err)
	}
	return &Suite{Name: c.Name, Path: c.File, Digest: digest}, ""
}

// pinMismatch says a version ships a suite whose digest is not the one the
// Stage pinned.
func pinMismatch(s *Suite, pin string) string {
	return "the Stage pins the suite " + s.Name + " to " + pin + ", but this version ships " + s.Digest +
		" in " + s.Path + ": the version changed the suite that judges it, so a person decides"
}
