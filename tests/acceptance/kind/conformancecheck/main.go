// Command conformancecheck asserts the runtime-conformance facts the kind shard
// (tests/acceptance/kind/runtime-conformance.sh) reads off a real cluster.
//
// The harness stays thin: it brings workloads up, breaks them on purpose and
// names the facts that must hold. The facts themselves are a typed decode with a
// test suite over recorded payloads (main_test.go), so an assertion that stops
// asserting fails a unit test instead of passing a cluster run.
//
//	status  reads one Pacto (kubectl get pacto X -o json) on stdin and checks
//	        contractStatus, findings (code, severity, subject), the evidence
//	        collector behind a finding and the evaluation coverage
//	nro     reads the NodeRuntimeObservation list (kubectl get
//	        noderuntimeobservations -o json) on stdin and checks what the kernel
//	        attributed to one pod UID: listeners, connections, the collection state
//
// Every failure is accumulated and reported together, so one cluster round says
// everything that is wrong rather than the first thing.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: conformancecheck status|nro [flags] < object.json")
		os.Exit(2)
	}
	errs, err := run(os.Args[1], os.Args[2:], os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(errs) > 0 {
		fmt.Fprintln(os.Stderr, strings.Join(errs, "\n"))
		os.Exit(1)
	}
}

func run(mode string, args []string, in io.Reader) ([]string, error) {
	switch mode {
	case "status":
		return runStatus(args, in)
	case "nro":
		return runNRO(args, in)
	}
	return nil, fmt.Errorf("unknown mode %q (use status or nro)", mode)
}

// multi is a repeatable string flag.
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// --- status -------------------------------------------------------------------

type evidenceRef struct {
	Collector string `json:"collector"`
}

type finding struct {
	Code         string        `json:"code"`
	Severity     string        `json:"severity"`
	Subject      string        `json:"subject"`
	EvidenceRefs []evidenceRef `json:"evidenceRefs"`
}

type pacto struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Status struct {
		ContractStatus     string    `json:"contractStatus"`
		Findings           []finding `json:"findings"`
		EvaluationCoverage *struct {
			Evaluated  int      `json:"evaluated"`
			Required   int      `json:"required"`
			Unverified []string `json:"unverified"`
		} `json:"evaluationCoverage"`
	} `json:"status"`
}

type statusWant struct {
	status     string
	expect     []string // CODE[:severity[:subject]]
	forbid     []string // CODE
	collector  []string // CODE=substring
	unverified []string // contract path listed in evaluationCoverage.unverified
	verified   []string // contract path NOT listed there: the facet was judged
	noFindings bool
	covered    bool
}

func runStatus(args []string, in io.Reader) ([]string, error) {
	var w statusWant
	var expect, forbid, collector, unverified, verified multi
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&w.status, "status", "", "required contractStatus")
	fs.Var(&expect, "expect", "a finding CODE[:severity[:subject]] that must be present (repeatable)")
	fs.Var(&forbid, "forbid", "a finding CODE that must be absent (repeatable)")
	fs.Var(&collector, "collector", "CODE=substring: a CODE finding cites a collector containing substring (repeatable)")
	fs.Var(&unverified, "unverified", "a contract path evaluationCoverage.unverified must list (repeatable)")
	fs.Var(&verified, "verified", "a contract path evaluationCoverage.unverified must not list (repeatable)")
	fs.BoolVar(&w.noFindings, "no-findings", false, "the Pacto has no findings at all")
	fs.BoolVar(&w.covered, "covered", false, "every required assertion was evaluated")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	w.expect, w.forbid, w.collector, w.unverified, w.verified = expect, forbid, collector, unverified, verified
	var p pacto
	if err := json.NewDecoder(in).Decode(&p); err != nil {
		return nil, fmt.Errorf("decode Pacto: %w", err)
	}
	return checkStatus(p, w), nil
}

