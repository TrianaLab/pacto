package pactify

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
)

// TestProposedRange: ^MAJOR.MINOR.0 of the lowest strict-semver version,
// never widened across majors, and nothing when there is no version.
func TestProposedRange(t *testing.T) {
	for _, tt := range []struct {
		versions []string
		want     string
	}{
		{nil, ""},
		{[]string{"v2.2"}, ""},
		{[]string{"2.3.1"}, "^2.3.0"},
		{[]string{"2.4.0", "2.3.1"}, "^2.3.0"},
		{[]string{"2.3.1", "3.0.0"}, "^2.3.0"},
		{[]string{"2.4.0-rc.1"}, "^2.4.0"},
		{[]string{"0.3.2"}, "^0.3.0"},
	} {
		if got := proposedRange(tt.versions); got != tt.want {
			t.Errorf("proposedRange(%v) = %q, want %q", tt.versions, got, tt.want)
		}
	}
}

// TestVersionList: the versions a comment lists, with no Oxford comma.
func TestVersionList(t *testing.T) {
	for want, vs := range map[string][]string{
		"2.3.1":                  {"2.3.1"},
		"2.3.1 and 2.4.0":        {"2.3.1", "2.4.0"},
		"2.3.1, 2.3.4 and 2.4.0": {"2.3.1", "2.3.4", "2.4.0"},
	} {
		if got := joinAnd(vs); got != want {
			t.Errorf("joinAnd(%v) = %q, want %q", vs, got, want)
		}
	}
}

// TestAscending: versions sorted, repeats dropped, and a value that is not a
// strict version skipped instead of panicking.
func TestAscending(t *testing.T) {
	got := ascending([]string{"2.4.0", "latest", "2.3.1", "2.4.0", "v1.0.0"})
	if !slices.Equal(got, []string{"2.3.1", "2.4.0"}) {
		t.Fatalf("ascending = %v", got)
	}
}

// noGuess fails when a written text claims what was not seen.
func noGuess(t *testing.T, r *Result) {
	t.Helper()
	for _, s := range append(append(append([]string{file(t, r, "pacto.yaml").Content}, r.Found...), r.Unknown...), r.Next...) {
		if strings.Contains(s, "observed calling") || strings.Contains(s, "seen running") {
			t.Fatalf("claims what was not seen: %q", s)
		}
	}
}

