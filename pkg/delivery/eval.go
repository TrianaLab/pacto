package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"slices"
	"strconv"
)

// ProviderEval is an eval runner: the gate an agent ships behind.
const ProviderEval = "eval"

// evalAnswer is what an eval runner answers: which version it ran, which
// suite, where the run can be read again and how many cases passed.
type evalAnswer struct {
	Subject string `json:"subject"`
	Suite   string `json:"suite"`
	Run     string `json:"run"`
	Passed  int    `json:"passed"`
	Total   int    `json:"total"`
	Message string `json:"message"`
}

// Eval asks an eval runner to run an agent's eval suite (the contract's
// agent.evals) on the version under check. The runner only counts; the Stage
// decides with the minPassRate param (default 1: every case passes). The
// runner gets the same {"input", "params"} as a Webhook, with the input also
// naming the contract the version ships and the suite in it, by path and
// sha256 ({"contract", "evals": {"path", "digest"}}), and answers
// {"subject", "suite", "run", "passed", "total", "message"} where suite is
// the digest of the suite it ran. An answer about another version or another
// suite, with no cases, or with no run to cite is Unknown, never Pass or Fail:
// a verdict on a suite nobody can tie to the bundle verifies nothing.
type Eval struct {
	URL    string
	Client *http.Client
	// Revision reads the contract the version under check ships. Without
	// it, or when the contract cannot be read, the gate answers Unknown.
	Revision func(context.Context, GateInput) Revision
}

// Check implements Gate.
func (e Eval) Check(ctx context.Context, in GateInput, params map[string]string) Verdict {
	minRate := 1.0
	if s, ok := params["minPassRate"]; ok {
		r, err := strconv.ParseFloat(s, 64)
		if err != nil || !(r > 0 && r <= 1) { // the negation also refuses NaN
			return Verdict{Result: Unknown, Message: fmt.Sprintf("minPassRate %q is not a number above 0 and up to 1", s)}
		}
		minRate = r
	}
	if msg := e.suite(ctx, &in); msg != "" {
		return Verdict{Result: Unknown, Message: msg}
	}
	var a evalAnswer
	if err := postJSON(ctx, e.Client, e.URL, map[string]any{"input": in, "params": params}, &a); err != nil {
		return Verdict{Result: Unknown, Message: err.Error()}
	}
	switch {
	case !slices.Contains(in.Subjects, a.Subject):
		return Verdict{Result: Unknown, Message: fmt.Sprintf("the runner ran the evals on %q, not on the version under check", a.Subject)}
	case a.Run == "":
		return Verdict{Result: Unknown, Message: "the runner named no run to cite"}
	case a.Suite != in.Evals.Digest:
		return Verdict{Result: Unknown, Message: fmt.Sprintf("the runner ran the suite %q, not %s, the %s the version ships", a.Suite, in.Evals.Digest, in.Evals.Path)}
	case a.Total <= 0 || a.Passed < 0 || a.Passed > a.Total:
		return Verdict{Result: Unknown, Message: fmt.Sprintf("the runner counted %d of %d cases passed", a.Passed, a.Total)}
	}
	v := Verdict{Result: Fail, Citation: map[string]string{
		"run": a.Run, "suite": a.Suite, "suitePath": in.Evals.Path, "contract": in.Contract, "subject": a.Subject,
		"passed": strconv.Itoa(a.Passed), "total": strconv.Itoa(a.Total),
	}}
	// Divide, never multiply: 7/100 rounds to the parsed 0.07, 0.07*100 does not round to 7.
	if float64(a.Passed)/float64(a.Total) >= minRate {
		v.Result = Pass
	}
	v.Message = fmt.Sprintf("%d of %d eval cases passed, %s needed", a.Passed, a.Total, strconv.FormatFloat(minRate*100, 'g', 10, 64)+"%")
	if a.Message != "" {
		v.Message += ": " + a.Message
	}
	return v
}

// suite sets in.Contract and in.Evals to the contract the version under
// check ships and the eval suite in it, or says why it cannot.
func (e Eval) suite(ctx context.Context, in *GateInput) string {
	if e.Revision == nil || len(in.Subjects) == 0 {
		return "the eval gate cannot read the contract the version ships"
	}
	rev := e.Revision(ctx, *in)
	switch {
	case rev.Bundle == nil:
		return "reading the contract the version ships: " + rev.Err
	case rev.Bundle.Contract == nil || rev.Bundle.Contract.Agent == nil || rev.Bundle.Contract.Agent.Evals == "":
		return "the contract " + rev.ContractRef + " ships no eval suite (agent.evals)"
	}
	path := rev.Bundle.Contract.Agent.Evals
	data, err := fs.ReadFile(rev.Bundle.FS, path)
	if err != nil {
		return fmt.Sprintf("reading the eval suite %s in %s: %v", path, rev.ContractRef, err)
	}
	sum := sha256.Sum256(data)
	in.Contract, in.Evals = rev.ContractRef, &Suite{Path: path, Digest: "sha256:" + hex.EncodeToString(sum[:])}
	return ""
}
