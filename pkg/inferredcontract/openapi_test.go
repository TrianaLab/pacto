package inferredcontract

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// spec is a stripe OpenAPI link at the registry with digest hex c repeated.
func spec(c string) string {
	return "oci://registry.example.com/vendors/stripe-openapi@sha256:" + strings.Repeat(c, 64)
}

// orgCatalog is the default catalog plus an organization stripe entry that
// links openapi ("" links none).
func orgCatalog(t *testing.T, openapi string) *externalcatalog.Catalog {
	t.Helper()
	y := "apiVersion: catalog.pacto.trianalab.io/v1\nentries:\n  - identity: stripe\n    class: vendor-api\n    hosts: [api.stripe.com]\n"
	if openapi != "" {
		y += "    openapi: " + openapi + "\n"
	}
	org, err := externalcatalog.ParseFile([]byte(y))
	if err != nil || org.Validate() != nil {
		t.Fatalf("org catalog: %v", err)
	}
	def, _ := externalcatalog.ParseFile(externalcatalog.DefaultBytes())
	return externalcatalog.Merge(def, externalcatalog.DefaultRef(), org, &externalcatalog.Ref{Origin: externalcatalog.OriginOrganization, Digest: "sha256:" + strings.Repeat("0", 64)})
}

// catalogFleet is checkout calling stripe at api.stripe.com, classified by
// cat (nil: the default catalog) with the catalog limitations lims.
func catalogFleet(t *testing.T, cat *externalcatalog.Catalog, lims ...fleet.Limitation) *fleet.FleetSnapshot {
	t.Helper()
	return fixture{svcs: []svc{{name: "checkout", ns: "shop"}}, uses: []fleet.RawExternalUse{use("checkout", "stripe", "api.stripe.com", 0)},
		cat: cat, catLims: lims}.build(t)
}

