package main

import (
	"strings"
	"testing"
)

// The stdout of a privileged `pacto run` of a command that never listened: the wrapped command's own output
// in between, two regular rounds and the final one.
const deafRun = `starting the server
{"evaluatedAt":"2026-10-18T13:51:24.003Z","mode":"ebpf","targets":[{"target":"session/pacto-run-1","contract":"/c","status":"Unknown","coverage":{"evaluated":0,"required":1},"findings":[{"code":"EVIDENCE_INSUFFICIENT","severity":"unknown","category":"Inconclusive","subject":{"kind":"interface","name":"api"},"contractPath":"interfaces[name=api]","message":"evidence for interface \"api\" is insufficient to satisfy or contradict the contract","evidenceRefs":[{"source":"pacto-run","observedAt":"2026-10-18T13:51:24Z","collector":"pacto-run+ebpf","detectedAt":"2026-10-18T13:51:24Z"}]}]}]}
{"evaluatedAt":"2026-10-18T13:51:25.003Z","mode":"ebpf","targets":[{"target":"session/pacto-run-1","contract":"/c","status":"NonCompliant","coverage":{"evaluated":1,"required":1},"findings":[{"code":"INTERFACE_ABSENT","severity":"error","category":"RuntimeDrift","subject":{"kind":"interface","name":"api"},"contractPath":"interfaces[name=api]","message":"interface \"api\" is not being served","evidenceRefs":[{"source":"pacto-run","observedAt":"2026-10-18T13:51:25Z","collector":"pacto-run+ebpf","detectedAt":"2026-10-18T13:51:25Z"}]}]}]}
{"evaluatedAt":"2026-10-18T13:51:26.007403502Z","mode":"ebpf","final":true,"targets":[{"target":"session/pacto-run-1","contract":"/c","status":"NonCompliant","coverage":{"evaluated":1,"required":1},"findings":[{"code":"INTERFACE_ABSENT","severity":"error","category":"RuntimeDrift","subject":{"kind":"interface","name":"api"},"contractPath":"interfaces[name=api]","message":"interface \"api\" is not being served","evidenceRefs":[{"source":"pacto-run","observedAt":"2026-10-18T13:51:26Z","collector":"pacto-run+ebpf","detectedAt":"2026-10-18T13:51:26Z"}]}]}]}
`

// Two rounds of `pacto agent --sink local --docker`, the second without the served container.
const agentRounds = `{"evaluatedAt":"2026-10-18T14:20:03Z","targets":[{"target":"container/served-1bfa38","contract":"/fixtures/served","status":"Compliant","coverage":{"evaluated":1,"required":1}},{"target":"container/refused-1bfa38","contract":"/fixtures/client","status":"NonCompliant","coverage":{"evaluated":1,"required":1},"findings":[{"code":"DEPENDENCY_UNREACHABLE","severity":"error","category":"RuntimeDrift","subject":{"kind":"dependency","name":"backend"},"contractPath":"dependencies[name=backend]","message":"dependency \"backend\" is unreachable","evidenceRefs":[{"source":"pacto-agent","observedAt":"2026-10-18T14:20:03Z","collector":"pacto-agent+ebpf","detectedAt":"2026-10-18T14:20:03Z"}]}]}]}
{"evaluatedAt":"2026-10-18T14:20:06Z","targets":[{"target":"container/refused-1bfa38","contract":"/fixtures/client","status":"NonCompliant","coverage":{"evaluated":1,"required":1},"findings":[{"code":"DEPENDENCY_UNREACHABLE","severity":"error","category":"RuntimeDrift","subject":{"kind":"dependency","name":"backend"},"contractPath":"dependencies[name=backend]","message":"dependency \"backend\" is unreachable","evidenceRefs":[{"source":"pacto-agent","observedAt":"2026-10-18T14:20:06Z","collector":"pacto-agent+ebpf","detectedAt":"2026-10-18T14:20:06Z"}]}]},{"target":"container/broken-1bfa38","contract":"/fixtures/nope","error":"no pacto.yaml"}]}
`

