package publish

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// deriveBundle is a 2.5 payments bundle at version ("" leaves it
// undeclared) with workload and, when health, a health capability.
func deriveBundle(host, version, workload string, health bool) *contract.Bundle {
	y := "pactoVersion: \"2.5\"\nservice:\n  name: payments\n"
	if version != "" {
		y += "  version: " + version + "\n"
	}
	y += "workload: " + workload + "\n"
	if health {
		y += "capabilities: [{type: health}]\n"
	}
	y += "publish:\n  bundle: oci://" + host + "/pacto/payments\n  sign: {key: cosign.key}\n"
	c, err := contract.Parse(strings.NewReader(y))
	if err != nil {
		panic(err)
	}
	return &contract.Bundle{Contract: c, RawYAML: []byte(y), FS: fstest.MapFS{"pacto.yaml": {Data: []byte(y)}}}
}

// signedSet verifies the digests it holds.
type signedSet map[string]bool

func (s signedSet) verify(_ context.Context, ref string) error {
	if !s[ref] {
		return errTest
	}
	return nil
}

func never(*contract.Bundle, *contract.Bundle) (bool, string) { return false, "" }

// deriveFrom pushes each version (signed unless listed in unsigned) and
// derives local's version.
func deriveFrom(t *testing.T, local func(host string) *contract.Bundle, versions []string, unsigned ...string) (Derived, error) {
	t.Helper()
	host, reg := newRegistry(t)
	signed := signedSet{}
	for _, v := range versions {
		d := pushBundle(t, reg, host, v, deriveBundle(host, v, workloadService, true))
		signed[host+"/pacto/payments@"+d] = !slices.Contains(unsigned, v)
	}
	return DeriveVersion(context.Background(), reg, DeriveInput{Repo: host + "/pacto/payments", Local: local(host), Unchanged: never, Verify: signed.verify})
}

func same(workload string, health bool) func(string) *contract.Bundle {
	return func(host string) *contract.Bundle { return deriveBundle(host, "", workload, health) }
}

