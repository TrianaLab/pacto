// Command runtimecheck asserts what `pacto run` and `pacto agent --sink local`
// printed with --output-format json, for the local runtime acceptance
// (tests/acceptance/local/runtime-docker.sh and runtime-run.sh).
//
// Both print one JSON round per line, interleaved with whatever else shares the
// stream (the wrapped command's output, the agent's logs): every line that is
// not a round is skipped. The facts are a typed decode with a test suite over
// recorded payloads (main_test.go), so an assertion that stops asserting fails
// a unit test instead of passing a Docker run.
//
//	runtimecheck [flags] < output
//
// Without -target the round checked is the last one, which must be the final
// round `pacto run` prints after its command exits; with -target it is the
// last round that names that target. Every failure is reported together.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

func main() {
	errs, err := run(os.Args[1:], os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(errs) > 0 {
		fmt.Fprintln(os.Stderr, strings.Join(errs, "\n"))
		os.Exit(1)
	}
}

// multi is a repeatable string flag.
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

type evidenceRef struct {
	Collector string `json:"collector"`
}

type finding struct {
	Code    string `json:"code"`
	Subject struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"subject"`
	EvidenceRefs []evidenceRef `json:"evidenceRefs"`
}

type result struct {
	Target   string `json:"target"`
	Status   string `json:"status"`
	Coverage struct {
		Evaluated int `json:"evaluated"`
		Required  int `json:"required"`
	} `json:"coverage"`
	Findings []finding `json:"findings"`
	Error    string    `json:"error"`
}

type round struct {
	EvaluatedAt string   `json:"evaluatedAt"`
	Mode        string   `json:"mode"`
	Final       bool     `json:"final"`
	Targets     []result `json:"targets"`
}

type want struct {
	target, mode, status string
	expect, forbid       []string // CODE[:kind/name]
	collector            []string // CODE=collector, exact
	covered              bool
}

func run(args []string, in io.Reader) ([]string, error) {
	var w want
	var expect, forbid, collector multi
	fs := flag.NewFlagSet("runtimecheck", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&w.target, "target", "", "check the last round naming this target instead of the final round")
	fs.StringVar(&w.mode, "mode", "", "the round's mode (ebpf, unprivileged, unsupported)")
	fs.StringVar(&w.status, "status", "", "the target's status")
	fs.Var(&expect, "expect", "a finding CODE[:kind/name] that must be present (repeatable)")
	fs.Var(&forbid, "forbid", "a finding CODE[:kind/name] that must be absent (repeatable)")
	fs.Var(&collector, "collector", "CODE=collector: a CODE finding cites exactly this collector (repeatable)")
	fs.BoolVar(&w.covered, "covered", false, "every required assertion was evaluated")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	w.expect, w.forbid, w.collector = expect, forbid, collector
	rounds, err := decode(in)
	if err != nil {
		return nil, err
	}
	r, res, errs := pick(rounds, w.target)
	if errs != nil {
		return errs, nil
	}
	return check(w, r, res), nil
}

// decode reads every round in the stream, skipping lines that are not one.
func decode(in io.Reader) ([]round, error) {
	var out []round
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var r round
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.EvaluatedAt != "" {
			out = append(out, r)
		}
	}
	return out, sc.Err()
}

// pick returns the round to check and its one result.
func pick(rounds []round, target string) (round, result, []string) {
	if target == "" {
		if len(rounds) == 0 || !rounds[len(rounds)-1].Final {
			return round{}, result{}, []string{fmt.Sprintf("no final round among %d rounds", len(rounds))}
		}
		r := rounds[len(rounds)-1]
		if len(r.Targets) != 1 {
			return round{}, result{}, []string{fmt.Sprintf("the final round has %d targets, want 1", len(r.Targets))}
		}
		return r, r.Targets[0], nil
	}
	for i := len(rounds) - 1; i >= 0; i-- {
		if j := slices.IndexFunc(rounds[i].Targets, func(t result) bool { return t.Target == target }); j >= 0 {
			return rounds[i], rounds[i].Targets[j], nil
		}
	}
	return round{}, result{}, []string{fmt.Sprintf("no round among %d names target %s", len(rounds), target)}
}

// matches reports whether a finding is CODE[:kind/name].
func matches(f finding, spec string) bool {
	code, subject, scoped := strings.Cut(spec, ":")
	return f.Code == code && (!scoped || f.Subject.Kind+"/"+f.Subject.Name == subject)
}

func check(w want, r round, res result) []string {
	var errs []string
	failf := func(format string, a ...any) { errs = append(errs, res.Target+": "+fmt.Sprintf(format, a...)) }
	if res.Error != "" {
		failf("not evaluated: %s", res.Error)
	}
	if w.mode != "" && r.Mode != w.mode {
		failf("mode %q, want %q", r.Mode, w.mode)
	}
	if w.status != "" && res.Status != w.status {
		failf("status %q, want %q (findings %s)", res.Status, w.status, codes(res))
	}
	if w.covered && res.Coverage.Evaluated != res.Coverage.Required {
		failf("evaluated %d of %d required assertions", res.Coverage.Evaluated, res.Coverage.Required)
	}
	for _, spec := range w.expect {
		if !slices.ContainsFunc(res.Findings, func(f finding) bool { return matches(f, spec) }) {
			failf("no %s finding (findings %s)", spec, codes(res))
		}
	}
	for _, spec := range w.forbid {
		if slices.ContainsFunc(res.Findings, func(f finding) bool { return matches(f, spec) }) {
			failf("a %s finding (findings %s)", spec, codes(res))
		}
	}
	for _, spec := range w.collector {
		code, collector, _ := strings.Cut(spec, "=")
		i := slices.IndexFunc(res.Findings, func(f finding) bool { return f.Code == code })
		switch {
		case i < 0:
			failf("no %s finding to cite %s", code, collector)
		case len(res.Findings[i].EvidenceRefs) == 0 || res.Findings[i].EvidenceRefs[0].Collector != collector:
			failf("%s cites %+v, want collector %s", code, res.Findings[i].EvidenceRefs, collector)
		}
	}
	return errs
}

// codes lists the findings as CODE:kind/name for a failure message.
func codes(res result) string {
	out := make([]string, len(res.Findings))
	for i, f := range res.Findings {
		out[i] = f.Code + ":" + f.Subject.Kind + "/" + f.Subject.Name
	}
	return "[" + strings.Join(out, " ") + "]"
}
