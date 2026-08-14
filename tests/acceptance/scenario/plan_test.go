package scenario

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// These are COUNTEREXAMPLES. Each mutates exactly one declaration in the fixture
// and proves the projection the Kind harness consumes moved with it.
//
// Before the plan existed the harness restated all of this — the repository each
// directory was pushed to, which revision was deployed, the observation source's
// id, the evidence subject and the producer that signed it. Every one of these
// tests passed vacuously then: the scenario changed and the shell went on
// describing the old fixture, because the shell was never reading the scenario.
// Each test below fails if that ever becomes true again for its value.
//
// The assertion is deliberately blunt: the new value appears in the consumed
// projection and the old one is gone. A projection that merely ADDED the new
// value while still carrying the stale one is the same bug in a subtler form.

// projection is everything the harness reads: the plan it acts on, the CRs it
// applies and the payloads it signs. One string, because "did the harness's input
// change" is one question.
func projection(t *testing.T, s Scenario) string {
	t.Helper()
	plan, err := s.Plan("/out")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	crs, err := s.PactoCRs("demo", domain, digestsFor(s))
	if err != nil {
		t.Fatalf("PactoCRs: %v", err)
	}
	payloads, err := s.EvidencePayloads("/out", domain, digestsFor(s))
	if err != nil {
		t.Fatalf("EvidencePayloads: %v", err)
	}
	var b strings.Builder
	b.Write(plan)
	b.Write(crs)
	for path, body := range payloads {
		b.WriteString(path + "\n")
		b.Write(body)
	}
	return b.String()
}

// digestsFor stands in for the registry: one distinguishable digest per published
// revision, so a CR that pins the wrong one is visible rather than plausible.
func digestsFor(s Scenario) map[string]string {
	d := map[string]string{}
	for _, svc := range s.Services {
		for _, rev := range svc.Revisions {
			d[DigestKey(svc.Name, rev.Version)] = "sha256:" + svc.Name + "-" + rev.Version
		}
	}
	return d
}

// mutate deep-copies the fixture far enough that a change cannot leak back into
// the package-level value the other tests read.
func mutate(f func(*Scenario)) Scenario {
	s := OperationalGraph
	s.Sources = append([]Source(nil), s.Sources...)
	s.Relationships = append([]Relationship(nil), s.Relationships...)
	s.Evidence = append([]Evidence(nil), s.Evidence...)
	s.Services = append([]Service(nil), s.Services...)
	for i := range s.Services {
		s.Services[i].Revisions = append([]Revision(nil), s.Services[i].Revisions...)
		if w := s.Services[i].Workload; w != nil {
			cp := *w
			s.Services[i].Workload = &cp
		}
	}
	f(&s)
	return s
}

// moved asserts the projection now carries want and no longer carries stale.
func moved(t *testing.T, before, after, stale, want string) {
	t.Helper()
	if !strings.Contains(before, stale) {
		t.Fatalf("the baseline projection never carried %q, so this proves nothing", stale)
	}
	if !strings.Contains(after, want) {
		t.Errorf("the projection does not carry %q; the harness would still be told the old value", want)
	}
	if strings.Contains(after, stale) {
		t.Errorf("the projection still carries %q after it was changed", stale)
	}
}

// declared is the service a mutation targets, named rather than indexed by a
// position that silently shifts when one is added.
func declared(s *Scenario, name string) *Service {
	for i := range s.Services {
		if s.Services[i].Name == name {
			return &s.Services[i]
		}
	}
	panic("the fixture declares no service " + name)
}

func TestPlan_FollowsTheDeclaredRepository(t *testing.T) {
	before := projection(t, OperationalGraph)
	after := projection(t, mutate(func(s *Scenario) {
		declared(s, "checkout").Repo = "commerce/checkout"
	}))
	moved(t, before, after, "\tcheckout:1.0.0\n", "\tcommerce/checkout:1.0.0\n")
	moved(t, before, after, domain+"/checkout@", domain+"/commerce/checkout@")
}

func TestPlan_FollowsTheDeclaredBundleDirectory(t *testing.T) {
	before := projection(t, OperationalGraph)
	after := projection(t, mutate(func(s *Scenario) {
		declared(s, "checkout").Revisions[0].Dir = "checkout-first"
	}))
	moved(t, before, after, "/out/checkout-a\t", "/out/checkout-first\t")
}