func checkStatus(p pacto, w statusWant) []string {
	var errs []string
	add := func(format string, a ...any) {
		errs = append(errs, fmt.Sprintf("%s: ", p.Metadata.Name)+fmt.Sprintf(format, a...))
	}
	s := p.Status
	if w.status != "" && s.ContractStatus != w.status {
		add("contractStatus is %q, want %q (findings: %s)", s.ContractStatus, w.status, summary(s.Findings))
	}
	if w.noFindings && len(s.Findings) > 0 {
		add("want no findings, got %s", summary(s.Findings))
	}
	for _, e := range w.expect {
		code, sev, subj := splitExpect(e)
		if !hasFinding(s.Findings, code, sev, subj) {
			add("missing finding %s (got %s)", e, summary(s.Findings))
		}
	}
	for _, code := range w.forbid {
		if hasFinding(s.Findings, code, "", "") {
			add("forbidden finding %s present (got %s)", code, summary(s.Findings))
		}
	}
	for _, c := range w.collector {
		code, sub, _ := strings.Cut(c, "=")
		if !citesCollector(s.Findings, code, sub) {
			add("no %s finding cites a collector containing %q (got %s)", code, sub, collectors(s.Findings))
		}
	}
	for _, path := range w.unverified {
		if cov := s.EvaluationCoverage; cov == nil || !slices.Contains(cov.Unverified, path) {
			add("evaluationCoverage.unverified does not list %s", path)
		}
	}
	for _, path := range w.verified {
		if cov := s.EvaluationCoverage; cov != nil && slices.Contains(cov.Unverified, path) {
			add("evaluationCoverage.unverified lists %s, want it judged", path)
		}
	}
	if w.covered {
		switch cov := s.EvaluationCoverage; {
		case cov == nil:
			add("no evaluationCoverage")
		case cov.Required == 0 || cov.Evaluated != cov.Required:
			add("evaluationCoverage evaluated=%d required=%d, want equal and non-zero", cov.Evaluated, cov.Required)
		}
	}
	return errs
}

func splitExpect(e string) (code, sev, subj string) {
	parts := strings.SplitN(e, ":", 3)
	parts = append(parts, "", "")
	return parts[0], parts[1], parts[2]
}

func hasFinding(fs []finding, code, sev, subj string) bool {
	for _, f := range fs {
		if f.Code == code && (sev == "" || f.Severity == sev) && (subj == "" || f.Subject == subj) {
			return true
		}
	}
	return false
}

func citesCollector(fs []finding, code, sub string) bool {
	for _, f := range fs {
		if f.Code != code {
			continue
		}
		for _, r := range f.EvidenceRefs {
			if strings.Contains(r.Collector, sub) {
				return true
			}
		}
	}
	return false
}

func summary(fs []finding) string {
	if len(fs) == 0 {
		return "[]"
	}
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Code+":"+f.Severity+":"+f.Subject)
	}
	return "[" + strings.Join(out, " ") + "]"
}

func collectors(fs []finding) string {
	var out []string
	for _, f := range fs {
		for _, r := range f.EvidenceRefs {
			out = append(out, f.Code+"="+r.Collector)
		}
	}
	return "[" + strings.Join(out, " ") + "]"
}

// --- nro ----------------------------------------------------------------------

type listener struct {
	Port    int32  `json:"port"`
	Address string `json:"address"`
}

type connection struct {
	Address   string `json:"address"`
	Port      int32  `json:"port"`
	Successes int64  `json:"successes"`
	Failures  int64  `json:"failures"`
}

type podObs struct {
	UID         string       `json:"uid"`
	Listeners   []listener   `json:"listeners"`
	Connections []connection `json:"connections"`
}

type nodeObs struct {
	Status struct {
		NodeName   string `json:"nodeName"`
		Collection struct {
			State  string `json:"state"`
			Reason string `json:"reason"`
		} `json:"collection"`
		Pods []podObs `json:"pods"`
	} `json:"status"`
}

type nroList struct {
	Items []nodeObs `json:"items"`
}

type nroWant struct {
	uid        string
	absentUID  string
	state      string
	listener   []int32
	noListener []int32
	connected  []string // ADDR:PORT with successes
	refused    []string // ADDR:PORT with failures and no successes
	noPeer     []string // ADDR:PORT the pod has no connection entry for
	none       bool     // no node object at all
}

