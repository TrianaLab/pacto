package app

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// rangeOf is the plan's row for dep.
func rangeOf(t *testing.T, p *DeclarePlan, dep string) DeclaredRange {
	t.Helper()
	for _, r := range p.Ranges {
		if r.Dependency == dep {
			return r
		}
	}
	t.Fatalf("no range for %s: %+v", dep, p.Ranges)
	return DeclaredRange{}
}

func TestPlanFirstDeclaration(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	p, d := e.plan(t, e.req("ledger"))
	if p.Version != firstDeclaration || p.Deploy != "none" || p.State != contract.DeclarationDeclared || p.Baseline != nil ||
		p.Signer.Source != signerLogin || p.BundleRepo != e.host+"/acme/invoices" || !p.Publish.Ready || !p.Publish.ContractOnly {
		t.Fatalf("%+v", p)
	}
	checkFirstRanges(t, p)
	if !strings.Contains(string(d.raw), "compatibility: \"^2.3.0\"") || d.record.Declaration.Accepted[0].Range != "^2.3.0" {
		t.Fatalf("%s\n%s", d.raw, d.record.Marshal())
	}
}

// checkFirstRanges: ledger is accepted as proposed, with the version
// observed; audit stays unknown with its proposal.
func checkFirstRanges(t *testing.T, p *DeclarePlan) {
	t.Helper()
	if l := rangeOf(t, p, "ledger"); l != (DeclaredRange{Dependency: "ledger", Compatibility: "^2.3.0", Proposed: "^2.3.0", Observed: "2.3.1", Accepted: true}) {
		t.Fatalf("%+v", l)
	}
	if a := rangeOf(t, p, "audit"); a != (DeclaredRange{Dependency: "audit", Compatibility: contract.CompatibilityUnknown, Proposed: "^1.4.0", Observed: "1.4.0"}) {
		t.Fatalf("%+v", a)
	}
}

// refusal is err's declare refusal code.
func refusal(t *testing.T, err error) *DeclareError {
	t.Helper()
	var de *DeclareError
	if !errors.As(err, &de) {
		t.Fatalf("not a declare refusal: %v", err)
	}
	return de
}

func TestPlanFirstDeclarationWithoutPublishSection(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	p, d := e.plan(t, e.req())
	if d.bundle.Contract.Publish != nil || !p.Publish.Ready || len(p.Publish.Items) != 1 || p.Publish.Items[0].Name != publish.BundleItem {
		t.Fatalf("%+v %+v", d.bundle.Contract.Publish, p.Publish)
	}
}

func TestPlanMissingRepositoryIsFirstDeclaration(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.pushImage(t, e.host+"/acme/other", "1.0.0") // the registry holds other repositories only
	p, _ := e.plan(t, e.req())
	if p.Version != firstDeclaration || p.Deploy != "none" || len(p.Skipped) != 0 {
		t.Fatalf("%+v", p)
	}
}

