package main

import (
	"strings"
	"testing"
)

// A Pacto as the operator writes it for the listener-absent workload: endpoints
// ready, the kernel saw no listener, the window elapsed.
const deafPacto = `{
  "apiVersion": "pacto.trianalab.io/v1alpha1", "kind": "Pacto",
  "metadata": {"name": "deaf", "namespace": "conf"},
  "status": {
    "contractStatus": "NonCompliant",
    "evaluationCoverage": {"evaluated": 2, "required": 2, "unverified": ["interfaces[name=api].type"]},
    "findings": [{
      "code": "INTERFACE_ABSENT", "severity": "error", "category": "RuntimeContradiction",
      "subject": "interface/api", "contractPath": "interfaces[name=api]", "message": "interface api is absent",
      "evidenceRefs": [{"source": "k8s", "observedAt": "2026-10-18T10:00:00Z", "collector": "k8s-observer+ebpf", "detectedAt": "2026-10-18T10:00:00Z"}]
    }]
  }
}`

const conformingPacto = `{
  "metadata": {"name": "conforming"},
  "status": {"contractStatus": "Compliant", "evaluationCoverage": {"evaluated": 5, "required": 5}}
}`

func status(t *testing.T, payload string, args ...string) []string {
	t.Helper()
	errs, err := run("status", args, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return errs
}

func TestStatus_AcceptsTheRecordedPayloads(t *testing.T) {
	if errs := status(t, deafPacto, "-status", "NonCompliant", "-expect", "INTERFACE_ABSENT:error:interface/api",
		"-expect", "INTERFACE_ABSENT", "-forbid", "CAPABILITY_ABSENT", "-collector", "INTERFACE_ABSENT=ebpf", "-covered",
		"-unverified", "interfaces[name=api].type", "-verified", "interfaces[name=api].visibility"); errs != nil {
		t.Fatalf("unexpected failures: %v", errs)
	}
	if errs := status(t, conformingPacto, "-status", "Compliant", "-no-findings", "-covered", "-forbid", "INTERFACE_ABSENT"); errs != nil {
		t.Fatalf("unexpected failures: %v", errs)
	}
}

// Every flag must be able to fail: a check that cannot fail asserts nothing.
func TestStatus_CatchesEveryBrokenFact(t *testing.T) {
	cases := map[string][]string{
		"wrong status":       {"-status", "Compliant"},
		"missing code":       {"-expect", "DEPENDENCY_UNREACHABLE"},
		"wrong severity":     {"-expect", "INTERFACE_ABSENT:warning"},
		"wrong subject":      {"-expect", "INTERFACE_ABSENT:error:interface/other"},
		"forbidden present":  {"-forbid", "INTERFACE_ABSENT"},
		"collector mismatch": {"-collector", "INTERFACE_ABSENT=otel"},
		"collector no code":  {"-collector", "CAPABILITY_ABSENT=ebpf"},
		"findings present":   {"-no-findings"},
		"not unverified":     {"-unverified", "interfaces[name=api].visibility"},
		"not verified":       {"-verified", "interfaces[name=api].type"},
	}
	for name, args := range cases {
		if errs := status(t, deafPacto, args...); len(errs) != 1 {
			t.Errorf("%s: want exactly one failure, got %v", name, errs)
		}
	}
	for name, payload := range map[string]string{
		"no coverage":      `{"metadata":{"name":"x"},"status":{}}`,
		"partial coverage": `{"metadata":{"name":"x"},"status":{"evaluationCoverage":{"evaluated":1,"required":2}}}`,
		"zero coverage":    `{"metadata":{"name":"x"},"status":{"evaluationCoverage":{"evaluated":0,"required":0}}}`,
	} {
		if errs := status(t, payload, "-covered"); len(errs) != 1 {
			t.Errorf("%s: want exactly one failure, got %v", name, errs)
		}
	}
	if errs := status(t, `{"metadata":{"name":"x"},"status":{}}`, "-unverified", "interfaces[name=api].type"); len(errs) != 1 {
		t.Errorf("no coverage, unverified: want exactly one failure, got %v", errs)
	}
}

// A provider Pacto with one uncontracted caller the kernel saw.
const backendPacto = `{
  "metadata": {"name": "backend"},
  "status": {"contractStatus": "Compliant",
    "observedCallers": [{"namespace": "conf", "kind": "Deployment", "name": "curl", "source": "ebpf", "firstSeen": "2026-10-18T10:00:00Z"}],
    "observedCallersCoverage": {"state": "complete", "refreshSeconds": 30}}
}`

func TestStatus_ObservedCallers(t *testing.T) {
	if errs := status(t, backendPacto, "-caller", "Deployment/curl", "-no-caller", "Deployment/caller", "-caller-coverage", "complete"); errs != nil {
		t.Fatalf("unexpected failures: %v", errs)
	}
	for name, args := range map[string][]string{
		"missing caller": {"-caller", "Deployment/other"},
		"listed caller":  {"-no-caller", "Deployment/curl"},
		"wrong coverage": {"-caller-coverage", "partial"},
	} {
		if errs := status(t, backendPacto, args...); len(errs) != 1 {
			t.Errorf("%s: want exactly one failure, got %v", name, errs)
		}
	}
	if errs := status(t, conformingPacto, "-caller-coverage", "complete"); len(errs) != 1 {
		t.Errorf("no coverage: want exactly one failure, got %v", errs)
	}
}

func TestStatus_ReportsEveryFailureTogether(t *testing.T) {
	errs := status(t, conformingPacto, "-status", "NonCompliant", "-expect", "INTERFACE_ABSENT", "-collector", "INTERFACE_ABSENT=ebpf")
	if len(errs) != 3 {
		t.Fatalf("want three failures, got %v", errs)
	}
	if !strings.Contains(errs[0], "conforming:") {
		t.Errorf("failure does not name the Pacto: %q", errs[0])
	}
}

// One kind node, the agent running: the conforming pod listens and reaches its
// provider through the ClusterIP; the caller of the deaf service only fails.
const nodeList = `{
  "apiVersion": "v1", "kind": "List",
  "items": [{
    "apiVersion": "pacto.trianalab.io/v1alpha1", "kind": "NodeRuntimeObservation",
    "metadata": {"name": "pacto-runtime-control-plane", "namespace": "pacto-system"},
    "status": {
      "nodeName": "pacto-runtime-control-plane", "agentVersion": "5.4.0", "publishIntervalSeconds": 5,
      "lastPublished": "2026-10-18T10:00:00Z",
      "collection": {"state": "Running"},
      "pods": [
        {"uid": "11111111-aaaa-bbbb-cccc-000000000001", "baselineAt": "2026-10-18T09:59:00Z",
         "listeners": [{"port": 8080, "family": "inet6", "address": "::", "process": "agnhost"},
                       {"port": 9999, "family": "inet", "address": "127.0.0.1", "process": "agnhost"}],
         "connections": [{"address": "::ffff:10.96.12.7", "port": 80, "successes": 12}]},
        {"uid": "22222222-aaaa-bbbb-cccc-000000000002",
         "connections": [{"address": "10.96.40.2", "port": 80, "failures": 9}]}
      ]
    }
  }]
}`

func nro(t *testing.T, payload string, args ...string) []string {
	t.Helper()
	errs, err := run("nro", args, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return errs
}

func TestNRO_AcceptsTheRecordedPayload(t *testing.T) {
	ok := [][]string{
		{"-state", "Running", "-uid", "11111111-aaaa-bbbb-cccc-000000000001", "-listener", "8080", "-no-listener", "9999",
			"-connected", "10.96.12.7:80", "-no-peer", "10.96.40.2:80", "-absent-uid", "33333333-aaaa-bbbb-cccc-000000000003"},
		{"-uid", "22222222-aaaa-bbbb-cccc-000000000002", "-no-listener", "8080", "-refused", "10.96.40.2:80"},
		{"-state", "Running"},
	}
	for _, args := range ok {
		if errs := nro(t, nodeList, args...); errs != nil {
			t.Errorf("%v: unexpected failures: %v", args, errs)
		}
	}
	if errs := nro(t, `{"items":[]}`, "-none"); errs != nil {
		t.Errorf("-none on an empty list: %v", errs)
	}
}

func TestNRO_CatchesEveryBrokenFact(t *testing.T) {
	const a, b = "11111111-aaaa-bbbb-cccc-000000000001", "22222222-aaaa-bbbb-cccc-000000000002"
	cases := map[string][]string{
		"wrong state":           {"-state", "Failed"},
		"uid still attributed":  {"-absent-uid", a},
		"unknown uid":           {"-uid", "nope"},
		"no listener":           {"-uid", b, "-listener", "8080"},
		"loopback only":         {"-uid", a, "-listener", "9999"},
		"listener present":      {"-uid", a, "-no-listener", "8080"},
		"no connection":         {"-uid", b, "-connected", "10.96.12.7:80"},
		"failures only":         {"-uid", b, "-connected", "10.96.40.2:80"},
		"successes not refused": {"-uid", a, "-refused", "10.96.12.7:80"},
		"objects exist":         {"-none"},
		"peer present":          {"-uid", a, "-no-peer", "10.96.12.7:80"},
	}
	for name, args := range cases {
		if errs := nro(t, nodeList, args...); len(errs) != 1 {
			t.Errorf("%s: want exactly one failure, got %v", name, errs)
		}
	}
	if errs := nro(t, `{"items":[]}`, "-state", "Running"); len(errs) != 1 {
		t.Errorf("empty list: want one failure, got %v", errs)
	}
}

func TestRun_RejectsBadInput(t *testing.T) {
	bad := []struct {
		mode, payload string
		args          []string
	}{
		{"bogus", "{}", nil},
		{"status", "not json", nil},
		{"status", "{}", []string{"-nope"}},
		{"nro", "not json", nil},
		{"nro", "{}", []string{"-listener", "x"}},
		{"nro", "{}", []string{"-no-listener", "x"}},
		{"nro", "{}", []string{"-nope"}},
	}
	for _, c := range bad {
		if _, err := run(c.mode, c.args, strings.NewReader(c.payload)); err == nil {
			t.Errorf("%s %v %q: want an error", c.mode, c.args, c.payload)
		}
	}
}

func TestCanonical_LeavesUnparseableInputAlone(t *testing.T) {
	if got := canonical("no-port"); got != "no-port" {
		t.Errorf("canonical(no-port) = %q", got)
	}
	if got := canonical("[::ffff:10.0.0.1]:80"); got != "10.0.0.1:80" {
		t.Errorf("canonical mapped = %q", got)
	}
}
