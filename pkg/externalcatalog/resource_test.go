package externalcatalog

import (
	"bufio"
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func TestCatalogSDKModuleEcosystems(t *testing.T) {
	ok := head + "  - identity: a\n    class: vendor-api\n    sdkModules: [github.com/a/a-go, \"npm:@a/sdk\", \"npm:a\", \"pypi:a-sdk\", \"maven:com.a:a-java\"]\n"
	if err := mustParse(t, ok).Validate(); err != nil {
		t.Fatalf("valid modules refused: %v", err)
	}
	for _, bad := range []string{"github.com/a/a-go/v76", "github.com", "npm:A", "npm:@a", "pypi:A_SDK", "pypi:a--b", "pypi:a.b", "maven:com.a", "maven:com.a:", "gem:a"} {
		got := validateErr(t, head+"  - identity: a\n    class: vendor-api\n    sdkModules: [\""+bad+"\"]\n")
		wantContains(t, got, "entries[0].sdkModules", bad)
	}
	got := validateErr(t, head+"  - identity: a\n    class: vendor-api\n    sdkModules: [\"pypi:a\"]\n  - identity: b\n    class: vendor-api\n    sdkModules: [\"pypi:a\"]\n")
	wantContains(t, got, "entries[1].sdkModules", "already claimed")
}

func TestCatalogValidatesResourceRows(t *testing.T) {
	ok := head + "  - identity: aws/rds\n    class: managed-cloud\n    terraform: [aws_db_instance, \"aws_rds_cluster:cluster_identifier\"]\n    crossplane: [rds.aws.upbound.io/Instance]\n"
	if err := mustParse(t, ok).Validate(); err != nil {
		t.Fatalf("valid rows refused: %v", err)
	}
	for field, bad := range map[string]string{"terraform": "AWS_DB", "crossplane": "rds/Instance"} {
		got := validateErr(t, head+"  - identity: aws/rds\n    class: managed-cloud\n    "+field+": [\""+bad+"\"]\n")
		wantContains(t, got, "entries[0]."+field, bad)
	}
	for _, field := range []string{"terraform: [aws_db_instance]", "crossplane: [rds.aws.upbound.io/Instance]"} {
		got := validateErr(t, head+"  - identity: stripe\n    class: vendor-api\n    "+field+"\n")
		wantContains(t, got, "entries[0]", "managed-cloud")
	}
	got := validateErr(t, head+"  - identity: aws/rds\n    class: managed-cloud\n    terraform: [aws_db_instance]\n    crossplane: [rds.aws.upbound.io/Instance]\n"+
		"  - identity: aws/x\n    class: managed-cloud\n    terraform: [\"aws_db_instance:identifier\"]\n    crossplane: [rds.aws.upbound.io/Instance]\n")
	wantContains(t, got, "entries[1].terraform", "entries[1].crossplane")
}

func TestCatalogRefusesSensitiveTerraformAttribute(t *testing.T) {
	got := validateErr(t, head+"  - identity: aws/rds\n    class: managed-cloud\n    terraform: [\"aws_db_instance:password\"]\n")
	wantContains(t, got, "entries[0].terraform", "aws_db_instance:password", "sensitive")
}

func TestMergeOrgClaimsSDKAndResourceRows(t *testing.T) {
	def := mustParse(t, head+"  - identity: aws/rds\n    class: managed-cloud\n    sdkModules: [github.com/aws/rds]\n    terraform: [\"aws_db_instance:identifier\"]\n    crossplane: [rds.aws.upbound.io/Instance]\n")
	org := mustParse(t, head+"  - identity: corp/db\n    class: managed-cloud\n    sdkModules: [github.com/aws/rds]\n    terraform: [aws_db_instance]\n    crossplane: [rds.aws.upbound.io/Instance]\n")
	c := Merge(def, DefaultRef(), org, &Ref{Origin: OriginOrganization, Digest: "sha256:org"})
	e, _, _ := c.Entry("aws/rds")
	if len(e.SDKModules)+len(e.Terraform)+len(e.Crossplane) != 0 {
		t.Fatalf("the default entry keeps rows the organization claims: %+v", e)
	}
	if m, attr, ok := c.MatchTerraform("aws_db_instance"); !ok || m.Identity != "corp/db" || attr != "" || m.Origin != OriginOrganization {
		t.Fatalf("MatchTerraform = %+v %q %v", m, attr, ok)
	}
}

func TestCatalogEntryManagedFallback(t *testing.T) {
	c := Default()
	if e, _, ok := c.Entry("aws/rds/123456789012/eu-west-1/orders"); !ok || e.Identity != "aws/rds" {
		t.Fatalf("five segments = %+v %v", e, ok)
	}
	if _, _, ok := c.Entry("aws/rds/123456789012/eu-west-1"); ok {
		t.Fatal("a four-segment identity found an entry")
	}
}

func TestMatchSDKGoStripsMajorAndSubpath(t *testing.T) {
	c := Default()
	for name, want := range map[string]string{
		"github.com/stripe/stripe-go/v76":                "stripe",
		"github.com/stripe/stripe-go":                    "stripe",
		"github.com/aws/aws-sdk-go-v2/service/sqs/types": "aws/sqs",
		"github.com/aws/aws-sdk-go-v2/service/sqs/v2":    "aws/sqs",
	} {
		if m, ok := c.MatchSDK("go", name); !ok || string(m.Identity) != want || m.Kind != KindSDK || m.Entry != want || m.Origin != OriginDefault {
			t.Errorf("MatchSDK(go, %q) = %+v %v", name, m, ok)
		}
	}
	for _, name := range []string{"github.com/stripe/stripe-gox", "github.com/stripe"} {
		if m, ok := c.MatchSDK("go", name); ok {
			t.Errorf("MatchSDK(go, %q) matched %+v", name, m)
		}
	}
	if _, ok := c.MatchSDK("rubygems", "stripe"); ok {
		t.Error("an unknown ecosystem matched")
	}
}

func TestMatchSDKPyPINormalizes(t *testing.T) {
	c := mustCatalog(t, head+"  - identity: stripe\n    class: vendor-api\n    sdkModules: [\"pypi:stripe\", \"pypi:stripe-extra\", \"npm:@stripe/x\", \"maven:com.stripe:stripe-java\"]\n")
	for eco, names := range map[string][]string{"pypi": {"Stripe", "stripe", "Stripe_Extra", "stripe.extra"}, "npm": {"@stripe/x"}, "maven": {"com.stripe:stripe-java"}} {
		for _, n := range names {
			if m, ok := c.MatchSDK(eco, n); !ok || m.Identity != "stripe" {
				t.Errorf("MatchSDK(%s, %q) = %+v %v", eco, n, m, ok)
			}
		}
	}
	if _, ok := c.MatchSDK("npm", "stripe"); ok {
		t.Error("npm:stripe is not listed")
	}
}

func TestMatchSDKAlwaysPossible(t *testing.T) {
	m, ok := Default().MatchSDK("pypi", "stripe")
	if !ok || m.Confidence != ConfidencePossible {
		t.Fatalf("MatchSDK = %+v %v", m, ok)
	}
}

func TestMatchTerraformReturnsIdAttribute(t *testing.T) {
	c := Default()
	m, attr, ok := c.MatchTerraform("aws_sqs_queue")
	if !ok || m.Identity != "aws/sqs" || attr != "name" || m.Kind != KindResource || m.Confidence != ConfidencePossible || m.Class != ClassManagedCloud {
		t.Fatalf("MatchTerraform = %+v %q %v", m, attr, ok)
	}
	if _, _, ok := c.MatchTerraform("aws_instance"); ok {
		t.Fatal("aws_instance matched")
	}
}

func TestMatchCrossplane(t *testing.T) {
	c := Default()
	m, ok := c.MatchCrossplane("sqs.aws.upbound.io", "Queue")
	if !ok || m.Identity != "aws/sqs" || m.Kind != KindResource || m.Confidence != ConfidencePossible {
		t.Fatalf("MatchCrossplane = %+v %v", m, ok)
	}
	if _, ok := c.MatchCrossplane("sqs.aws.upbound.io", "queue"); ok {
		t.Fatal("the kind is compared exactly")
	}
}

func TestMatchOriginNamesCatalog(t *testing.T) {
	c := orgCatalog(t)
	if m, ok := matchHost(t, c, "https://erp.corp.example"); !ok || m.Origin != OriginOrganization {
		t.Fatalf("organization entry = %+v %v", m, ok)
	}
	if m, ok := matchHost(t, c, "https://api.stripe.com"); !ok || m.Origin != OriginDefault {
		t.Fatalf("default entry = %+v %v", m, ok)
	}
	if m, ok := matchHost(t, c, "https://mq.corp-legacy.co.uk"); !ok || m.Origin != "" {
		t.Fatalf("an unclassified host has no origin: %+v %v", m, ok)
	}
}

// rowKeyRE is a key whose values each need a cited source.
var rowKeyRE = regexp.MustCompile(`^\s+(sdkModules|terraform|crossplane):(.*)$`)

func TestDefaultCatalogValidates(t *testing.T) {
	if err := mustParse(t, string(DefaultBytes())).Validate(); err != nil {
		t.Fatalf("default catalog: %v", err)
	}
	inRows := false
	sc := bufio.NewScanner(bytes.NewReader(DefaultBytes()))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if m := rowKeyRE.FindStringSubmatch(line); m != nil {
			inRows = strings.TrimSpace(m[2]) == "" || strings.HasPrefix(strings.TrimSpace(m[2]), "#")
			if !inRows && !strings.Contains(line, "# source: https://") {
				t.Errorf("line %d has no source: %s", n, line)
			}
			continue
		}
		if inRows && strings.HasPrefix(line, "      - ") {
			if !strings.Contains(line, "# source: https://") {
				t.Errorf("line %d has no source: %s", n, line)
			}
			continue
		}
		inRows = false
	}
	for _, want := range []string{"pypi:stripe", "aws_sqs_queue:name", "sqs.aws.upbound.io/Queue"} {
		if !bytes.Contains(DefaultBytes(), []byte(want)) {
			t.Errorf("default catalog lacks %s", want)
		}
	}
}