// linkAt is the OpenAPI link the tag ref records.
func linkAt(t *testing.T, reg publish.Registry, ref string) string {
	t.Helper()
	b, err := reg.Pull(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return openAPILink(b)
}

func stripeBundle(t *testing.T, version, openapi string) *contract.Bundle {
	t.Helper()
	snap := stripeFleet(t, []string{"api.stripe.com"})
	b, err := Bundle("stripe", Surface(snap, "stripe", now), version, inputs(Deps{Pacto: "4.7.0"}, snap), openapi)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBundleWritesOpenAPILink(t *testing.T) {
	b := stripeBundle(t, "0.0.1", spec("a"))
	want := "pactoVersion: \"2.5\"\nservice:\n  name: stripe\n  version: 0.0.1\n  external: stripe\nmetadata:\n  run.pacto.external.openapi: \"" + spec("a") + "\"\n"
	if string(b.RawYAML) != want {
		t.Errorf("pacto.yaml = %q", b.RawYAML)
	}
}

func TestBundleOpenAPILinkIsQuotedYAML(t *testing.T) {
	b := stripeBundle(t, "0.0.1", spec("a"))
	c, err := contract.Parse(strings.NewReader(string(b.RawYAML)))
	if err != nil || c.Metadata[OpenAPIMetadataKey] != spec("a") || openAPILink(b) != spec("a") {
		t.Errorf("parsed back = %+v, %v", c, err)
	}
}

func TestInferredBundleWithLinkValidates(t *testing.T) {
	b := stripeBundle(t, "0.0.1", spec("a"))
	if res := validation.Validate(b.Contract, b.RawYAML, b.FS); !res.IsValid() || b.Contract.PactoVersion != "2.5" {
		t.Errorf("validation = %+v", res.Errors)
	}
}

func TestChangedOnOpenAPIDigest(t *testing.T) {
	ctx := context.Background()
	if !Changed(ctx, stripeBundle(t, "0.0.1", spec("a")), stripeBundle(t, "0.0.2", spec("b"))) {
		t.Error("a moved digest is no change")
	}
	if !Changed(ctx, stripeBundle(t, "0.0.1", ""), stripeBundle(t, "0.0.2", spec("b"))) {
		t.Error("a link added is no change")
	}
}

func TestChangedIgnoresSameOpenAPI(t *testing.T) {
	if Changed(context.Background(), stripeBundle(t, "0.0.1", spec("a")), stripeBundle(t, "0.0.2", spec("a"))) {
		t.Error("the same link is a change")
	}
}

func TestChangedIgnoresRehostedSameDigest(t *testing.T) {
	rehosted := "https://raw.example.com/stripe/spec3.json@sha256:" + strings.Repeat("a", 64)
	if Changed(context.Background(), stripeBundle(t, "0.0.1", spec("a")), stripeBundle(t, "0.0.2", rehosted)) {
		t.Error("a document re-hosted under the same digest is a change")
	}
}

func TestPublishOnClientMajorChange(t *testing.T) {
	host, reg := testRegistry(t)
	d, root := deps(t, reg), host+"/demo/pacto-inferred"
	v76, v77 := 76, 77
	publishOK(t, d, root, fixture{svcs: []svc{{name: "checkout", ns: "shop", fsys: sdkRecord(&v76)}}}.build(t))
	rep := publishOK(t, d, root, fixture{svcs: []svc{{name: "checkout", ns: "shop", fsys: sdkRecord(&v77)}}}.build(t))
	if len(rep.Published) != 1 || rep.Published[0].Version != "0.0.2" {
		t.Fatalf("report = %+v", rep)
	}
	if c := surfaceAt(t, reg, root+"/stripe:0.0.2").Clients; len(c) != 1 || c[0].Value != "stripe-go/77" {
		t.Errorf("clients = %+v", c)
	}
}

// publishLinked publishes stripe once with an organization catalog that
// links spec("a") and returns the registry, deps and root.
func publishLinked(t *testing.T) (publish.Registry, Deps, string) {
	t.Helper()
	host, reg := testRegistry(t)
	d, root := deps(t, reg), host+"/demo/pacto-inferred"
	if rep := publishOK(t, d, root, catalogFleet(t, orgCatalog(t, spec("a")))); len(rep.Published) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if got := linkAt(t, reg, root+"/stripe:0.0.1"); got != spec("a") {
		t.Fatalf("link = %q", got)
	}
	return reg, d, root
}

// wantOneTag checks the run published nothing and stripe still has only
// 0.0.1, linking spec("a").
func wantOneTag(t *testing.T, reg publish.Registry, root string, rep Report) {
	t.Helper()
	tags, _ := reg.Tags(context.Background(), root+"/stripe")
	if len(rep.Published) != 0 || !slices.Equal(tags, []string{"0.0.1"}) {
		t.Errorf("report = %+v, tags %v", rep, tags)
	}
}

func TestPartialRunKeepsOpenAPILink(t *testing.T) {
	reg, d, root := publishLinked(t)
	snap := catalogFleet(t, orgCatalog(t, ""))
	snap.Completeness = fleet.CompletenessPartial
	wantOneTag(t, reg, root, publishOK(t, d, root, snap))
}

func TestCatalogUnavailableKeepsOpenAPILink(t *testing.T) {
	reg, d, root := publishLinked(t)
	lim := fleet.Limitation{Code: fleet.LimitationExternalCatalogUnavailable, Source: "external-catalog", Message: "the organization catalog could not be read"}
	wantOneTag(t, reg, root, publishOK(t, d, root, catalogFleet(t, nil, lim)))
}

func TestDefaultCatalogRunKeepsOpenAPILink(t *testing.T) {
	reg, d, root := publishLinked(t)
	wantOneTag(t, reg, root, publishOK(t, d, root, catalogFleet(t, nil)))
}

func TestOrganizationEntryWithoutLinkRemovesIt(t *testing.T) {
	reg, d, root := publishLinked(t)
	rep := publishOK(t, d, root, catalogFleet(t, orgCatalog(t, "")))
	if len(rep.Published) != 1 || rep.Published[0].Version != "0.0.2" {
		t.Fatalf("report = %+v", rep)
	}
	if got := linkAt(t, reg, root+"/stripe:0.0.2"); got != "" {
		t.Errorf("link = %q", got)
	}
}

func TestOperatorAndCLIPublishersDoNotFlap(t *testing.T) {
	reg, d, root := publishLinked(t)
	for range 5 {
		wantOneTag(t, reg, root, publishOK(t, d, root, catalogFleet(t, nil)))
		wantOneTag(t, reg, root, publishOK(t, d, root, catalogFleet(t, orgCatalog(t, spec("a")))))
	}
}

func TestRepairNeverSignsForeignOpenAPILink(t *testing.T) {
	// "no link" is a default-catalog run, where the keep rule would apply.
	for name, link := range map[string]string{"another link": spec("a"), "no link": ""} {
		t.Run(name, func(t *testing.T) {
			var run *externalcatalog.Catalog
			if link != "" {
				run = orgCatalog(t, link)
			}
			host, reg := testRegistry(t)
			d, root := deps(t, reg), host+"/demo/pacto-inferred"
			repo := root + "/stripe"
			snap := catalogFleet(t, run)
			foreign, err := Bundle("stripe", Surface(snap, "stripe", now), "0.0.1", inputs(d, snap), spec("f"))
			if err != nil {
				t.Fatal(err)
			}
			digest := pushUnsigned(t, reg, repo, "0.0.1", foreign)
			rep := publishOK(t, d, root, snap)
			if len(rep.Repaired) != 0 || len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0].Error, "never used") {
				t.Fatalf("report = %+v", rep)
			}
			if err := publish.VerifyDigest(context.Background(), reg, d.Verify, repo, digest); err == nil {
				t.Error("the installation key signed a foreign link")
			}
			if len(rep.Published) != 1 || linkAt(t, reg, repo+":0.0.2") == spec("f") {
				t.Errorf("published = %+v", rep.Published)
			}
		})
	}
}

func TestRepairSignsOwnCrashedPushWithLink(t *testing.T) {
	host, reg := testRegistry(t)
	d, root := deps(t, reg), host+"/demo/pacto-inferred"
	signer := d.Signer
	d.Signer = failSigner{}
	snap := catalogFleet(t, orgCatalog(t, spec("a")))
	if rep := publishOK(t, d, root, snap); len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0].Error, "signing 0.0.1") {
		t.Fatalf("crashed push = %+v", rep)
	}
	d.Signer = signer
	rep := publishOK(t, d, root, snap)
	if len(rep.Repaired) != 1 || rep.Repaired[0].Version != "0.0.1" || rep.Unchanged != 1 || len(rep.Published) != 0 {
		t.Fatalf("repair = %+v", rep)
	}
	if got := linkAt(t, reg, root+"/stripe:0.0.1"); got != spec("a") {
		t.Errorf("link = %q", got)
	}
}
