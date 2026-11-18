package discover

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// managedLine is "<identity> <where> <type> <name> [missing...] <confidence>".
func managedLine(x External) string {
	return strings.Join(append([]string{x.Identity, x.Where, x.Resource.Type, x.Resource.Name}, append(x.Resource.Missing, x.Confidence)...), " ")
}

// tfUsesOf scans dir and returns its Terraform externals as managed lines,
// its findings as "<code> <source> <message>" and the files read.
func tfUsesOf(t *testing.T, dir string) (uses, findings, read []string) {
	t.Helper()
	sc, err := Dir(dir)
	if err != nil {
		t.Fatal(err)
	}
	xs, fs, files := sc.terraformUses(dir, externalcatalog.Default())
	for _, x := range xs {
		if x.Kind != "terraform" {
			t.Errorf("external %+v", x)
		}
		uses = append(uses, managedLine(x))
	}
	for _, f := range fs {
		findings = append(findings, f.Code+" "+f.Source+" "+f.Message)
	}
	return uses, findings, files
}

func tfDir(t *testing.T, main string) string {
	return writeTree(t, t.TempDir(), map[string]string{"main.tf": main})
}

const awsProvider = "provider \"aws\" {\n  region = \"eu-west-1\"\n  allowed_account_ids = [\"123456789012\"]\n}\n"

func TestTerraformResourceLiteralId(t *testing.T) {
	uses, findings, read := tfUsesOf(t, "testdata/terraform")
	equalUses(t, uses, []string{
		"aws/sqs/123456789012/eu-west-1/orders-queue infra/main.tf:12 aws_sqs_queue.orders aws_sqs_queue orders-queue possible",
		"aws/sqs infra/main.tf:23 aws_sqs_queue.us_orders aws_sqs_queue us-orders account region possible",
		"aws/rds infra/main.tf:28 aws_db_instance.main aws_db_instance main id possible",
	})
	if !slices.Equal(read, []string{"infra/main.tf"}) {
		t.Errorf("read %q", read)
	}
	if !slices.Equal(findings, []string{CodeUnsupportedFile + " infra/main.tf:37 infra/main.tf:37 calls the Terraform module network, which Pacto does not read"}) {
		t.Errorf("findings %q", findings)
	}
}

func TestTerraformInterpolatedIdFallsBackToName(t *testing.T) {
	uses, _, _ := tfUsesOf(t, tfDir(t, awsProvider+"resource \"aws_sqs_queue\" \"jobs\" {\n  name = \"${var.env}-jobs\"\n}\n"+
		"resource \"aws_sqs_queue\" \"Bad_\" {\n  name = format(\"%s\", \"x\")\n}\n"+
		"resource \"aws_sqs_queue\" \"x\" {\n  name = \"Has Space\"\n}\n"))
	equalUses(t, uses, []string{
		"aws/sqs main.tf:5 aws_sqs_queue.jobs aws_sqs_queue jobs id possible",
		"aws/sqs main.tf:11 aws_sqs_queue.x aws_sqs_queue x id possible",
	}) // Bad_ makes no identity segment, so it is left out
}

func TestTerraformBracesInStringsAndHeredocs(t *testing.T) {
	uses, _, _ := tfUsesOf(t, tfDir(t, awsProvider+`resource "aws_sqs_queue" "a" {
  description = "a { b \" { ${jsonencode({ k = "}" })} $${x} %%{y}"
  redrive = <<EOF
{ "deadLetterTargetArn": "${aws_sqs_queue.dlq.arn}" }
EOF
  tags = {
    name = "not-the-id"
  }
  name = "first"
}
resource "aws_sqs_queue" "b" { name = "one-line" }
resource "aws_sqs_queue" "c" {
  name = "second" # trailing
}
`))
	equalUses(t, uses, []string{
		"aws/sqs/123456789012/eu-west-1/first main.tf:5 aws_sqs_queue.a aws_sqs_queue first possible",
		"aws/sqs main.tf:15 aws_sqs_queue.b aws_sqs_queue b id possible",
		"aws/sqs/123456789012/eu-west-1/second main.tf:16 aws_sqs_queue.c aws_sqs_queue second possible",
	})
}

func TestTerraformCommentsIgnored(t *testing.T) {
	uses, _, _ := tfUsesOf(t, tfDir(t, awsProvider+`# resource "aws_sqs_queue" "hash" {
// resource "aws_sqs_queue" "slash" {
/*
resource "aws_sqs_queue" "block" {
  name = "block"
}
*/ resource "aws_sqs_queue" "after_comment" {
}
resource "aws_sqs_queue" "real" { /* { */
  # name = "commented"
  name = "real-queue" // }
}
`))
	equalUses(t, uses, []string{"aws/sqs/123456789012/eu-west-1/real-queue main.tf:13 aws_sqs_queue.real aws_sqs_queue real-queue possible"})
}