// dirFolder drafts invoices into a folder as pacto init --cluster does, with
// its pacto.yaml replaced by contract when not empty.
func (e *declareEnv) dirFolder(t *testing.T, contractYAML string, extra map[string]string) string {
	t.Helper()
	out := t.TempDir()
	if _, err := pactify.Cluster(t.Context(), e.dyn, pactify.Options{}, pactify.ClusterOptions{Namespaces: []string{"billing"}, Out: out, Workload: "invoices"}, os.WriteFile); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(out, "billing", "invoices")
	if contractYAML != "" {
		extra = maps.Clone(extra)
		if extra == nil {
			extra = map[string]string{}
		}
		extra["pacto.yaml"] = contractYAML
	}
	for p, c := range extra {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.dyn.ClearActions()
	return dir
}

// withBaseline publishes invoices 1.4.0, signed by acme's release workflow,
// and its deploy artifact.
func (e *declareEnv) withBaseline(t *testing.T) (bundle, deploy string) {
	t.Helper()
	ci := e.signer(t, acmeCIToken())
	d := e.publishBundle(t, e.host+"/acme/invoices", "1.4.0", e.invoicesBaseline("1.4.0", ""), &ci)
	dep := e.pushImage(t, e.host+"/acme/deploy/invoices", "1.4.0")
	return e.host + "/acme/invoices@" + d, e.host + "/acme/deploy/invoices@" + dep
}

// ledgerDependency is a dependency on ledger the baseline does not declare.
const ledgerDependency = "dependencies:\n  - name: ledger\n    ref: oci://reg.io/acme/contracts/ledger\n    required: true\n    compatibility: unknown\n"

func TestPlanWithBaselineBumps(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	bundle, deploy := e.withBaseline(t)
	req := e.req()
	req.Dir = e.dirFolder(t, e.invoicesBaseline("1.4.0", ledgerDependency)["pacto.yaml"], nil)
	p, d := e.plan(t, req)
	// A new required dependency is a non-breaking change: a patch.
	if p.Version != "1.4.1" || p.Publish.Classification != "NON_BREAKING" || p.Baseline == nil || p.Baseline.Version != "1.4.0" || p.Baseline.Bundle != bundle || p.Deploy != deploy ||
		p.Baseline.Signer != "https://github.com/acme/invoices/.github/workflows/release.yml@refs/heads/main (https://token.actions.githubusercontent.com)" {
		t.Fatalf("%+v %+v", p, p.Baseline)
	}
	if d.bundle.Contract.Publish == nil || d.record.Declaration.Baseline.Deploy != deploy || p.Publish.Deploy != deploy || p.Owner != "team:billing" {
		t.Fatalf("%+v\n%s", d.bundle.Contract.Publish, d.record.Marshal())
	}
}

func TestPlanWithBaselineReplacesDeclaredVersion(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withBaseline(t)
	req := e.req()
	req.Dir = e.dirFolder(t, e.invoicesBaseline("1.4.0", ledgerDependency)["pacto.yaml"], nil)
	p, d := e.plan(t, req)
	// The folder's pacto.yaml declares the baseline's 1.4.0.
	if d.bundle.Contract.Service.Version != "1.4.1" || p.Version != "1.4.1" || !p.Publish.Ready || p.Owner != "team:billing" ||
		!strings.Contains(string(d.raw), "  version: 1.4.1\n") {
		t.Fatalf("%+v\n%s", p, d.raw)
	}
}

func TestPlanBaselineWrittenIntoWorkloadFolder(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withBaseline(t)
	var scratch string
	var written []string
	e.svc.sys.mkdirTemp = func(dir, pattern string) (string, error) {
		var err error
		scratch, err = os.MkdirTemp(dir, pattern)
		return scratch, err
	}
	e.svc.sys.writeFile = func(name string, data []byte, perm os.FileMode) error {
		if strings.Contains(string(data), "version: 1.4.0") {
			written = append(written, name)
		}
		return os.WriteFile(name, data, perm)
	}
	e.plan(t, e.req())
	if len(written) == 0 || written[0] != filepath.Join(scratch, "billing", "invoices", "pacto.yaml") {
		t.Fatalf("%s: %v", scratch, written)
	}
}

func TestPlanBaselineHigherUnverifiedTagOccupies(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withBaseline(t)
	e.publishBundle(t, e.host+"/acme/invoices", "1.6.0", e.invoicesBaseline("1.6.0", ""), nil)
	req := e.req()
	req.Dir = e.dirFolder(t, e.invoicesBaseline("1.4.0", ledgerDependency)["pacto.yaml"], nil)
	p, _ := e.plan(t, req)
	if p.Baseline.Version != "1.4.0" || p.Version != "1.6.1" || len(p.Skipped) != 1 || p.Skipped[0].Tag != "1.6.0" || p.Skipped[0].Signer != "nobody" {
		t.Fatalf("%+v %+v", p, p.Skipped)
	}
}

func TestPlanUnchangedWritesNothing(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	_, d := e.plan(t, e.req())
	person := e.signer(t, testutil.Token("ana@acme.com", personIssuer)) // a token with an email claim needs email_verified
	files := map[string]string{}
	_ = fs.WalkDir(d.bundle.FS, ".", func(p string, de fs.DirEntry, _ error) error {
		if !de.IsDir() {
			b, _ := fs.ReadFile(d.bundle.FS, p)
			files[p] = string(b)
		}
		return nil
	})
	e.publishBundle(t, e.host+"/acme/invoices", firstDeclaration, files, &person)
	p, again, err := e.svc.PlanDeclare(t.Context(), e.req())
	if err != nil || p.Unchanged != firstDeclaration || p.Version != firstDeclaration || len(p.Publish.Items) != 0 || again != nil ||
		p.Baseline.Signer != "ana@acme.com ("+personIssuer+")" {
		t.Fatalf("%v %+v", err, p)
	}
}

func TestPlanUnverifiedTagsOnly(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.publishBundle(t, e.host+"/acme/invoices", "1.0.0", e.invoicesBaseline("1.0.0", ""), nil)
	e.publishBundle(t, e.host+"/acme/invoices", "2.0.0-rc.1", e.invoicesBaseline("2.0.0-rc.1", ""), nil)
	p, _, err := e.svc.PlanDeclare(t.Context(), e.req())
	if de := refusal(t, err); de.Code != codeVersionConflict || !strings.Contains(de.Message, "1.0.0, signed by nobody") ||
		len(p.Skipped) != 1 || p.Baseline != nil {
		t.Fatalf("%v %+v", err, p)
	}
	e2 := newDeclareEnv(t)
	e2.publishBundle(t, e2.host+"/acme/invoices", "2.0.0-rc.1", e2.invoicesBaseline("2.0.0-rc.1", ""), nil)
	if _, _, err := e2.svc.PlanDeclare(t.Context(), e2.req()); refusal(t, err).Code != codeVersionConflict || !strings.Contains(err.Error(), "only candidates") {
		t.Fatal(err)
	}
}

func TestPlanBaselineFromUnknownSignerIgnored(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	eve := e.signer(t, testutil.Token("eve@evil.example", personIssuer))
	e.publishBundle(t, e.host+"/acme/invoices", "9.9.0", e.invoicesBaseline("9.9.0", ""), &eve)
	p, _, err := e.svc.PlanDeclare(t.Context(), e.req())
	if de := refusal(t, err); de.Code != codeVersionConflict || p.Baseline != nil || len(p.Skipped) != 1 ||
		p.Skipped[0] != (SkippedTag{Tag: "9.9.0", Signer: "eve@evil.example (" + personIssuer + ")", Why: p.Skipped[0].Why}) || p.Skipped[0].Why == "" {
		t.Fatalf("%v %+v", err, p)
	}
}

func TestPlanBundleRepoMismatchRefused(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	ci := e.signer(t, acmeCIToken())
	files := e.invoicesBaseline("1.4.0", "")
	files["pacto.yaml"] = strings.Replace(files["pacto.yaml"], "/acme/invoices\n", "/other/invoices\n", 1)
	e.publishBundle(t, e.host+"/acme/invoices", "1.4.0", files, &ci)
	if _, _, err := e.svc.PlanDeclare(t.Context(), e.req()); refusal(t, err).Code != codeBundleRepoMismatch {
		t.Fatal(err)
	}
}

func TestPlanBaselineBelow25Refused(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	ci := e.signer(t, acmeCIToken())
	e.publishBundle(t, e.host+"/acme/invoices", "1.4.0", map[string]string{"pacto.yaml": "pactoVersion: \"2.3\"\nservice:\n  name: invoices\n  version: 1.4.0\n"}, &ci)
	_, _, err := e.svc.PlanDeclare(t.Context(), e.req())
	if de := refusal(t, err); de.Code != codeBaselineNeedsMigration || de.CLI != "pacto migrate <dir>" {
		t.Fatal(err)
	}
}

func TestPlanAmbientTokenPlansDraft(t *testing.T) {
	e := newDeclareEnv(t)
	t.Setenv("SIGSTORE_ID_TOKEN", personToken("ana@acme.com"))
	req := e.req("ledger")
	req.OnBehalfOf = "ana"
	p, d := e.plan(t, req)
	dec := d.record.Declaration
	if p.State != contract.DeclarationDraft || p.Signer.Source != signerAmbient || p.Signer.Subject != "ana@acme.com" || p.AcceptCLI != "pacto declare invoices --registry "+e.host+"/acme --from-draft <tag>" ||
		!slices.ContainsFunc(p.Notes, func(n string) bool { return strings.Contains(n, "an ambient OIDC token is set") }) ||
		dec.State != contract.DeclarationDraft || len(dec.Requested) != 1 || len(dec.Accepted) != 0 || dec.OnBehalfOf != "ana" {
		t.Fatalf("%+v\n%s", p, d.record.Marshal())
	}
}

func TestPlanAgentPlansUnsignedDraft(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.req()
	req.Agent = true
	p, d := e.plan(t, req)
	if p.State != contract.DeclarationDraft || d.record.Declaration.Asker != (contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent}) {
		t.Fatalf("%+v", d.record.Declaration)
	}
}