// TestObservedDependencyIsProposedNotDeclared: a dependency seen at runtime
// is written with compatibility unknown and the proposal in a comment.
func TestObservedDependencyIsProposedNotDeclared(t *testing.T) {
	inferred := Inferred{Name: "payments", Ref: paymentsRef, Versions: []string{"2.3.1", "2.4.0"}, Where: "calls seen to the Service shop/payments"}
	r, err := Init(context.Background(), Options{Dir: fixtureDir(t, "invoices"), Name: "checkout", DescribeOnly: true, Plan: true, Inferred: []Inferred{inferred}}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "  # inferred from runtime calls (calls seen to the Service shop/payments)\n  - name: payments\n    ref: " + paymentsRef + "\n    required: true\n" +
		"    compatibility: unknown   # proposed ^2.3.0, from the Pactos describing the Service shop/payments at 2.3.1 and 2.4.0\n"
	if text := file(t, r, "pacto.yaml").Content; !strings.Contains(text, want) {
		t.Fatalf("pacto.yaml:\n%s", text)
	}
	if !hasLine(r.Found, "checkout calls the Service shop/payments, described by payments 2.3.1 and 2.4.0") ||
		!hasLine(r.Unknown, "the versions of payments that checkout accepts: pacto.yaml leaves compatibility unknown, so impact cannot tell whether a payments change breaks checkout: "+
			"its verdict for checkout is unknown, and a BREAKING change to payments stays unverified; the proposal ^2.3.0 comes from the lowest version the Pactos describing it report") ||
		!hasLine(r.Next, "declare the versions of payments that checkout accepts: replace compatibility: unknown with a range, such as the proposed ^2.3.0") {
		t.Fatalf("plan: %+v", r)
	}
	noGuess(t, r)
}

// TestInferredUnknownNotesArePrinted: what kept a range from being proposed
// is printed, and a dependency whose Pactos name different contracts keeps
// the ref they name and says why no range is proposed, even when --fleet
// holds a contract by that name.
func TestInferredUnknownNotesArePrinted(t *testing.T) {
	note := "the version of shop/payments to propose a range from: the Pactos describing it name different contracts (a and b), so no range is proposed"
	inferred := Inferred{Name: "payments", Ref: paymentsRef, Where: "calls seen to the Service shop/payments", Unknown: []string{note}, conflict: true}
	fleetDir := writeTree(t, t.TempDir(), map[string]string{
		"payments/pacto.yaml": "pactoVersion: \"2.1\"\nservice: {name: payments, version: 3.0.0}\npublish: {bundle: oci://reg.example/payments, sign: {key: k}}\n",
	})
	r, err := Init(context.Background(), Options{Dir: fixtureDir(t, "invoices"), DescribeOnly: true, Plan: true, FleetRoots: []string{fleetDir}, Inferred: []Inferred{inferred}}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	text := file(t, r, "pacto.yaml").Content
	want := "  # inferred from runtime calls (calls seen to the Service shop/payments)\n  - name: payments\n    ref: " + paymentsRef + "\n    required: true\n" +
		"    compatibility: unknown   # no range proposed: the Pactos describing the Service shop/payments name different contracts\n"
	if !hasLine(r.Unknown, note) || !strings.Contains(text, want) || strings.Contains(text, "no contract for payments was read") || strings.Contains(text, "^3.0.0") ||
		hasLine(r.Next, "such as the proposed") {
		t.Fatalf("plan: %+v\n%s", r, text)
	}
}

// TestNotReconciledDependencyHasNoProposal: a dependency whose only Pacto is
// not reconciled says so, with its ref where it would be published.
func TestNotReconciledDependencyHasNoProposal(t *testing.T) {
	note := "the version of shop/payments that Pacto shop/payments describes: its status is not reconciled with its spec, so no range is proposed from it"
	inferred := Inferred{Name: "payments", Where: "calls seen to the Service shop/payments", Unknown: []string{note}}
	r, err := Init(context.Background(), Options{Dir: fixtureDir(t, "invoices"), DescribeOnly: true, Plan: true, Inferred: []Inferred{inferred}}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	text := file(t, r, "pacto.yaml").Content
	want := "  # inferred from runtime calls (calls seen to the Service shop/payments)\n  # no Pacto describing it names a contract ref: ref is where it would be published\n" +
		"  - name: payments\n    ref: oci://ghcr.io/acme/contracts/payments\n    required: true\n" +
		"    compatibility: unknown   # no range proposed: no reconciled Pacto describing the Service shop/payments reports a contract version\n"
	if !hasLine(r.Unknown, note) || !strings.Contains(text, want) || !hasLine(r.Found, "invoices calls the Service shop/payments") {
		t.Fatalf("plan: %+v\n%s", r, text)
	}
}

// TestProposalNamesOnlyDescribedServices: a proposal merged from a Service
// no Pacto describes names only the Services whose Pactos reported.
func TestProposalNamesOnlyDescribedServices(t *testing.T) {
	inferred := Inferred{Name: "payments", Ref: paymentsRef, Versions: []string{"2.3.1"}, Where: "calls seen to the Services other/payments and shop/payments",
		described: []string{"shop/payments"}}
	r, err := Init(context.Background(), Options{Dir: fixtureDir(t, "invoices"), Name: "checkout", DescribeOnly: true, Plan: true, Inferred: []Inferred{inferred}}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if text := file(t, r, "pacto.yaml").Content; !strings.Contains(text, "compatibility: unknown   # proposed ^2.3.0, from the Pactos describing the Service shop/payments at 2.3.1\n") ||
		!hasLine(r.Found, "checkout calls the Services other/payments and shop/payments, and the Pactos describing the Service shop/payments report payments 2.3.1") {
		t.Fatalf("plan: %+v\n%s", r, text)
	}
}

// TestFleetDependencyIsProposedNotDeclared: a dependency whose contract is
// in --fleet gets a proposal from that contract's version.
func TestFleetDependencyIsProposedNotDeclared(t *testing.T) {
	fleetDir := writeTree(t, t.TempDir(), map[string]string{
		"ledger/pacto.yaml": "pactoVersion: \"2.1\"\nservice: {name: ledger, version: 1.6.2}\npublish: {bundle: oci://reg.example/finance/ledger, sign: {key: k}}\n",
	})
	r, err := Init(context.Background(), Options{Dir: fixtureDir(t, "invoices"), DescribeOnly: true, FleetRoots: []string{fleetDir}, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if text := file(t, r, "pacto.yaml").Content; !strings.Contains(text, "  # from k8s/configmap.yaml, env LEDGER_URL\n  - name: ledger\n    ref: oci://reg.example/finance/ledger\n    required: true\n"+
		"    compatibility: unknown   # proposed ^1.6.0, from its contract in --fleet at 1.6.2\n") {
		t.Fatalf("pacto.yaml:\n%s", text)
	}
	if !hasLine(r.Unknown, "; the proposal ^1.6.0 comes from its contract in --fleet") || !hasLine(r.Next, "such as the proposed ^1.6.0") {
		t.Fatalf("plan: %+v", r)
	}
	noGuess(t, r)
}

// TestUnreadDependencyHasNoProposal: with no contract read, the comment says
// no range is proposed and next suggests none.
func TestUnreadDependencyHasNoProposal(t *testing.T) {
	r, err := Init(context.Background(), Options{Dir: fixtureDir(t, "invoices"), DescribeOnly: true, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if text := file(t, r, "pacto.yaml").Content; !strings.Contains(text, "    compatibility: unknown   # no range proposed: no contract or running version of ledger was read\n") {
		t.Fatalf("pacto.yaml:\n%s", text)
	}
	if !hasLine(r.Next, "declare the versions of ledger that invoices accepts: replace compatibility: unknown with a range") || hasLine(r.Next, "such as") {
		t.Fatalf("next: %q", r.Next)
	}
}

// TestPactoVersionRaisedForUndeclaredRange: every contract init writes is at
// the current pactoVersion, the first that leaves service.version to pacto ci
// --publish, whatever else it holds.
func TestPactoVersionRaisedForUndeclaredRange(t *testing.T) {
	plain := writeTree(t, t.TempDir(), map[string]string{"Dockerfile": "FROM scratch\n"})
	inferred := []Inferred{{Name: "payments", Ref: paymentsRef, Versions: []string{"2.3.1"}, Where: "calls seen to the Service shop/payments"}}
	for _, tt := range []struct {
		name string
		opts Options
		want string
	}{
		{"dependency", Options{Dir: fixtureDir(t, "invoices")}, "2.5"},
		{"none", Options{Dir: plain}, "2.5"},
		{"domain without dependency", Options{Dir: plain, Domain: "payments"}, "2.5"},
		{"blank with inferred", Options{Dir: t.TempDir(), Name: "blank", Inferred: inferred}, "2.5"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.opts.Plan, tt.opts.DescribeOnly = true, true
			r, err := Init(context.Background(), tt.opts, os.WriteFile)
			if err != nil {
				t.Fatal(err)
			}
			if text := file(t, r, "pacto.yaml").Content; !strings.Contains(text, "pactoVersion: \""+tt.want+"\"\n") {
				t.Fatalf("pacto.yaml:\n%s", text)
			}
		})
	}
}

// writtenRange is a compatibility line pactify writes: unknown, then the
// proposal or why there is none in a comment.
var writtenRange = regexp.MustCompile(`^ +compatibility: unknown +# \S`)

func checkNoRangeDeclared(t *testing.T, label string, results ...*Result) {
	t.Helper()
	n := 0
	for _, r := range results {
		for _, f := range r.Files {
			if filepath.Base(f.Path) != "pacto.yaml" {
				continue
			}
			for _, line := range strings.Split(f.Content, "\n") {
				if strings.Contains(line, "compatibility:") {
					n++
					if !writtenRange.MatchString(line) {
						t.Fatalf("%s declares a range: %q", label, line)
					}
				}
			}
		}
	}
	if n == 0 {
		t.Fatalf("%s wrote no dependency", label)
	}
}

// TestNoInferredRangeIsDeclared: every pactify path writes compatibility
// unknown for every dependency it works out.
func TestNoInferredRangeIsDeclared(t *testing.T) {
	ctx := context.Background()
	fleetDir := writeTree(t, t.TempDir(), map[string]string{
		"ledger/pacto.yaml": "pactoVersion: \"2.1\"\nservice: {name: ledger, version: 1.6.2}\npublish: {bundle: oci://reg.example/finance/ledger, sign: {key: k}}\n",
	})
	for label, opts := range map[string]Options{
		"folder":        {Dir: fixtureDir(t, "invoices")},
		"fleet":         {Dir: fixtureDir(t, "invoices"), FleetRoots: []string{fleetDir}},
		"describe-only": {Dir: fixtureDir(t, "invoices"), DescribeOnly: true},
	} {
		opts.Plan = true
		r, err := Init(ctx, opts, os.WriteFile)
		if err != nil {
			t.Fatal(label, err)
		}
		checkNoRangeDeclared(t, label, r)
	}
	many, err := Many(ctx, Options{Plan: true}, []string{fixtureDir(t, "invoices"), fixtureDir(t, "invoices")}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	checkNoRangeDeclared(t, "many", many.Results...)
	now := time.Now()
	for label, objs := range map[string][]runtime.Object{"cluster": shopCluster(now), "cluster with two versions": twoPaymentServices(now, paymentsRef, false)} {
		c := ClusterOptions{Namespaces: []string{"shop"}, Out: t.TempDir(), Now: func() time.Time { return now }}
		b, err := Cluster(ctx, fakeCluster(objs...), Options{Plan: true}, c, nil)
		if err != nil {
			t.Fatal(label, err)
		}
		checkNoRangeDeclared(t, label, b.Results...)
	}
}

// TestDeclaredByNamesUndeclaredRange: a --fleet consumer that leaves its
// range unknown reads range not declared under Used by, whether the fleet
// holds this service's contract or the consumer names it only by its bundle.
func TestDeclaredByNamesUndeclaredRange(t *testing.T) {
	billing := "pactoVersion: \"2.5\"\nservice: {name: billing, version: 1.0.0}\n" +
		"dependencies:\n  - {name: inv, ref: oci://ghcr.io/acme/contracts/invoices, required: true, compatibility: unknown}\n"
	for label, tree := range map[string]map[string]string{
		"with its contract": {"invoices/pacto.yaml": "pactoVersion: \"2.1\"\nservice: {name: invoices, version: 1.3.0}\n", "billing/pacto.yaml": billing},
		"by bundle only":    {"billing/pacto.yaml": billing},
	} {
		r, err := Init(context.Background(), Options{Dir: fixtureDir(t, "invoices"), FleetRoots: []string{writeTree(t, t.TempDir(), tree)}, Plan: true}, os.WriteFile)
		if err != nil {
			t.Fatal(label, err)
		}
		if !slices.Contains(r.Consumers, Finding{Name: "billing", Where: "declares invoices range not declared"}) {
			t.Errorf("%s: consumers: %+v %+v", label, r.Consumers, r.Unknown)
		}
	}
}