func TestTerraformNeverReadsOtherAttributes(t *testing.T) {
	rec := complete(t, "testdata/terraform", nil)
	data, _ := json.Marshal(rec)
	all := string(data) + string(rec.Marshal())
	for _, leak := range []string{"tf-hunter2", "ami-123", "var.prefix", "us-east-1", "./network"} {
		if strings.Contains(all, leak) {
			t.Errorf("%q leaked", leak)
		}
	}
	if _, err := contract.ParseInferredRecord(rec.Marshal()); err != nil {
		t.Fatal(err)
	}
}

func TestTerraformSensitiveIdAttributeRefused(t *testing.T) {
	rs, _, _ := terraformResources("main.tf", []byte("resource \"aws_db_instance\" \"main\" {\n  password = \"tf-hunter2\"\n}\n"),
		func(string) string { return "password" })
	if len(rs) != 1 || rs[0].id != "" {
		t.Fatalf("resources = %+v", rs)
	}
}

func TestTerraformStateNeverRead(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"main.tf":                      awsProvider + "resource \"aws_sqs_queue\" \"q\" {\n  name = \"q\"\n}\n",
		"terraform.tfstate":            `{"resources": [{"instances": [{"attributes": {"password": "state-hunter2"}}]}]}`,
		"old/terraform.tfstate.backup": "{}",
	})
	var opened []string
	readFolderFile = func(p string) ([]byte, error) { opened = append(opened, p); return os.ReadFile(p) }
	defer func() { readFolderFile = os.ReadFile }()
	_, findings, read := tfUsesOf(t, dir)
	if !slices.Equal(opened, []string{filepath.Join(dir, "main.tf")}) || !slices.Equal(read, []string{"main.tf"}) {
		t.Errorf("opened %q, read %q", opened, read)
	}
	want := []string{
		CodeUnsupportedFile + " old/terraform.tfstate.backup old/terraform.tfstate.backup is not read: " + unsupportedTerraformState,
		CodeUnsupportedFile + " terraform.tfstate terraform.tfstate is not read: " + unsupportedTerraformState,
	}
	if !slices.Equal(findings, want) {
		t.Errorf("findings %q", findings)
	}
	rec := complete(t, dir, nil)
	if data := rec.Marshal(); strings.Contains(string(data), "state-hunter2") || slices.ContainsFunc(rec.Inputs.Sources, func(s contract.InferredSourceFile) bool {
		return strings.Contains(s.Path, "tfstate")
	}) {
		t.Errorf("state reached the record:\n%s", data)
	}
}

func TestTerraformStateUnreadableStillNamed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	dir := tfDir(t, awsProvider)
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte("{}"), 0o000); err != nil {
		t.Fatal(err)
	}
	_, findings, _ := tfUsesOf(t, dir)
	if len(findings) != 1 || !strings.Contains(findings[0], unsupportedTerraformState) {
		t.Errorf("findings %q", findings)
	}
}

func TestTerraformModuleIsUnknown(t *testing.T) {
	_, findings, _ := tfUsesOf(t, tfDir(t, "module \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n}\n"))
	if !slices.Equal(findings, []string{CodeUnsupportedFile + " main.tf:1 main.tf:1 calls the Terraform module vpc, which Pacto does not read"}) {
		t.Errorf("findings %q", findings)
	}
}

func TestTerraformJSONIsUnknown(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{"main.tf.json": `{"resource": {"aws_sqs_queue": {"q": {"name": "q"}}}}`})
	uses, findings, read := tfUsesOf(t, dir)
	if len(uses) != 0 || len(read) != 0 || !slices.Equal(findings, []string{CodeUnsupportedFile + " main.tf.json main.tf.json is not read: " + unsupportedTerraformJSON}) {
		t.Errorf("uses %q, findings %q, read %q", uses, findings, read)
	}
}

func TestTerraformUnreadableFileIsFinding(t *testing.T) {
	readFolderFile = func(string) ([]byte, error) { return nil, os.ErrPermission }
	defer func() { readFolderFile = os.ReadFile }()
	uses, findings, _ := tfUsesOf(t, tfDir(t, awsProvider))
	if len(uses) != 0 || !slices.Equal(findings, []string{CodeUnreadable + " main.tf main.tf " + unreadableFailed}) {
		t.Errorf("uses %q, findings %q", uses, findings)
	}
}