func TestPlanKeyPlansDraft(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	dir := t.TempDir()
	keyFiles(t, dir)
	req := e.req()
	req.Signing.Key = filepath.Join(dir, "cosign.key")
	p, d := e.plan(t, req)
	if p.State != contract.DeclarationDraft || p.Signer.Source != signerKey || d.record.Declaration.Asker.Issuer != signerKey || signerText(p.Signer) != "a key that names no person" {
		t.Fatalf("%+v %+v", p.Signer, d.record.Declaration)
	}
	req.Signing.Key = filepath.Join(dir, "missing.key")
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); err == nil {
		t.Fatal("a missing key planned")
	}
}

func TestPlanNoRegistryRefused(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.req()
	req.Registry = ""
	_, _, err := e.svc.PlanDeclare(t.Context(), req)
	if de := refusal(t, err); de.Code != codeNoRegistry || len(e.dyn.Actions()) != 0 {
		t.Fatalf("%v %v", err, e.dyn.Actions())
	}
}

func TestPlanWorkloadNotFound(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.req()
	req.Service = "missing"
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); refusal(t, err).Code != codeWorkloadNotFound {
		t.Fatal(err)
	}
}

func TestPlanWorkloadAmbiguous(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t, append(billing(time.Now()), kubeDeployment("other", "invoices", ""))...)
	req := e.req()
	req.Namespace = ""
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); refusal(t, err).Code != codeWorkloadAmbiguous {
		t.Fatal(err)
	}
}