func TestPlan_FollowsTheDeclaredVersion(t *testing.T) {
	before := projection(t, OperationalGraph)
	after := projection(t, mutate(func(s *Scenario) {
		declared(s, "checkout").Revisions[0].Version = "1.0.1"
	}))
	// The tag published, the key the digest comes back under, and the digest the
	// deployed CR therefore pins all move together.
	moved(t, before, after, "\tcheckout:1.0.0\n", "\tcheckout:1.0.1\n")
	moved(t, before, after, "checkout@1.0.0\t", "checkout@1.0.1\t")
	moved(t, before, after, "sha256:checkout-1.0.0", "sha256:checkout-1.0.1")
}

// Which revision runs is the whole point of the fixture: checkout is published
// twice and deployed once, so moving the flag has to move the applied CR — and
// nothing else. What the harness PUBLISHES and RUNS is the same either way, so a
// plan that moved with the flag would mean deployment had leaked into the shell's
// half of the fixture, where the scenario can no longer be the one authority on it.
func TestPactoCRs_FollowTheDeployedRevision(t *testing.T) {
	flip := func(s *Scenario) {
		revs := declared(s, "checkout").Revisions
		revs[0].Deployed, revs[1].Deployed = false, true
	}
	before := projection(t, OperationalGraph)
	after := projection(t, mutate(flip))
	moved(t, before, after, "checkout@sha256:checkout-1.0.0", "checkout@sha256:checkout-1.1.0")
	// Both revisions are still published — only the deployment moved.
	if !strings.Contains(after, "\tcheckout:1.0.0\n") {
		t.Error("checkout 1.0.0 stopped being published when it stopped being deployed")
	}
	planBefore, err := OperationalGraph.Plan("/out")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	planAfter, err := mutate(flip).Plan("/out")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !bytes.Equal(planBefore, planAfter) {
		t.Errorf("moving the deployment moved the shell's plan too:\n%s---\n%s", planBefore, planAfter)
	}
}

func TestPlan_FollowsTheObservationSourceIdentity(t *testing.T) {
	before := projection(t, OperationalGraph)
	after := projection(t, mutate(func(s *Scenario) {
		for i := range s.Sources {
			if s.Sources[i].Kind == SourceObservation {
				s.Sources[i].ID = "orders-otlp"
			}
		}
		for i := range s.Relationships {
			s.Relationships[i].ObservedBy = "orders-otlp"
		}
	}))
	// The id the Product must publish, the ConfigMap the operator mounts and the
	// export file all derive from the one declaration.
	moved(t, before, after, "orders-traces", "orders-otlp")
	if !strings.Contains(after, "\torders-otlp\tpacto-orders-otlp\ttraces.json\t/out/orders-otlp.json\n") {
		t.Error("the observation record did not follow the source id through the ConfigMap and the export path")
	}
}

func TestEvidence_FollowsTheDeclaredSubject(t *testing.T) {
	before := projection(t, OperationalGraph)
	after := projection(t, mutate(func(s *Scenario) {
		s.Evidence[0].Service = "checkout"
	}))
	// The plan record's subject, the payload's Subject and the ContractRef it
	// resolves to all move to the new service.
	moved(t, before, after, `"name": "payments"`, `"name": "checkout"`)
	moved(t, before, after, "oci://"+domain+"/payments@", "oci://"+domain+"/checkout@")
}

func TestEvidence_FollowsTheDeclaredSourceEnvironment(t *testing.T) {
	before := projection(t, OperationalGraph)
	after := projection(t, mutate(func(s *Scenario) {
		s.Evidence[0].Source = "remote-us"
	}))
	// Source and the observation's collector are the same declaration, and neither
	// is the signer: the producer must NOT follow it.
	moved(t, before, after, `"Source": "remote-eu"`, `"Source": "remote-us"`)
	moved(t, before, after, `"collector": "remote-eu"`, `"collector": "remote-us"`)
	if !strings.Contains(after, "\tremote-eu-collector\t") {
		t.Error("changing the environment changed who signs; Source and Signer are different identities")
	}
}

func TestEvidence_FollowsTheDeclaredSigner(t *testing.T) {
	before := projection(t, OperationalGraph)
	after := projection(t, mutate(func(s *Scenario) {
		s.Evidence[0].Signer = Signer{Producer: "eu-fleet-agent", KeyID: "fleet"}
	}))
	moved(t, before, after, "signer\tremote-eu-collector\tdemo\n", "signer\teu-fleet-agent\tfleet\n")
	// The environment is payload data and must NOT follow the signer.
	if !strings.Contains(after, `"Source": "remote-eu"`) {
		t.Error("changing who signs changed where the observations came from")
	}
}