func TestTerraformAliasedProvidersLeaveRegionUnknown(t *testing.T) {
	q := "resource \"aws_sqs_queue\" \"q\" {\n  name = \"q\"\n}\n"
	for name, main := range map[string]string{
		"two plain providers": awsProvider + awsProvider + q,
		"no provider":         q,
		"interpolated region": "provider \"aws\" {\n  region = var.region\n  allowed_account_ids = [\"123456789012\"]\n}\n" + q,
		"two accounts":        "provider \"aws\" {\n  region = \"eu-west-1\"\n  allowed_account_ids = [\"1\", \"2\"]\n}\n" + q,
	} {
		uses, _, _ := tfUsesOf(t, tfDir(t, main))
		if len(uses) != 1 || !strings.HasPrefix(uses[0], "aws/sqs ") || !strings.Contains(uses[0], " q ") {
			t.Errorf("%s: %q", name, uses)
		}
	}
	// Only the aliased block is aside; the plain one gives region and account.
	uses, _, _ := tfUsesOf(t, tfDir(t, awsProvider+"provider \"aws\" {\n  alias = \"x\"\n  region = \"us-east-1\"\n}\n"+q))
	equalUses(t, uses, []string{"aws/sqs/123456789012/eu-west-1/q main.tf:9 aws_sqs_queue.q aws_sqs_queue q possible"})
}

func TestManagedIdentityOnlyWhenComplete(t *testing.T) {
	uses, _, _ := tfUsesOf(t, tfDir(t, awsProvider+"resource \"aws_sqs_queue\" \"orders\" {\n  name = \"Orders-Queue\"\n}\n"+
		"resource \"google_pubsub_topic\" \"t\" {\n  name = \"events\"\n}\n"+
		"provider \"google\" {\n  project = \"shop-prod\"\n  region = \"europe-west1\"\n}\n"))
	equalUses(t, uses, []string{
		"aws/sqs/123456789012/eu-west-1/orders-queue main.tf:5 aws_sqs_queue.orders aws_sqs_queue orders-queue possible",
		"gcp/pubsub/shop-prod/europe-west1/events main.tf:8 google_pubsub_topic.t google_pubsub_topic events possible",
	})
	// A resource-level region overrides the provider's.
	uses, _, _ = tfUsesOf(t, tfDir(t, awsProvider+"resource \"aws_sqs_queue\" \"o\" {\n  region = \"us-west-2\"\n  name = \"o\"\n}\n"))
	equalUses(t, uses, []string{"aws/sqs/123456789012/us-west-2/o main.tf:5 aws_sqs_queue.o aws_sqs_queue o possible"})
}

func TestManagedPartialNamesMissingParts(t *testing.T) {
	main := "provider \"aws\" {\n  region = \"eu-west-1\"\n}\nresource \"aws_sqs_queue\" \"orders\" {\n  name = \"orders-queue\"\n}\n"
	uses, _, _ := tfUsesOf(t, tfDir(t, main))
	equalUses(t, uses, []string{"aws/sqs main.tf:4 aws_sqs_queue.orders aws_sqs_queue orders-queue account possible"})
	rec := complete(t, tfDir(t, main), nil)
	f := field(t, rec.Fields, "dependencies[orders-queue]")
	r := f.External.Resource
	if f.Value != "aws/sqs" || f.Confidence != contract.ConfidencePossible || r == nil || r.Type != "aws_sqs_queue" || r.Name != "orders-queue" || !slices.Equal(r.Missing, []string{"account"}) {
		t.Errorf("field %+v resource %+v", f, r)
	}
	if !slices.Equal(f.External.Sources, []contract.InferredExternalSource{{Kind: "terraform", Path: "main.tf", Line: 4, Key: "aws_sqs_queue.orders"}}) {
		t.Errorf("sources %+v", f.External.Sources)
	}
	if !slices.ContainsFunc(rec.Inputs.Sources, func(s contract.InferredSourceFile) bool { return s.Path == "main.tf" }) {
		t.Errorf("inputs %+v", rec.Inputs.Sources)
	}
	if _, err := contract.ParseInferredRecord(rec.Marshal()); err != nil {
		t.Fatal(err)
	}
}

func TestTwoPartialQueuesStayTwoEntries(t *testing.T) {
	rec := complete(t, tfDir(t, "resource \"aws_sqs_queue\" \"a\" {\n  name = \"orders\"\n}\nresource \"aws_sqs_queue\" \"b\" {\n  name = \"refunds\"\n}\n"+
		"resource \"aws_sqs_queue\" \"c\" {\n  name = \"orders\"\n}\n"), nil)
	a, b := field(t, rec.Fields, "dependencies[orders]"), field(t, rec.Fields, "dependencies[refunds]")
	if a.Value != "aws/sqs" || b.Value != "aws/sqs" || len(a.External.Sources) != 2 || len(b.External.Sources) != 1 {
		t.Errorf("a %+v, b %+v", a.External, b.External)
	}
}