func TestPlanAlreadyDeclared(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t, append(billing(time.Now()), kubePacto("billing", "invoices", "1.0.0"))...)
	if _, _, err := e.svc.PlanDeclare(t.Context(), e.req()); refusal(t, err).Code != codeAlreadyDeclared {
		t.Fatal(err)
	}
}

func TestPlanNeedsACluster(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.req()
	req.Dynamic = nil
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); err == nil || !strings.Contains(err.Error(), "no Kubernetes client") {
		t.Fatal(err)
	}
}

func TestOwnerRuleNewOwnerStrippedFromClusterDraft(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	p, d := e.plan(t, e.req())
	i := slices.IndexFunc(d.record.Fields, func(f contract.Inferred) bool { return f.Field == "service.owner" })
	if strings.Contains(string(d.raw), "owner") || p.Owner != "" || i < 0 || d.record.Fields[i].Source != "cluster:label billing/Deployment/invoices team" ||
		d.record.Fields[i].Value != "billing" || d.record.Fields[i].AsOf == "" {
		t.Fatalf("%s\n%s", d.raw, d.record.Marshal())
	}
	if f := ownerField(contract.Owner{DRI: "ana"}, d.record); f.Value != "ana (dri)" || !strings.Contains(f.Declare, "dri: ana") {
		t.Fatalf("%+v", f)
	}
}

func TestAcceptRewritesUnknownOnly(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	_, d := e.plan(t, e.req("ledger", "ledger"))
	raw := string(d.raw)
	if !strings.Contains(raw, "compatibility: \"^2.3.0\"   # proposed ^2.3.0") || !strings.Contains(raw, "    compatibility: unknown   # proposed ^1.4.0") ||
		len(d.record.Declaration.Accepted) != 1 {
		t.Fatalf("%s", raw)
	}
}

func TestAcceptWithoutProposalRefused(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	if _, _, err := e.svc.PlanDeclare(t.Context(), e.req("fraud")); refusal(t, err).Code != codeRangeNotProposed {
		t.Fatal(err)
	}
	rec := &contract.InferredRecord{Fields: []contract.Inferred{{Field: "dependencies[ledger].compatibility", Proposed: "^1.0.0"}}}
	if _, _, err := applyAccept([]byte("dependencies:\n  - name: ledger\n    compatibility: ^2.0.0\n"), rec, []string{"ledger"}); refusal(t, err).Code != codeRangeNotProposed {
		t.Fatal(err)
	}
}

func TestDeclareProposalStaysUnknown(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	p, d := e.plan(t, e.req())
	for _, r := range p.Ranges {
		if r.Compatibility != contract.CompatibilityUnknown || r.Accepted || r.Proposed == "" {
			t.Fatalf("%+v", r)
		}
	}
	if len(d.record.Declaration.Accepted) != 0 {
		t.Fatalf("%s", d.record.Marshal())
	}
}

func TestPlanInferredFromAndCluster(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	p, d := e.plan(t, e.req())
	if p.InferredFrom != "k8s:billing/Deployment/invoices" || p.Cluster != "3f1c-uid" || d.record.Declaration.InferredFrom != p.InferredFrom || d.record.Declaration.Cluster != "3f1c-uid" {
		t.Fatalf("%+v", p)
	}
	e.dyn.PrependReactor("get", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "kube-system", errors.New("no"))
	})
	p, _ = e.plan(t, e.req())
	if p.Cluster != "" || !slices.ContainsFunc(p.Notes, func(n string) bool { return strings.HasPrefix(n, "the cluster's identity was not read (") }) {
		t.Fatalf("%+v", p)
	}
}

// noOwner is the cluster draft of invoices with no owner line.
func noOwner(t *testing.T, e *declareEnv) string {
	t.Helper()
	dir := e.dirFolder(t, "", nil)
	raw, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	raw, err := contract.RemoveServiceOwner(raw)
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, "pacto.yaml"), raw, 0o644)
	}
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPlanFromDir(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.req("ledger")
	req.Dir = noOwner(t, e)
	p, d := e.plan(t, req)
	if p.Version != firstDeclaration || p.InferredFrom != "k8s:billing/Deployment/invoices" || !rangeOf(t, p, "ledger").Accepted || d.record.Inputs.Cluster == nil {
		t.Fatalf("%+v", p)
	}
}

func TestPlanFromDirWithoutClusterRecordRefused(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.req()
	req.Dir = t.TempDir()
	_ = os.WriteFile(filepath.Join(req.Dir, "pacto.yaml"), []byte("pactoVersion: \"2.5\"\nservice:\n  name: invoices\n"), 0o644)
	_, _, err := e.svc.PlanDeclare(t.Context(), req)
	if de := refusal(t, err); de.Code != codeWorkloadNotFound || !strings.Contains(de.Message, "run pacto init --cluster first") {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(req.Dir, contract.InferredRecordFile), []byte("format: pacto.inferred/v1\ninputs: {pacto: \"\", discovery: \"5\", sources: []}\nfields: []\n"), 0o644)
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); refusal(t, err).Code != codeWorkloadNotFound {
		t.Fatal(err)
	}
}