// A fixture the harness cannot install one trust key for must be refused at
// projection time, not discovered as a producer mismatch at ingestion.
func TestPlan_RefusesASecondSigner(t *testing.T) {
	s := mutate(func(s *Scenario) {
		s.Evidence = append(s.Evidence, Evidence{
			Service: "checkout", Source: "remote-us",
			Signer:     Signer{Producer: "us-fleet-agent", KeyID: "demo"},
			ObservedAt: "2026-07-29T12:00:00Z", Via: "evidence-http",
		})
	})
	if _, err := s.Plan("/out"); err == nil || !strings.Contains(err.Error(), "one trust key") {
		t.Errorf("Plan accepted two signers: %v", err)
	}
}

func TestPlan_RejectsAFieldThatCouldForgeARecord(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    func(*Scenario)
		want string
	}{
		{"a tab splits one field into two", func(s *Scenario) {
			s.Services[0].Repo = "pay\tments"
		}, "delimiter"},
		{"a newline ends the record early", func(s *Scenario) {
			s.Services[0].Revisions[0].Dir = "payments\nworkload\tghost\tghost\t0"
		}, "delimiter"},
		{"an empty field shifts every later one left", func(s *Scenario) {
			declared(s, "orders").Workload.Name = ""
		}, "empty field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := mutate(tc.f).Plan("/out"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Plan accepted it: %v", err)
			}
		})
	}
}

func TestPlan_NeedsSomewhereToRenderInto(t *testing.T) {
	if _, err := OperationalGraph.Plan(""); err == nil {
		t.Error("Plan accepted an empty directory")
	}
}

// Deployed is the fixture saying "the workload runs THIS". A declaration that
// cannot mean exactly that has to be refused by every projection, because each
// one used to resolve it independently by taking the FIRST revision flagged
// Deployed — the plan, the CR the harness applies and the Product gate all
// agreeing on one side of a contradiction and erasing the other without a word.
//
// Both projections are asserted for every case: a rule enforced in one of them
// is a rule the other can be reached around.
func TestPlan_RefusesADeploymentThatCannotMeanOneThing(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    func(*Scenario)
		want string
	}{{
		// A workload is the promise that something runs; a CR pinning nothing is the
		// fixture contradicting itself.
		name: "a workload that deploys nothing would pin nothing",
		f: func(s *Scenario) {
			for i := range declared(s, "orders").Revisions {
				declared(s, "orders").Revisions[i].Deployed = false
			}
		},
		want: "deploys no revision",
	}, {
		// The counterexample. Both checkout revisions deployed: the scan returned
		// 1.0.0, so the plan, the CR and the gate silently agreed on it and the
		// second declared deployment simply ceased to exist.
		name: "two deployed revisions would erase one another",
		f: func(s *Scenario) {
			revs := declared(s, "checkout").Revisions
			revs[0].Deployed, revs[1].Deployed = true, true
		},
		want: "exactly one",
	}, {
		// Deployed on a service that runs nothing is deployment semantics acquired
		// by accident: no CR would ever pin it, and the gate would sit out its whole
		// timeout waiting for an operational target the cluster has no reason to
		// produce.
		name: "a service that runs nothing cannot deploy anything",
		f: func(s *Scenario) {
			declared(s, "payments").Revisions[0].Deployed = true
		},
		want: "runs no workload",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			s := mutate(tc.f)
			if _, err := s.Plan("/out"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Plan accepted it: %v", err)
			}
			if _, err := s.PactoCRs("demo", domain, digestsFor(s)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("PactoCRs accepted it: %v", err)
			}
		})
	}
}

// The CRs are marshalled from typed values, so this parses them back rather than
// matching strings: a target the operator cannot read is not a projection.
func TestPactoCRs_DeclareEveryRunningWorkloadAndOnlyThose(t *testing.T) {
	body, err := OperationalGraph.PactoCRs("demo", domain, digestsFor(OperationalGraph))
	if err != nil {
		t.Fatalf("PactoCRs: %v", err)
	}
	got := map[string]pactoCR{}
	dec := yaml.NewDecoder(strings.NewReader(string(body)))
	for {
		var cr pactoCR
		if err := dec.Decode(&cr); err != nil {
			break
		}
		if cr.Metadata.Namespace != "demo" {
			t.Errorf("%s: namespace %q", cr.Metadata.Name, cr.Metadata.Namespace)
		}
		got[cr.Metadata.Name] = cr
	}
	for _, svc := range OperationalGraph.Services {
		cr, ok := got[svc.Name]
		if svc.Workload == nil {
			if ok {
				t.Errorf("%s runs nothing but was given a CR", svc.Name)
			}
			continue
		}
		if !ok {
			t.Fatalf("%s runs a workload but was given no CR", svc.Name)
		}
		rev, err := svc.DeployedRevision()
		if err != nil {
			t.Fatalf("%s runs a workload but has no one deployed revision: %v", svc.Name, err)
		}
		if want := domain + "/" + svc.Repo + "@sha256:" + svc.Name + "-" + rev.Version; cr.Spec.ContractRef.OCI != want {
			t.Errorf("%s pins %q, want %q", svc.Name, cr.Spec.ContractRef.OCI, want)
		}
		// An unexposed workload gets no Service and no binding, so the operator's
		// only honest verdict on interface availability stays Unknown.
		if svc.Workload.Port == 0 {
			if cr.Spec.Target.ServiceName != "" || len(cr.Spec.Target.InterfaceBindings) > 0 {
				t.Errorf("%s is unexposed but its CR claims a Service or a binding", svc.Name)
			}
			continue
		}
		want := crInterfaceBinding{Interface: svc.Workload.Interface, ServicePort: svc.Workload.Port}
		if cr.Spec.Target.ServiceName != svc.Workload.Name || len(cr.Spec.Target.InterfaceBindings) != 1 ||
			cr.Spec.Target.InterfaceBindings[0] != want {
			t.Errorf("%s: target %+v does not bind %+v", svc.Name, cr.Spec.Target, want)
		}
	}
}

