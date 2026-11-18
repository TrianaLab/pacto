package discover

import (
	"fmt"
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

func xpUsesOf(t *testing.T, dir string, cat *externalcatalog.Catalog) []string {
	t.Helper()
	sc, err := Dir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, x := range sc.crossplaneUses(cat) {
		if x.Kind != "crossplane" {
			t.Errorf("external %+v", x)
		}
		out = append(out, managedLine(x))
	}
	return out
}

func TestCrossplaneCandidatesNeedNoCatalog(t *testing.T) {
	sc, err := Dir("testdata/crossplane")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range sc.xpObjects {
		got = append(got, o.group+"/"+o.kind+" "+o.name)
	}
	want := []string{"rds.aws.upbound.io/Instance orders-db", "sqs.aws.upbound.io/Queue signups", "platform.example.org/PostgreSQLInstance ledger-db", "networking.k8s.io/Ingress web"}
	if !slices.Equal(got, want) {
		t.Errorf("candidates %q", got)
	}
	if o := sc.xpObjects[0]; o.externalName != "orders-prod" || o.region != "eu-west-1" || o.connSecret != "orders-db-conn" || o.file != "rds.yaml" || o.line != 2 {
		t.Errorf("object %+v", o)
	}
}

func TestCrossplaneManagedResourceRegionAndId(t *testing.T) {
	uses := xpUsesOf(t, "testdata/crossplane", externalcatalog.Default())
	equalUses(t, uses, []string{
		"aws/rds rds.yaml:2 Instance.orders-db rds.aws.upbound.io/Instance orders-prod account likely",
		"aws/sqs rds.yaml:14 Queue.signups sqs.aws.upbound.io/Queue signups account possible",
	})
	rec := complete(t, "testdata/crossplane", nil)
	f := field(t, rec.Fields, "dependencies[orders-prod]")
	if f.Value != "aws/rds" || f.Confidence != contract.ConfidenceLikely || f.External.Resource.Type != "rds.aws.upbound.io/Instance" ||
		!slices.ContainsFunc(rec.Inputs.Sources, func(s contract.InferredSourceFile) bool { return s.Path == "rds.yaml" }) {
		t.Errorf("field %+v %+v, inputs %+v", f, f.External, rec.Inputs.Sources)
	}
	if _, err := contract.ParseInferredRecord(rec.Marshal()); err != nil {
		t.Fatal(err)
	}
}

func TestCrossplaneConnectionSecretMakesLikely(t *testing.T) {
	obj := "apiVersion: sqs.aws.upbound.io/v1beta1\nkind: Queue\nmetadata:\n  name: q\nspec:\n  writeConnectionSecretToRef:\n    name: q-conn\n"
	deploy := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app\nspec:\n  template:\n    spec:\n      containers:\n        - name: app\n          envFrom:\n            - secretRef:\n                name: %s\n"
	for secret, want := range map[string]string{"q-conn": "likely", "other": "possible"} {
		dir := writeTree(t, t.TempDir(), map[string]string{"queue.yaml": obj, "deploy.yaml": fmt.Sprintf(deploy, secret)})
		uses := xpUsesOf(t, dir, externalcatalog.Default())
		equalUses(t, uses, []string{"aws/sqs queue.yaml:2 Queue.q sqs.aws.upbound.io/Queue q account region " + want})
	}
	// No workload reads nothing.
	uses := xpUsesOf(t, writeTree(t, t.TempDir(), map[string]string{"queue.yaml": obj}), externalcatalog.Default())
	equalUses(t, uses, []string{"aws/sqs queue.yaml:2 Queue.q sqs.aws.upbound.io/Queue q account region possible"})
}

func TestCrossplaneOrgClaimNeedsOrgCatalog(t *testing.T) {
	org := []byte("apiVersion: " + externalcatalog.APIVersion + "\nentries:\n  - identity: example/postgres\n    class: managed-cloud\n    crossplane: [platform.example.org/PostgreSQLInstance]\n")
	cat, err := externalcatalog.Assemble(externalcatalog.Ref{Origin: externalcatalog.OriginDefault, Digest: externalcatalog.DefaultDigest()}, org,
		externalcatalog.Ref{Origin: externalcatalog.OriginOrganization, Digest: "sha256:org"})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(xpUsesOf(t, "testdata/crossplane", externalcatalog.Default()), func(u string) bool { return u[:8] == "example/" }) {
		t.Error("the default catalog matched an organization claim")
	}
	sc, _ := Dir("testdata/crossplane")
	xs := sc.crossplaneUses(cat)
	i := slices.IndexFunc(xs, func(x External) bool { return x.Identity == "example/postgres" })
	if i < 0 || xs[i].Origin != externalcatalog.OriginOrganization || xs[i].Resource.Name != "ledger-db" {
		t.Errorf("externals %+v", xs)
	}
}

func TestCrossplaneNoValidIdSkipped(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{"q.yaml": "apiVersion: sqs.aws.upbound.io/v1beta1\nkind: Queue\nmetadata:\n  name: \"Not Valid\"\n"})
	if uses := xpUsesOf(t, dir, externalcatalog.Default()); len(uses) != 0 {
		t.Errorf("uses %q", uses)
	}
}