func TestDirClusterFactsReRead(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.req()
	req.Dir = noOwner(t, e)
	recPath := filepath.Join(req.Dir, contract.InferredRecordFile)
	raw, _ := os.ReadFile(recPath)
	rec, err := contract.ParseInferredRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	stale := "2020-01-01T00:00:00Z"
	rec.Inputs.Cluster.AsOf = stale
	for i := range rec.Fields {
		if rec.Fields[i].AsOf != "" {
			rec.Fields[i].AsOf = stale
		}
	}
	for i := range rec.Coverage {
		rec.Coverage[i] = contract.InferredCoverage{Source: rec.Coverage[i].Source, State: contract.CoverageComplete}
	}
	_ = os.WriteFile(recPath, rec.Marshal(), 0o644)
	p, d := e.plan(t, req)
	if d.record.Inputs.Cluster.AsOf == stale || p.Coverage[len(p.Coverage)-1].State != contract.CoveragePartial {
		t.Fatalf("%s", d.record.Marshal())
	}
	// No cluster to read again: the folder's cluster facts are dropped.
	req.Dynamic = nil
	p, d = e.plan(t, req)
	if d.record.Inputs.Cluster != nil || len(p.Coverage) != 0 || slices.ContainsFunc(d.record.Fields, func(f contract.Inferred) bool { return strings.HasPrefix(f.Source, "cluster:") }) ||
		!slices.ContainsFunc(p.Notes, func(n string) bool {
			return strings.HasPrefix(n, "the cluster was not read again (no Kubernetes client")
		}) || p.Cluster != "" {
		t.Fatalf("%+v\n%s", p, d.record.Marshal())
	}
	// A re-read that fails drops them too.
	e.dyn.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("down") })
	req.Dynamic = e.dyn
	// No fact of this run came from the cluster, so the record names none.
	if p, _ = e.plan(t, req); !slices.ContainsFunc(p.Notes, func(n string) bool { return strings.HasPrefix(n, "the cluster was not read again (") }) || p.Cluster != "" {
		t.Fatalf("%+v %q", p.Notes, p.Cluster)
	}
}

// A re-read that finds the workload already has a contract refuses, as the
// cluster path does: it is no failed read.
func TestPlanFromDirAlreadyDeclaredRefused(t *testing.T) {
	t.Parallel()
	dir := noOwner(t, newDeclareEnv(t))
	e := newDeclareEnv(t, append(billing(time.Now()), kubePacto("billing", "invoices", "1.0.0"))...)
	req := e.req()
	req.Dir = dir
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); refusal(t, err).Code != codeAlreadyDeclared {
		t.Fatal(err)
	}
}

// deployHeadFails cannot read the baseline's deploy artifact.
type deployHeadFails struct{ publish.Registry }

func (d deployHeadFails) Head(ctx context.Context, ref string) (*publish.Remote, error) {
	if strings.Contains(ref, "/acme/deploy/") {
		return nil, errors.New("401")
	}
	return d.Registry.Head(ctx, ref)
}

// A baseline deploy artifact that cannot be read refuses the plan: the
// signed record never says "none" for it.
func TestPlanBaselineDeployUnreadableRefused(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withBaseline(t)
	e.svc.sys.publishRegistry = func() publish.Registry { return deployHeadFails{e.reg} }
	if _, _, err := e.svc.PlanDeclare(t.Context(), e.req()); refusal(t, err).Code != codeRegistryUnreadable || !strings.Contains(err.Error(), "deploy artifact") {
		t.Fatal(err)
	}
}

// referrersFail cannot list any referrer.
type referrersFail struct{ publish.Registry }

func (referrersFail) Referrers(context.Context, string, string, string) ([]string, error) {
	return nil, errors.New("timeout")
}

// A tag whose signatures could not be read is signed by someone unknown,
// never by nobody.
func TestPlanUnreadSignaturesAreUnknown(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.publishBundle(t, e.host+"/acme/invoices", "1.0.0", e.invoicesBaseline("1.0.0", ""), nil)
	e.svc.sys.publishRegistry = func() publish.Registry { return referrersFail{e.reg} }
	p, _, err := e.svc.PlanDeclare(t.Context(), e.req())
	if de := refusal(t, err); de.Code != codeVersionConflict || strings.Contains(de.Message, "nobody") ||
		len(p.Skipped) != 1 || p.Skipped[0].Signer != "unknown (its signatures could not be read)" {
		t.Fatalf("%v %+v", err, p.Skipped)
	}
}

// walkBundle is every file of b by path.
func walkBundle(t *testing.T, b *contract.Bundle) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := fs.WalkDir(b.FS, ".", func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return err
		}
		data, err := fs.ReadFile(b.FS, p)
		out[p] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDeclarationBundleHasNoManifests(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	_, d := e.plan(t, e.req())
	files := walkBundle(t, d.bundle)
	if _, ok := files[manifestsFile]; ok || files[contract.InferredRecordFile] == "" || files["pacto.yaml"] == "" {
		t.Fatalf("%v", slices.Collect(maps.Keys(files)))
	}
}