func TestPactoCRs_NeedANamespaceADomainAndTheDigests(t *testing.T) {
	if _, err := OperationalGraph.PactoCRs("", domain, digestsFor(OperationalGraph)); err == nil {
		t.Error("PactoCRs accepted an empty namespace")
	}
	if _, err := OperationalGraph.PactoCRs("demo", "", digestsFor(OperationalGraph)); err == nil {
		t.Error("PactoCRs accepted an empty domain")
	}
	// The push has to have happened first; a CR pinned to a missing digest would
	// send the operator after content nothing ever published.
	if _, err := OperationalGraph.PactoCRs("demo", domain, nil); err == nil {
		t.Error("PactoCRs projected a CR without a published digest")
	}
}

// The envelope has to describe the subject's REAL contract, read from the bundle
// the scenario publishes rather than asserted alongside it.
func TestEvidencePayloads_ReportTheSubjectsOwnWorkload(t *testing.T) {
	payloads, err := OperationalGraph.EvidencePayloads("/out", domain, digestsFor(OperationalGraph))
	if err != nil {
		t.Fatalf("EvidencePayloads: %v", err)
	}
	if len(payloads) != len(OperationalGraph.Evidence) {
		t.Fatalf("got %d payloads for %d declared envelopes", len(payloads), len(OperationalGraph.Evidence))
	}
	for path, body := range payloads {
		var set struct {
			Subject      struct{ Name string }
			ContractRef  string
			Source       string
			Observations []struct {
				Outcome string `json:"outcome"`
				Value   struct {
					Type string `json:"type"`
				} `json:"value"`
			}
		}
		if err := json.Unmarshal(body, &set); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		svc, ok := OperationalGraph.Service(set.Subject.Name)
		if !ok {
			t.Fatalf("%s: subject %q is not declared", path, set.Subject.Name)
		}
		if len(set.Observations) != 1 || set.Observations[0].Outcome != "Observed" {
			t.Fatalf("%s: %d observations", path, len(set.Observations))
		}
		c := parseBundle(t, mustMaterialize(t), svc.Revisions[0].Dir)
		if got := set.Observations[0].Value.Type; got != c.Workload {
			t.Errorf("%s: reports workload %q, the bundle declares %q", path, got, c.Workload)
		}
	}
}

func TestEvidencePayloads_RefuseAnUndeclaredOrUnparseableEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    func(*Scenario)
	}{
		{"a subject nothing publishes", func(s *Scenario) { s.Evidence[0].Service = "ghost" }},
		{"no source environment", func(s *Scenario) { s.Evidence[0].Source = "" }},
		{"an unparseable ObservedAt", func(s *Scenario) { s.Evidence[0].ObservedAt = "yesterday" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := mutate(tc.f)
			if _, err := s.EvidencePayloads("/out", domain, digestsFor(s)); err == nil {
				t.Error("EvidencePayloads accepted it")
			}
		})
	}
	if _, err := OperationalGraph.EvidencePayloads("", domain, nil); err == nil {
		t.Error("EvidencePayloads accepted an empty directory")
	}
	if _, err := OperationalGraph.EvidencePayloads("/out", "", nil); err == nil {
		t.Error("EvidencePayloads accepted an empty domain")
	}
	if _, err := OperationalGraph.EvidencePayloads("/out", domain, nil); err == nil {
		t.Error("EvidencePayloads accepted a subject with no published digest")
	}
}

// mustMaterialize renders the fixture once for a test that has to read real
// bundle content back.
func mustMaterialize(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := OperationalGraph.Materialize(dir, domain); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	return dir
}