func verify(t *testing.T, payload string, args ...string) []string {
	t.Helper()
	errs, err := run(args, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return errs
}

func TestAcceptsTheRecordedPayloads(t *testing.T) {
	if errs := verify(t, deafRun, "-mode", "ebpf", "-status", "NonCompliant", "-covered", "-expect", "INTERFACE_ABSENT",
		"-expect", "INTERFACE_ABSENT:interface/api", "-forbid", "EVIDENCE_INSUFFICIENT", "-forbid", "INTERFACE_ABSENT:interface/admin",
		"-collector", "INTERFACE_ABSENT=pacto-run+ebpf"); errs != nil {
		t.Fatalf("unexpected failures: %v", errs)
	}
	// With -target the last round naming it is checked, not the last round.
	if errs := verify(t, agentRounds, "-target", "container/served-1bfa38", "-status", "Compliant", "-covered"); errs != nil {
		t.Fatalf("unexpected failures: %v", errs)
	}
	if errs := verify(t, agentRounds, "-target", "container/refused-1bfa38", "-status", "NonCompliant",
		"-expect", "DEPENDENCY_UNREACHABLE:dependency/backend", "-collector", "DEPENDENCY_UNREACHABLE=pacto-agent+ebpf"); errs != nil {
		t.Fatalf("unexpected failures: %v", errs)
	}
}

// Every flag must be able to fail: a check that cannot fail asserts nothing.
func TestCatchesEveryBrokenFact(t *testing.T) {
	cases := map[string][]string{
		"wrong mode":         {"-mode", "unprivileged"},
		"wrong status":       {"-status", "Compliant"},
		"missing code":       {"-expect", "DEPENDENCY_UNREACHABLE"},
		"wrong subject":      {"-expect", "INTERFACE_ABSENT:interface/admin"},
		"forbidden present":  {"-forbid", "INTERFACE_ABSENT:interface/api"},
		"collector mismatch": {"-collector", "INTERFACE_ABSENT=pacto-run"},
		"collector no code":  {"-collector", "DEPENDENCY_UNREACHABLE=pacto-run+ebpf"},
	}
	for name, args := range cases {
		if errs := verify(t, deafRun, args...); len(errs) == 0 {
			t.Errorf("%s: %v passed", name, args)
		}
	}
	notCovered := strings.Replace(deafRun, `"final":true,"targets":[{"target":"session/pacto-run-1","contract":"/c","status":"NonCompliant","coverage":{"evaluated":1`,
		`"final":true,"targets":[{"target":"session/pacto-run-1","contract":"/c","status":"NonCompliant","coverage":{"evaluated":0`, 1)
	if errs := verify(t, notCovered, "-covered"); len(errs) == 0 {
		t.Error("-covered passed an uncovered round")
	}
	noRefs := strings.Replace(deafRun, `"evidenceRefs":[{"source":"pacto-run","observedAt":"2026-10-18T13:51:26Z","collector":"pacto-run+ebpf","detectedAt":"2026-10-18T13:51:26Z"}]`, `"evidenceRefs":[]`, 1)
	if errs := verify(t, noRefs, "-collector", "INTERFACE_ABSENT=pacto-run+ebpf"); len(errs) == 0 {
		t.Error("-collector passed a finding citing nothing")
	}
}

func TestRoundSelection(t *testing.T) {
	// The last round is not final: the run has not ended.
	unfinished := deafRun[:strings.LastIndex(strings.TrimSuffix(deafRun, "\n"), "\n")+1]
	if errs := verify(t, unfinished); len(errs) != 1 || !strings.Contains(errs[0], "no final round") {
		t.Errorf("unfinished = %v", errs)
	}
	if errs := verify(t, agentRounds); len(errs) != 1 || !strings.Contains(errs[0], "no final round") {
		t.Errorf("agent rounds without -target = %v", errs)
	}
	twoTargets := `{"evaluatedAt":"x","final":true,"targets":[{"target":"a"},{"target":"b"}]}`
	if errs := verify(t, twoTargets); len(errs) != 1 || !strings.Contains(errs[0], "has 2 targets") {
		t.Errorf("two targets = %v", errs)
	}
	if errs := verify(t, agentRounds, "-target", "container/nope"); len(errs) != 1 || !strings.Contains(errs[0], "no round among 2") {
		t.Errorf("unknown target = %v", errs)
	}
	if errs := verify(t, agentRounds, "-target", "container/broken-1bfa38"); len(errs) != 1 || !strings.Contains(errs[0], "not evaluated: no pacto.yaml") {
		t.Errorf("broken target = %v", errs)
	}
	if _, err := run([]string{"-bogus"}, strings.NewReader("")); err == nil {
		t.Error("an unknown flag was accepted")
	}
}