func TestDeclarationBundleHoldsNoLiteral(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	_, d := e.plan(t, e.req())
	for p, c := range walkBundle(t, d.bundle) {
		if strings.Contains(c, "hunter2") {
			t.Errorf("the env literal is in %s:\n%s", p, c)
		}
	}
}

func TestDeclarationBundleCarriesInterfaceFiles(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.req()
	req.Dir = noOwner(t, e)
	raw, _ := os.ReadFile(filepath.Join(req.Dir, "pacto.yaml"))
	// The draft already reads its env from configuration/schema.json.
	withFiles := string(raw) + "interfaces:\n  - name: api\n    type: openapi\n    ref: interfaces/openapi.yaml\n"
	_ = os.WriteFile(filepath.Join(req.Dir, "pacto.yaml"), []byte(withFiles), 0o644)
	_ = os.MkdirAll(filepath.Join(req.Dir, "interfaces"), 0o755)
	_ = os.WriteFile(filepath.Join(req.Dir, "interfaces", "openapi.yaml"), []byte("openapi: 3.1.0\ninfo: {title: invoices, version: 1.0.0}\npaths: {}\n"), 0o644)
	_, d := e.plan(t, req)
	files := walkBundle(t, d.bundle)
	if files["interfaces/openapi.yaml"] == "" || files["configuration/schema.json"] == "" {
		t.Fatalf("%v", slices.Collect(maps.Keys(files)))
	}
}

func TestDeclarationBundleHonoursPactoignore(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.req()
	req.Dir = noOwner(t, e)
	_ = os.WriteFile(filepath.Join(req.Dir, ".pactoignore"), []byte("notes.txt\n"), 0o644)
	_ = os.WriteFile(filepath.Join(req.Dir, "notes.txt"), []byte("private"), 0o644)
	_, d := e.plan(t, req)
	if _, ok := walkBundle(t, d.bundle)["notes.txt"]; ok {
		t.Fatal("an ignored file is in the bundle")
	}
}

// writeFolder writes files into a new folder.
func writeFolder(t *testing.T, files fs.FS) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, files); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDeclarationBundleValidates(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	_, d := e.plan(t, e.req())
	dir := writeFolder(t, d.files)
	forged := *d.record
	dec := *forged.Declaration
	dec.Accepted = []contract.InferredAcceptance{{Field: "dependencies[audit].compatibility", Range: "^9.0.0"}}
	forged.Declaration = &dec
	if _, err := declarationBundle(dir, &forged); err == nil || !strings.Contains(err.Error(), "the declaration is not valid") {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: \"2.4\"\nservice:\n  name: invoices\n  version: 0.1.0\n"), 0o644)
	if _, err := declarationBundle(dir, d.record); err == nil || !strings.Contains(err.Error(), "pactoVersion 2.5") {
		t.Fatal(err)
	}
	if _, err := declarationBundle(filepath.Join(dir, "missing"), d.record); err == nil {
		t.Fatal("a missing folder packed")
	}
}

func TestDeclarationBundleDeterministic(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	_, d := e.plan(t, e.req())
	dir := writeFolder(t, d.files)
	a, err := declarationBundle(dir, d.record)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := declarationBundle(dir, d.record)
	da, _ := oci.BundleDigest(a)
	db, _ := oci.BundleDigest(b)
	if da != db {
		t.Fatalf("%s != %s", da, db)
	}
}

func TestIntentDigestIgnoresObservedFacts(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	p, d := e.plan(t, e.req())
	observed := *p
	observed.Coverage, observed.Hidden, observed.Cluster, observed.Notes = nil, &contract.InferredHidden{Services: 3}, "other", []string{"x"}
	if intentDigest(&observed, d.raw, d.record.Declaration.Accepted, "") != p.IntentDigest {
		t.Fatal("observed facts changed the intent")
	}
	if intentDigest(p, d.raw, nil, "sha256:aa") == p.IntentDigest {
		t.Fatal("the draft does not bind")
	}
}

func TestIntentDigestChangesWithAccept(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	a, _ := e.plan(t, e.req())
	b, _ := e.plan(t, e.req("ledger"))
	if a.IntentDigest == b.IntentDigest {
		t.Fatal("accepting a range kept the intent")
	}
}

func TestIntentDigestIgnoresSigner(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	person, _ := e.plan(t, e.req("ledger"))
	req := e.req("ledger")
	req.Agent, req.OnBehalfOf = true, "ana"
	agent, _ := e.plan(t, req)
	if person.State == agent.State || person.IntentDigest != agent.IntentDigest {
		t.Fatalf("%s %s / %s %s", person.State, person.IntentDigest, agent.State, agent.IntentDigest)
	}
}

