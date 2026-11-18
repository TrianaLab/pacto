package pactify

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func externalField(name, id string, x contract.InferredExternal) contract.Inferred {
	x.Identity = id
	return contract.Inferred{Field: "dependencies[" + name + "]", Value: id, External: &x}
}

func TestExternalSummaryLineMatchesUX(t *testing.T) {
	major := 76
	fields := []contract.Inferred{
		{Field: "image", Value: "ghcr.io/acme/payments:1.0.0"},
		externalField("payments-db", "aws/rds", contract.InferredExternal{
			Sources:  []contract.InferredExternalSource{{Kind: "terraform", Path: "infra/main.tf", Line: 4}},
			Resource: &contract.InferredResource{Type: "aws_db_instance", Name: "payments-db", Missing: []string{"account", "region"}}}),
		externalField("orders-queue", "aws/sqs/123456789012/eu-west-1/orders-queue", contract.InferredExternal{
			Sources:  []contract.InferredExternalSource{{Kind: "terraform", Path: "infra/main.tf", Line: 12}},
			Resource: &contract.InferredResource{Type: "aws_sqs_queue", Name: "orders-queue"}}),
		externalField("stripe", "stripe", contract.InferredExternal{
			Sources: []contract.InferredExternalSource{
				{Kind: "helmValues", Path: "chart/values.yaml", Line: 31, Key: "STRIPE_API_BASE", Host: "api.stripe.com"},
				{Kind: "sdk", Path: "go.mod", Line: 5}},
			Client: &contract.InferredClient{SDK: "github.com/stripe/stripe-go/v76", Major: &major}}),
	}
	got := ExternalSummary(fields, 0)
	want := []string{
		"External dependencies: 3 (rds:payments-db, sqs:orders-queue, stripe), inferred",
		"dependencies[payments-db]   external, from infra/main.tf:4 (aws_db_instance payments-db, possible)",
		"dependencies[orders-queue]  external, from infra/main.tf:12 (aws_sqs_queue orders-queue, possible)",
		"dependencies[stripe]        external, from chart/values.yaml:31 (STRIPE_API_BASE) and go.mod:5 (github.com/stripe/stripe-go/v76, possible)",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestExternalSummaryNoneFound(t *testing.T) {
	got := ExternalSummary([]contract.Inferred{{Field: "image"}}, 0)
	if !slices.Equal(got, []string{"External dependencies: none found, inferred"}) {
		t.Fatalf("got %q", got)
	}
}

func TestExternalSummaryNoneFoundWithUnknowns(t *testing.T) {
	for n, want := range map[int]string{
		2: "External dependencies: none found in what was read, 2 sources not read, inferred",
		1: "External dependencies: none found in what was read, 1 source not read, inferred",
	} {
		if got := ExternalSummary(nil, n); !slices.Equal(got, []string{want}) {
			t.Errorf("unread %d: got %q", n, got)
		}
	}
	// Externals found with inputs unread still say what was not read.
	got := ExternalSummary([]contract.Inferred{externalField("stripe", "stripe", contract.InferredExternal{
		Sources: []contract.InferredExternalSource{{Kind: "env", Path: "deploy.yaml"}}})}, 3)
	if got[len(got)-1] != "3 sources not read" || got[1] != "dependencies[stripe]  external, from deploy.yaml" {
		t.Fatalf("got %q", got)
	}
}

func TestExternalSummaryCapsLabels(t *testing.T) {
	var fields []contract.Inferred
	for i := range 12 {
		id := fmt.Sprintf("vendor%02d", i)
		fields = append(fields, externalField(id, id, contract.InferredExternal{}))
	}
	got := ExternalSummary(fields, 0)[0]
	want := "External dependencies: 12 (vendor00, vendor01, vendor02, vendor03, vendor04, vendor05, vendor06, vendor07, vendor08, vendor09, 2 more), inferred"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestExternalLabelForms(t *testing.T) {
	for _, tc := range []struct {
		id   string
		res  *contract.InferredResource
		want string
	}{
		{"stripe", nil, "stripe"},
		{"host/example.com", nil, "host/example.com"},
		{"host/example.com", &contract.InferredResource{Type: "t", Name: "x"}, "host/example.com"},
		{"aws/rds", nil, "rds"},
		{"aws/rds", &contract.InferredResource{Type: "aws_db_instance"}, "rds"},
		{"aws/sqs", &contract.InferredResource{Type: "aws_sqs_queue", Name: "orders"}, "sqs:orders"},
		{"aws/sqs/123456789012/eu-west-1/orders", nil, "sqs:orders"},
		{"a/b/c", nil, "a/b/c"},
	} {
		if got := externalLabel(externalField("n", tc.id, contract.InferredExternal{Resource: tc.res})); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestExternalSummarySourceNotes(t *testing.T) {
	x := &contract.InferredExternal{Identity: "stripe", Client: &contract.InferredClient{SDK: "stripe"}, Sources: []contract.InferredExternalSource{
		{Kind: "sdk", Path: "package.json", Line: 3}, {Kind: "sdk", Path: "pom.xml"},
		{Kind: "crossplane", Path: "db.yaml", Line: 1}, {Kind: "configMap", Path: "cm.yaml", Line: 2},
	}}
	want := "package.json:3 (SDK, possible) and pom.xml (SDK, possible) and db.yaml:1 (crossplane, possible) and cm.yaml:2"
	if got := externalFrom(x); got != want {
		t.Fatalf("got %q", got)
	}
}

// TestPlanExternalSummaryCountsUnread: the plan carries the summary, which
// counts the Terraform state the folder holds and an organization catalog
// that could not be read, named under Unknown.
func TestPlanExternalSummaryCountsUnread(t *testing.T) {
	why := "The organization catalog oci://r.example/cat@sha256:abc could not be read"
	r := planExternals(t, Options{Dir: fixtureDir(t, "folder-externals"), DescribeOnly: true, CatalogUnavailable: why})
	if len(r.ExternalSummary) == 0 || !strings.HasPrefix(r.ExternalSummary[0], "External dependencies: 3 (") ||
		r.ExternalSummary[len(r.ExternalSummary)-1] != "2 sources not read" || !slices.Contains(r.Unknown, why) {
		t.Fatalf("summary %q, unknown %q", r.ExternalSummary, r.Unknown)
	}
}