func TestTerraformIsPossible(t *testing.T) {
	sc, err := Dir("testdata/terraform")
	if err != nil {
		t.Fatal(err)
	}
	xs, _, _ := sc.terraformUses("testdata/terraform", externalcatalog.Default())
	if len(xs) == 0 || slices.ContainsFunc(xs, func(x External) bool { return x.Confidence != externalcatalog.ConfidencePossible }) {
		t.Errorf("externals %+v", xs)
	}
}

func TestManagedResourceNameTakenFallsBack(t *testing.T) {
	x := External{Identity: "aws/sqs", Confidence: "possible", Kind: "terraform", Path: "main.tf", Line: 1, Origin: "default",
		Resource: &contract.InferredResource{Type: "aws_sqs_queue", Name: "web"}}
	fields, _ := externalFields([]External{x}, func(f string) bool { return f == "dependencies[web]" }, func(string, string) bool { return false }, map[string]bool{})
	if len(fields) != 1 || fields[0].Field != "dependencies[aws-sqs-web]" {
		t.Errorf("fields = %+v", fields)
	}
}

func TestManagedExternalBadKeyDropped(t *testing.T) {
	x, ok := managedExternal(externalcatalog.Match{Identity: "aws/sqs"}, managedSource{kind: "crossplane", path: "a.yaml", line: 2, key: "Queue.a b", typ: "g/Queue", ids: []string{"q"}})
	if !ok || x.Key != "" || x.Where != "a.yaml:2" {
		t.Errorf("external %+v", x)
	}
	if _, ok := managedExternal(externalcatalog.Match{Identity: "aws/sqs"}, managedSource{ids: []string{"", "a/b", "other-egress"}}); ok {
		t.Error("an id that makes no segment was recorded")
	}
}

func TestManagedResourcesSharingANameGetTwoFields(t *testing.T) {
	dir := tfDir(t, awsProvider+"resource \"aws_sqs_queue\" \"orders\" {\n  name = \"orders\"\n}\n"+
		"resource \"aws_sns_topic\" \"orders\" {\n  name = \"orders\"\n}\n")
	rec, err := Complete(dir, nil, contract.InferredInputs{Commit: "4e1a9c0", Pacto: "4.6.0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range rec.Fields {
		if f.External != nil {
			names = append(names, f.Field)
		}
	}
	if len(names) != 2 || names[0] == names[1] {
		t.Errorf("fields %q", names)
	}
	if _, err := contract.ParseInferredRecord(rec.Marshal()); err != nil {
		t.Errorf("record refused: %v", err)
	}
	if got := freeName("a", "k", func(n string) bool { return n == "a" || n == "k" || n == "k-2" }); got != "k-3" {
		t.Errorf("freeName = %q", got)
	}
}

func TestServiceDependencyOfTheSameNameKeepsTheExternal(t *testing.T) {
	dir := tfDir(t, "resource \"aws_sqs_queue\" \"orders\" {}\n")
	d := &contract.Contract{Dependencies: []contract.Dependency{{Name: "orders", Ref: "oci://ghcr.io/acme/orders:1.0.0"}}}
	rec, err := Complete(dir, d, contract.InferredInputs{Commit: "4e1a9c0", Pacto: "4.6.0"}, nil)
	if err != nil || rec == nil {
		t.Fatalf("record %v, %v", rec, err)
	}
	if i := slices.IndexFunc(rec.Fields, func(f contract.Inferred) bool { return f.External != nil }); i < 0 || rec.Fields[i].Field == "dependencies[orders]" {
		t.Errorf("fields %+v", rec.Fields)
	}
	d.Dependencies = append(d.Dependencies, contract.Dependency{Name: "queue", External: true, Identity: "aws/sqs"})
	if rec, _ := Complete(dir, d, contract.InferredInputs{Commit: "4e1a9c0", Pacto: "4.6.0"}, nil); rec != nil &&
		slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return f.External != nil }) {
		t.Error("an external the contract declares by identity was inferred")
	}
}

func TestUnrecordablePathsGiveNoExternals(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"infra:prod/main.tf":   awsProvider + "resource \"aws_sqs_queue\" \"orders\" {\n  name = \"orders\"\n}\n",
		"requirements:dev.txt": "stripe==11.4.0\n",
		"env:prod/queue.yaml":  "apiVersion: sqs.aws.upbound.io/v1beta1\nkind: Queue\nmetadata:\n  name: orders\n",
		"deploy/ok/README.md":  "",
	})
	rec, err := Complete(dir, nil, contract.InferredInputs{Commit: "4e1a9c0", Pacto: "4.6.0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec != nil {
		if slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return f.External != nil }) {
			t.Errorf("fields %+v", rec.Fields)
		}
		if _, err := contract.ParseInferredRecord(rec.Marshal()); err != nil {
			t.Errorf("record refused: %v", err)
		}
	}
}