func TestPlanReadsNoSecret(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.plan(t, e.req("ledger"))
	configmaps := 0
	for _, a := range e.dyn.Actions() {
		switch a.GetResource().Resource {
		case "secrets":
			t.Fatalf("read a secret: %+v", a)
		case "configmaps":
			get, ok := a.(k8stesting.GetAction)
			if !ok || get.GetNamespace() != delivery.PolicyNamespace || get.GetName() != delivery.PolicyConfigMap {
				t.Fatalf("%+v", a)
			}
			configmaps++
		}
	}
	if configmaps != 1 {
		t.Fatalf("%d configmap reads", configmaps)
	}
}

func TestPlanLeavesNoScratch(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	var scratch []string
	e.svc.sys.mkdirTemp = func(dir, pattern string) (string, error) {
		s, err := os.MkdirTemp(dir, pattern)
		scratch = append(scratch, s)
		return s, err
	}
	e.plan(t, e.req())
	req := e.req()
	req.Service = "missing"
	_, _, _ = e.svc.PlanDeclare(t.Context(), req)
	for _, s := range scratch {
		if _, err := os.Stat(s); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s left: %v", s, err)
		}
	}
	e.svc.sys.mkdirTemp = func(string, string) (string, error) { return "", errors.New("disk full") }
	if _, _, err := e.svc.PlanDeclare(t.Context(), e.req()); err == nil || err.Error() != "disk full" {
		t.Fatal(err)
	}
}

// faultyRegistry fails the calls a test names.
type faultyRegistry struct {
	publish.Registry
	tags, head error
	// pulls fails every pull after the first n when failPull is set.
	pulls    int
	failPull int
	// vanish is a tag the registry answers is not there.
	vanish string
}

func (f *faultyRegistry) Tags(ctx context.Context, repo string) ([]string, error) {
	if f.tags != nil {
		return nil, f.tags
	}
	return f.Registry.Tags(ctx, repo)
}

func (f *faultyRegistry) Head(ctx context.Context, ref string) (*publish.Remote, error) {
	switch {
	case f.head != nil:
		return nil, f.head
	case f.vanish != "" && strings.HasSuffix(ref, ":"+f.vanish):
		return nil, nil
	}
	return f.Registry.Head(ctx, ref)
}

func (f *faultyRegistry) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	if f.pulls++; f.failPull > 0 && f.pulls >= f.failPull {
		return nil, errors.New("pull failed")
	}
	return f.Registry.Pull(ctx, ref)
}

func (e *declareEnv) faulty(f *faultyRegistry) {
	f.Registry = e.reg
	e.svc.sys.publishRegistry = func() publish.Registry { return f }
}

func TestPlanRegistryUnreadable(t *testing.T) {
	t.Parallel()
	for name, f := range map[string]*faultyRegistry{"tags": {tags: errors.New("down")}, "head": {head: errors.New("down")}, "pull": {failPull: 1}} {
		e := newDeclareEnv(t)
		e.withBaseline(t)
		e.faulty(f)
		if _, _, err := e.svc.PlanDeclare(t.Context(), e.req()); refusal(t, err).Code != codeRegistryUnreadable {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPlanBaselineVanishedTagSkipped(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withBaseline(t)
	e.publishBundle(t, e.host+"/acme/invoices", "1.6.0", e.invoicesBaseline("1.6.0", ""), nil)
	e.faulty(&faultyRegistry{vanish: "1.6.0"})
	if p, _ := e.plan(t, e.req()); p.Baseline.Version != "1.4.0" || len(p.Skipped) != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestPlanDeriveFails(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withBaseline(t)
	req := e.req()
	req.Dir = e.dirFolder(t, e.invoicesBaseline("1.4.0", ledgerDependency)["pacto.yaml"], nil)
	e.faulty(&faultyRegistry{failPull: 3}) // the baseline reads once, the owner history once, then deriving reads it again
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); refusal(t, err).Code != codeVersionConflict {
		t.Fatal(err)
	}
}

func TestPlanTrustedRootUnreadable(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withBaseline(t)
	req := e.req()
	req.Signing.TrustedRoot = filepath.Join(t.TempDir(), "missing.json")
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); err == nil || !strings.Contains(err.Error(), "the trusted root") {
		t.Fatal(err)
	}
}

func TestPlanClusterReadFails(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.dyn.PrependReactor("list", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if slices.Contains([]string{"deployments", "statefulsets", "daemonsets", "cronjobs"}, a.GetResource().Resource) {
			return true, nil, errors.New("down")
		}
		return false, nil, nil
	})
	if _, _, err := e.svc.PlanDeclare(t.Context(), e.req()); err == nil || !strings.HasPrefix(err.Error(), "reading the workload from the cluster: ") {
		t.Fatal(err)
	}
}

// unreadCluster makes every workload list of e fail, so a folder's
// workload cannot be read again.
func unreadCluster(e *declareEnv) {
	e.dyn.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("down") })
}