func TestDeriveVersionNonBreaking(t *testing.T) {
	d, err := deriveFrom(t, same(workloadService, true), []string{"1.4.2"})
	if err != nil || d.Version != "1.4.3" || d.Baseline != "1.4.2" || d.Bump != "patch" || d.Classification != "NON_BREAKING" || d.BaselineDigest == "" {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestDeriveVersionPotentialBreaking(t *testing.T) {
	d, err := deriveFrom(t, same(workloadService, false), []string{"1.4.2"})
	if err != nil || d.Version != "1.5.0" || d.Classification != "POTENTIAL_BREAKING" {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestDeriveVersionBreaking(t *testing.T) {
	d, err := deriveFrom(t, same("job", true), []string{"2.3.0"})
	if err != nil || d.Version != "3.0.0" || d.Bump != "major" {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestDeriveVersionBreakingBelowOne(t *testing.T) {
	d, err := deriveFrom(t, same("job", true), []string{"0.4.0"})
	if err != nil || d.Version != "0.5.0" || d.Bump != "minor" {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestDeriveVersionFirstPublish(t *testing.T) {
	d, err := deriveFrom(t, same(workloadService, true), nil)
	if err != nil || d.Version != FirstVersion || d.Baseline != "" || d.Unchanged {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestDeriveVersionUnchangedReturnsBaseline(t *testing.T) {
	host, reg := newRegistry(t)
	digest := pushBundle(t, reg, host, "1.4.2", deriveBundle(host, "1.4.2", workloadService, true))
	d, err := DeriveVersion(context.Background(), reg, DeriveInput{Repo: host + "/pacto/payments", Local: deriveBundle(host, "", workloadService, true),
		Unchanged: func(*contract.Bundle, *contract.Bundle) (bool, string) { return true, "same" },
		Verify:    signedSet{host + "/pacto/payments@" + digest: true}.verify})
	if err != nil || !d.Unchanged || d.Version != "1.4.2" || d.BaselineDigest != digest {
		t.Fatalf("%+v %v", d, err)
	}
}

// TestDeriveVersionUnreadableBaseline: a registry that cannot be read is an
// error at every step, never a first publish.
func TestDeriveVersionUnreadableBaseline(t *testing.T) {
	for _, fail := range []string{"tags", "head", "pull"} {
		host, reg := newRegistry(t)
		digest := pushBundle(t, reg, host, "1.4.2", deriveBundle(host, "1.4.2", workloadService, true))
		d, err := DeriveVersion(context.Background(), brokenRegistry{reg, fail}, DeriveInput{Repo: host + "/pacto/payments",
			Local: deriveBundle(host, "", workloadService, true), Unchanged: never, Verify: signedSet{host + "/pacto/payments@" + digest: true}.verify})
		if !errors.Is(err, errTest) || d.Version != "" {
			t.Errorf("%s: %+v %v", fail, d, err)
		}
	}
}

// TestDeriveVersionCandidateAbove: a candidate above the baseline judges the
// derived version too, so the publish that follows never refuses it.
func TestDeriveVersionCandidateAbove(t *testing.T) {
	host, reg := newRegistry(t)
	signed := signedSet{}
	signed[host+"/pacto/payments@"+pushBundle(t, reg, host, "1.4.2", deriveBundle(host, "1.4.2", workloadService, true))] = true
	// The candidate changed the workload; the folder changes it back, which
	// breaks the candidate line, and the derived 1.5.0 is only a minor.
	pushBundle(t, reg, host, "1.4.3-rc.1", deriveBundle(host, "1.4.3-rc.1", "job", true))
	in := DeriveInput{Repo: host + "/pacto/payments", Local: deriveBundle(host, "", workloadService, false), Unchanged: never, Verify: signed.verify}
	_, err := DeriveVersion(context.Background(), reg, in)
	if err == nil || !strings.Contains(err.Error(), "holds candidate 1.4.3-rc.1 above 1.4.2, and the derived 1.5.0 does not satisfy it") ||
		!strings.Contains(err.Error(), "declare service.version to publish a candidate line") {
		t.Fatalf("candidate refused: %v", err)
	}
	// A candidate the derived version satisfies lets it through.
	host2, reg2 := newRegistry(t)
	signed2 := signedSet{}
	signed2[host2+"/pacto/payments@"+pushBundle(t, reg2, host2, "1.4.2", deriveBundle(host2, "1.4.2", workloadService, true))] = true
	pushBundle(t, reg2, host2, "1.5.0-rc.1", deriveBundle(host2, "1.5.0-rc.1", workloadService, false))
	d, err := DeriveVersion(context.Background(), reg2, DeriveInput{Repo: host2 + "/pacto/payments", Local: deriveBundle(host2, "", workloadService, false), Unchanged: never, Verify: signed2.verify})
	if err != nil || d.Version != "1.5.0" {
		t.Fatalf("candidate satisfied: %+v %v", d, err)
	}
}

func TestDeriveVersionIgnoresUnsignedHigherTag(t *testing.T) {
	d, err := deriveFrom(t, same(workloadService, true), []string{"1.4.2", "9.9.9"}, "9.9.9")
	if err != nil || d.Baseline != "1.4.2" || d.Version != "9.9.10" || !slices.Contains(d.Notes, "ignored 9.9.9: no signature by the publisher identity") {
		t.Fatalf("%+v %v", d, err)
	}
	// An unsigned tag whose content the change breaks refuses the derived
	// version rather than let the publish refuse it.
	_, err = deriveFrom(t, same(workloadService, false), []string{"1.4.2", "9.9.9"}, "9.9.9")
	if err == nil || !strings.Contains(err.Error(), "holds 9.9.9 above 1.4.2") || !strings.Contains(err.Error(), ": declare service.version to publish") {
		t.Fatalf("unsigned tag the change breaks: %v", err)
	}
}

func TestDeriveVersionNoVerifiableBaselineFails(t *testing.T) {
	_, err := deriveFrom(t, same(workloadService, true), []string{"1.4.2", "1.5.0-rc.1"}, "1.4.2")
	if err == nil || !strings.Contains(err.Error(), "no verifiable baseline in ") {
		t.Fatalf("%v", err)
	}
}

func TestDeriveVersionLayoutMovedFails(t *testing.T) {
	host, reg := newRegistry(t)
	pushBundle(t, reg, host, "1.4.2", deriveBundle(host, "1.4.2", workloadService, true))
	in := DeriveInput{Repo: host + "/contracts/payments", AltRepo: host + "/pacto/payments", Local: deriveBundle(host, "", workloadService, true), Unchanged: never}
	_, err := DeriveVersion(context.Background(), reg, in)
	if err == nil || !strings.Contains(err.Error(), "bundle repository moved: "+host+"/pacto/payments holds versions") {
		t.Fatalf("%v", err)
	}
	// An alternative repository that cannot be listed is an error too.
	if _, err := DeriveVersion(context.Background(), altTagsFail{reg, in.AltRepo}, in); !errors.Is(err, errTest) {
		t.Fatalf("alt unreadable: %v", err)
	}
	// An empty alternative is a first publish.
	in.Repo, in.AltRepo = host+"/a/payments", host+"/b/payments"
	if d, err := DeriveVersion(context.Background(), reg, in); err != nil || d.Version != FirstVersion {
		t.Fatalf("both empty: %+v %v", d, err)
	}
}

// altTagsFail fails listing one repository.
type altTagsFail struct {
	*Client
	repo string
}

func (a altTagsFail) Tags(ctx context.Context, repo string) ([]string, error) {
	if repo == a.repo {
		return nil, errTest
	}
	return a.Client.Tags(ctx, repo)
}

// ghostTag lists a tag the registry no longer holds.
type ghostTag struct{ *Client }

func (g ghostTag) Tags(ctx context.Context, repo string) ([]string, error) {
	tags, err := g.Client.Tags(ctx, repo)
	return append(tags, "1.9.0"), err
}

// TestDeriveVersionSkipsAVanishedTag: a tag gone since the listing is no
// baseline and no occupied version.
func TestDeriveVersionSkipsAVanishedTag(t *testing.T) {
	host, reg := newRegistry(t)
	digest := pushBundle(t, reg, host, "1.4.2", deriveBundle(host, "1.4.2", workloadService, true))
	d, err := DeriveVersion(context.Background(), ghostTag{reg}, DeriveInput{Repo: host + "/pacto/payments", Local: deriveBundle(host, "", workloadService, true),
		Unchanged: never, Verify: signedSet{host + "/pacto/payments@" + digest: true}.verify})
	if err != nil || d.Baseline != "1.4.2" {
		t.Fatalf("%+v %v", d, err)
	}
}

// TestDeriveVersionBaselineRevision: the baseline's revision is its deploy
// artifact's, else the commit its record names.
func TestDeriveVersionBaselineRevision(t *testing.T) {
	host, reg := newRegistry(t)
	tools := newTools()
	if _, err := Apply(context.Background(), reg, tools, Input{Bundle: bundleOf(host, "1.4.2", workloadService), Revision: "rev1"}); err != nil {
		t.Fatal(err)
	}
	local := bundleOf(host, "1.4.2", workloadService)
	local.Contract.Service.Version = ""
	d, err := DeriveVersion(context.Background(), reg, DeriveInput{Repo: host + "/pacto/payments", Local: local, Unchanged: never,
		Verify: func(ctx context.Context, ref string) error { return tools.Verify(ctx, contract.PublishSign{}, ref) }})
	if err != nil || d.BaselineRevision != "rev1" || d.BaselineCreated != "" {
		t.Fatalf("deploy annotation: %+v %v", d, err)
	}
	rec := recordBundle(host, "1.4.2", "1", "src", false)
	rec.Contract.Publish = &contract.Publish{Bundle: "oci://" + host + "/pacto/payments", Deploy: "manifests",
		Artifacts: []contract.PublishArtifact{{Name: "chart", Ref: "oci://" + host + "/charts/payments"}, {Name: "manifests", Ref: "oci://" + host + "/nothing"}}}
	rev, created := baselineRevision(context.Background(), reg, rec, "1.4.2")
	if rev != "" || created != "" {
		t.Fatalf("record without commit: %q %q", rev, created)
	}
	r := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Commit: "abc", Pacto: "x", Discovery: "1"}}
	rec.FS = fstest.MapFS{contract.InferredRecordFile: {Data: r.Marshal()}}
	rec.Contract.Publish = nil
	if rev, _ := baselineRevision(context.Background(), reg, rec, "1.4.2"); rev != "abc" {
		t.Fatalf("record commit: %q", rev)
	}
}