func runNRO(args []string, in io.Reader) ([]string, error) {
	var w nroWant
	var listen, noListen, connected, refused, noPeer multi
	fs := flag.NewFlagSet("nro", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&w.uid, "uid", "", "the pod UID the listener and connection checks are about")
	fs.StringVar(&w.absentUID, "absent-uid", "", "a pod UID no node object may still carry")
	fs.StringVar(&w.state, "state", "", "the collection state every node object must report")
	fs.BoolVar(&w.none, "none", false, "no node object exists")
	fs.Var(&listen, "listener", "a port the pod listens on, off loopback (repeatable)")
	fs.Var(&noListen, "no-listener", "a port the pod does not listen on (repeatable)")
	fs.Var(&connected, "connected", "ADDR:PORT the pod connected to successfully (repeatable)")
	fs.Var(&refused, "refused", "ADDR:PORT the pod only failed to connect to (repeatable)")
	fs.Var(&noPeer, "no-peer", "ADDR:PORT the pod has no connection to at all (repeatable)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	for _, v := range listen {
		p, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("-listener %q: %w", v, err)
		}
		w.listener = append(w.listener, int32(p))
	}
	for _, v := range noListen {
		p, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("-no-listener %q: %w", v, err)
		}
		w.noListener = append(w.noListener, int32(p))
	}
	w.connected, w.refused, w.noPeer = connected, refused, noPeer
	var l nroList
	if err := json.NewDecoder(in).Decode(&l); err != nil {
		return nil, fmt.Errorf("decode NodeRuntimeObservation list: %w", err)
	}
	return checkNRO(l, w), nil
}

func checkNRO(l nroList, w nroWant) []string {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	if w.none {
		if len(l.Items) > 0 {
			add("want no NodeRuntimeObservation, got %d", len(l.Items))
		}
		return errs
	}
	if len(l.Items) == 0 {
		return []string{"no NodeRuntimeObservation published"}
	}
	for _, n := range l.Items {
		if w.state != "" && n.Status.Collection.State != w.state {
			add("node %s collection.state is %q (%s), want %q", n.Status.NodeName, n.Status.Collection.State, n.Status.Collection.Reason, w.state)
		}
	}
	if w.absentUID != "" && findPod(l, w.absentUID) != nil {
		add("pod %s is still attributed by a node object", w.absentUID)
	}
	if w.uid == "" {
		return errs
	}
	pod := findPod(l, w.uid)
	if pod == nil {
		return append(errs, fmt.Sprintf("pod %s is not attributed by any node object", w.uid))
	}
	for _, port := range w.listener {
		if !listens(pod, port) {
			add("pod %s does not listen on %d (listeners %v)", w.uid, port, pod.Listeners)
		}
	}
	for _, port := range w.noListener {
		if listens(pod, port) {
			add("pod %s listens on %d, want no listener", w.uid, port)
		}
	}
	for _, peer := range w.connected {
		if c := findConn(pod, peer); c == nil || c.Successes == 0 {
			add("pod %s has no successful connection to %s (connections %v)", w.uid, peer, pod.Connections)
		}
	}
	for _, peer := range w.refused {
		if c := findConn(pod, peer); c == nil || c.Failures == 0 || c.Successes > 0 {
			add("pod %s has no failed-only connection to %s (connections %v)", w.uid, peer, pod.Connections)
		}
	}
	for _, peer := range w.noPeer {
		if findConn(pod, peer) != nil {
			add("pod %s has a connection to %s, want none", w.uid, peer)
		}
	}
	return errs
}

func findPod(l nroList, uid string) *podObs {
	for i := range l.Items {
		for j := range l.Items[i].Status.Pods {
			if p := &l.Items[i].Status.Pods[j]; p.UID == uid {
				return p
			}
		}
	}
	return nil
}

// listens mirrors the operator: a loopback-only listener does not serve the pod IP.
func listens(p *podObs, port int32) bool {
	for _, l := range p.Listeners {
		if l.Port != port {
			continue
		}
		if ip := net.ParseIP(l.Address); ip == nil || !ip.IsLoopback() {
			return true
		}
	}
	return false
}

func findConn(p *podObs, peer string) *connection {
	want := canonical(peer)
	for i := range p.Connections {
		c := &p.Connections[i]
		if canonical(net.JoinHostPort(c.Address, strconv.Itoa(int(c.Port)))) == want {
			return c
		}
	}
	return nil
}

// canonical folds IPv4-mapped IPv6 and spelling variants so both sides compare equal.
func canonical(hostport string) string {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	return net.JoinHostPort(host, port)
}