func TestPlanFromDirInvalidContract(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.req()
	req.Dir = noOwner(t, e)
	_ = os.WriteFile(filepath.Join(req.Dir, "pacto.yaml"), []byte("service: [\n"), 0o644)
	req.Dynamic = nil
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); err == nil {
		t.Fatal("an unreadable pacto.yaml planned")
	}
	// A folder at pactoVersion 2.4 is never a declaration, with or without a baseline.
	_ = os.WriteFile(filepath.Join(req.Dir, "pacto.yaml"), []byte("pactoVersion: \"2.4\"\nservice:\n  name: invoices\n"), 0o644)
	req.Dynamic = e.dyn
	unreadCluster(e)
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); err == nil || !strings.Contains(err.Error(), "pactoVersion 2.5") {
		t.Fatal(err)
	}
	e.withBaseline(t)
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); err == nil || !strings.Contains(err.Error(), "pactoVersion 2.5") {
		t.Fatal(err)
	}
}

func TestPlanScratchUnwritable(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	e := newDeclareEnv(t)
	req := e.req()
	req.Dir = noOwner(t, e)
	e.svc.sys.mkdirTemp = func(dir, pattern string) (string, error) {
		s, err := os.MkdirTemp(dir, pattern)
		if err == nil {
			err = os.Chmod(s, 0o500)
			t.Cleanup(func() { _ = os.Chmod(s, 0o700); _ = os.RemoveAll(s) })
		}
		return s, err
	}
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); err == nil {
		t.Fatal("planned into a folder it cannot write")
	}
}

func TestPlanWriteFails(t *testing.T) {
	t.Parallel()
	for _, marker := range []string{"version: 1.4.0", "version: 0.1.0"} {
		e := newDeclareEnv(t)
		if marker == "version: 1.4.0" {
			e.withBaseline(t)
		}
		e.svc.sys.writeFile = func(name string, data []byte, perm os.FileMode) error {
			if strings.Contains(string(data), marker) {
				return errors.New("disk full")
			}
			return os.WriteFile(name, data, perm)
		}
		if _, _, err := e.svc.PlanDeclare(t.Context(), e.req()); err == nil || !strings.Contains(err.Error(), "disk full") {
			t.Errorf("%s: %v", marker, err)
		}
	}
}

func TestPlanRekorNotice(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.req()
	req.Signing.RekorURL = signing.PublicRekor
	p, _ := e.plan(t, req)
	if !slices.Contains(p.Notes, "your email is recorded in the public Rekor transparency log") {
		t.Fatalf("%v", p.Notes)
	}
}

func TestBaselineDeployNone(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	base := &contract.Bundle{Contract: &contract.Contract{Publish: &contract.Publish{Deploy: "manifests", Artifacts: []contract.PublishArtifact{
		{Name: "api", Ref: "oci://" + e.host + "/acme/invoices-api"}, {Name: "manifests", Ref: "oci://" + e.host + "/acme/deploy/invoices"}}}}}
	if d, err := baselineDeploy(t.Context(), e.reg, base, "1.4.0"); d != "" || err != nil {
		t.Fatal(d, err)
	}
	if d, err := baselineDeploy(t.Context(), e.reg, &contract.Bundle{Contract: &contract.Contract{}}, "1.4.0"); d != "" || err != nil {
		t.Fatal(d, err)
	}
	// A deploy artifact that cannot be read is unknown, never "none".
	if d, err := baselineDeploy(t.Context(), &faultyRegistry{Registry: e.reg, head: errors.New("401")}, base, "1.4.0"); d != "" || err == nil {
		t.Fatal("a failed read reads as no deploy artifact")
	}
}

func TestDeclarationBundleUnreadableFile(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("root reads anything")
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: \"2.5\"\nservice:\n  name: invoices\n  version: 0.1.0\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("x"), 0o000)
	if _, err := declarationBundle(dir, &contract.InferredRecord{Format: contract.InferredRecordFormat}); err == nil {
		t.Fatal("an unreadable file packed")
	}
}

func TestIntentDigestSortsAccepted(t *testing.T) {
	t.Parallel()
	p := &DeclarePlan{Service: "invoices", Version: "0.1.0"}
	a := contract.InferredAcceptance{Field: "dependencies[a].compatibility", Range: "^1.0.0"}
	b := contract.InferredAcceptance{Field: "dependencies[b].compatibility", Range: "^2.0.0"}
	if intentDigest(p, nil, []contract.InferredAcceptance{a, b}, "") != intentDigest(p, nil, []contract.InferredAcceptance{b, a}, "") {
		t.Fatal("the order of the accepted ranges changed the intent")
	}
}

func TestPublisherRules(t *testing.T) {
	t.Parallel()
	if publisherRules("") != nil || publisherRules("{") != nil || publisherRules(`{"registry":"ghcr.io"}`) != nil || len(publisherRules(`{"registry":"ghcr.io/acme"}`)) != 1 {
		t.Fatal("publisher rules")
	}
}
