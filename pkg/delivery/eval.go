package delivery

import (
	"context"
	"fmt"
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
// runner gets the same {"input", "params"} as a Webhook and answers
// {"subject", "suite", "run", "passed", "total", "message"}. An answer about
// another version, with no cases, or with no run or suite to cite is Unknown,
// never Pass or Fail.
type Eval struct {
	URL    string
	Client *http.Client
}

// Check implements Gate.
func (e Eval) Check(ctx context.Context, in GateInput, params map[string]string) Verdict {
	minRate := 1.0
	if s, ok := params["minPassRate"]; ok {
		r, err := strconv.ParseFloat(s, 64)
		if err != nil || r < 0 || r > 1 {
			return Verdict{Result: Unknown, Message: fmt.Sprintf("minPassRate %q is not a number from 0 to 1", s)}
		}
		minRate = r
	}
	var a evalAnswer
	if err := postJSON(ctx, e.Client, e.URL, map[string]any{"input": in, "params": params}, &a); err != nil {
		return Verdict{Result: Unknown, Message: err.Error()}
	}
	switch {
	case !slices.Contains(in.Subjects, a.Subject):
		return Verdict{Result: Unknown, Message: fmt.Sprintf("the runner ran the evals on %q, not on the version under check", a.Subject)}
	case a.Run == "" || a.Suite == "":
		return Verdict{Result: Unknown, Message: "the runner named no run or suite to cite"}
	case a.Total <= 0 || a.Passed < 0 || a.Passed > a.Total:
		return Verdict{Result: Unknown, Message: fmt.Sprintf("the runner counted %d of %d cases passed", a.Passed, a.Total)}
	}
	v := Verdict{Result: Fail, Citation: map[string]string{
		"run": a.Run, "suite": a.Suite, "subject": a.Subject,
		"passed": strconv.Itoa(a.Passed), "total": strconv.Itoa(a.Total),
	}}
	if float64(a.Passed) >= minRate*float64(a.Total) {
		v.Result = Pass
	}
	v.Message = fmt.Sprintf("%d of %d eval cases passed, %s needed", a.Passed, a.Total, strconv.FormatFloat(minRate*100, 'f', -1, 64)+"%")
	if a.Message != "" {
		v.Message += ": " + a.Message
	}
	return v
}
