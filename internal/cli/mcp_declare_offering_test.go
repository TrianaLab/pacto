package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// offeringContribution pushes xbucket, an inferred offering contributed by
// the installation of cluster c1, signed by its installation key, and
// writes the detection that advertises that key and repository. It returns
// the contribution's ref and the detection file.
func (w *declareWorld) offeringContribution(t *testing.T) (string, string) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, err := signing.FromPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	root, producer := w.host+"/acme", "idp-c1"
	repo, tag := contract.ContributionRepository(root, producer), "xbucket_0.0.0-inferred.2.0"
	files := map[string]string{
		"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: xbucket\n  version: 0.0.0-inferred.2.0\n  offering: xbucket\n  summary: Object storage bucket\n" +
			"configurations:\n  - name: provisioning\n    schema: configuration/provisioning.schema.json\n    required: true\n",
		"configuration/provisioning.schema.json": `{"type":"object","additionalProperties":false,"required":["class"],"properties":{` +
			`"class":{"type":"string","enum":["large","small"],"x-pacto-tier":"contract"}}}`,
	}
	fsys := fstest.MapFS{}
	for p, c := range files {
		fsys[p] = &fstest.MapFile{Data: []byte(c), Mode: 0o644}
	}
	c, err := contract.Parse(strings.NewReader(files["pacto.yaml"]))
	if err != nil {
		t.Fatal(err)
	}
	img, err := oci.BundleImage(&contract.Bundle{Contract: c, RawYAML: []byte(files["pacto.yaml"]), FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	b := &contract.Bundle{Contract: c, RawYAML: []byte(files["pacto.yaml"]), FS: fsys}
	digest, err := oci.BundleDigest(b)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := v1.NewHash(digest)
	reg := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), w.host)
	d, err := reg.Write(t.Context(), repo, digestedImage{img, h})
	if err == nil {
		err = reg.Tag(t.Context(), repo, d, tag)
	}
	if err == nil {
		err = publish.SignDigest(t.Context(), reg, signing.Keyed(kp, ""), repo, d)
	}
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(kp.GetPublicKey())
	asOf := time.Now().Add(-time.Minute)
	res := detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: time.Now(), Cluster: "c1",
		Installation: &detect.Installation{Registry: root, SnapshotKey: base64.StdEncoding.EncodeToString(der), SnapshotKeyID: "installation",
			Offerings: &detect.OfferingsAd{Producer: producer, Repository: repo, Observed: []string{"xbucket"}, AsOf: &asOf}}}
	raw, _ := json.Marshal(res)
	detected := filepath.Join(t.TempDir(), "detection.json")
	if err := os.WriteFile(detected, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return "oci://" + repo + ":" + tag, detected
}

// digestedImage is an image whose digest is known before its layers stream.
type digestedImage struct {
	v1.Image
	h v1.Hash
}

func (d digestedImage) Digest() (v1.Hash, error) { return d.h, nil }

// stdioOfferingServer is `pacto mcp --detected detected` over stdio.
func stdioOfferingServer(t *testing.T, detected string) *mcpsdk.Server {
	t.Helper()
	d := defaultDeps()
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v", d)
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Set("detected", detected); err != nil {
		t.Fatal(err)
	}
	server, err := buildMCPServer(cmd, svc, "v", nil, d)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func (w *declareWorld) offeringArgs(from string) map[string]any {
	return map[string]any{"service": "offering:xbucket", "from": from, "version": "1.0.0", "owner": "team-storage", "registry": w.host + "/acme/offerings"}
}

func TestMCPDeclareOfferingWritesDraft(t *testing.T) {
	w := newDeclareWorld(t)
	from, detected := w.offeringContribution(t)
	out := declareTwice(t, func() *mcpsdk.Server { return stdioOfferingServer(t, detected) }, w.offeringArgs(from))
	if !out.Executed || out.Draft == nil || out.Draft.Tag != "draft-1" || out.Draft.Signed ||
		!strings.HasPrefix(out.AcceptCLI, "pacto declare offering:xbucket --from "+from) {
		t.Fatalf("%+v", out)
	}
	b := w.pulledClaim(t, "offerings/xbucket", "draft-1")
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil || rec.Declaration == nil {
		t.Fatalf("%v %+v", err, rec)
	}
	producer, svc, _, ok := contract.InferredFromOffering(rec.Declaration.InferredFrom)
	if d := rec.Declaration; !ok || producer != "idp-c1" || svc != "xbucket" || d.State != contract.DeclarationDraft || d.OnBehalfOf != "ana" ||
		d.Asker.Class != contract.SignerClassAgent {
		t.Fatalf("%s", rec.Marshal())
	}
}

// TestMCPDeclareOfferingUnverifiedRefused: without the detection no key
// verifies the contribution, so nothing records an unverified provenance.
func TestMCPDeclareOfferingUnverifiedRefused(t *testing.T) {
	w := newDeclareWorld(t)
	from, _ := w.offeringContribution(t)
	plan, res := callTool(t, stdioOfferingServer(t, ""), "pacto_declare", w.offeringArgs(from))
	if !res.IsError || plan.Refusal == nil || plan.Refusal.Code != pactomcp.RefusalOfferingSourceUnverified {
		t.Fatalf("%+v", plan)
	}
}

// TestMCPDeclareOfferingIgnoresAmbientToken: a person's ambient token never
// signs an agent's acceptance, which stays an unsigned draft.
func TestMCPDeclareOfferingIgnoresAmbientToken(t *testing.T) {
	w := newDeclareWorld(t)
	from, detected := w.offeringContribution(t)
	t.Setenv("SIGSTORE_ID_TOKEN", personToken())
	out := declareTwice(t, func() *mcpsdk.Server { return stdioOfferingServer(t, detected) }, w.offeringArgs(from))
	if out.Draft == nil || out.Draft.Signed || out.Draft.Tag != "draft-1" {
		t.Fatalf("%+v", out)
	}
	tags, _ := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), w.host).Tags(t.Context(), w.host+"/acme/offerings/xbucket")
	if slices.Contains(tags, "1.0.0") {
		t.Fatalf("an agent's acceptance published a declared version: %v", tags)
	}
}

// contributionFleet is a view that lists xbucket, contributed at ref.
func contributionFleet(ref string) *pactomcp.FleetMode {
	snap := &fleet.FleetSnapshot{
		Services: map[fleet.ServiceKey]*fleet.ServiceRecord{"c/xbucket": {Key: "c/xbucket", Offering: "xbucket", Revisions: []fleet.RevisionKey{"r", "gone", "noref"},
			Contribution: &fleet.ServiceContribution{Producer: "idp-c1"}}, "ledger": {Key: "ledger"}},
		Revisions: map[fleet.RevisionKey]*fleet.ContractRevision{"r": {ResolvedRef: ref, Digest: "sha256:abc"}, "noref": {}},
	}
	return &pactomcp.FleetMode{Src: func(context.Context) (*fleet.Query, error) { return fleet.NewQuery(snap), nil }}
}

// TestRemoteDeclareOfferingOnlyFromVisibleContribution: over HTTP from must
// name a contribution the caller's view lists, by ref or digest; anything
// else is one refusal, before any read.
func TestRemoteDeclareOfferingOnlyFromVisibleContribution(t *testing.T) {
	ref := "ghcr.io/acme/pacto-inferred/idp-c1/services:xbucket_0.0.0-inferred.2.0"
	a := pactomcp.DeclareArgs{Service: "offering:xbucket", Version: "1.0.0", Owner: "team-storage", Registry: "ghcr.io/acme/offerings"}
	for name, c := range map[string]struct {
		mode *pactomcp.FleetMode
		from string
	}{
		"no fleet":   {nil, "oci://" + ref},
		"not listed": {contributionFleet(ref), "oci://ghcr.io/acme/pacto-inferred/idp-c1/services:other"},
		"no from":    {contributionFleet(ref), ""},
	} {
		rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://ghcr.io/acme/offerings"}, c.mode)
		a.From = c.from
		for _, run := range []func(context.Context, pactomcp.DeclareArgs) (json.RawMessage, *pactomcp.Refused, error){rd.Plan, httpWriting(rd).Execute} {
			raw, refused, err := run(remoteCtx(nil), a)
			if err != nil || raw != nil || refused == nil || refused.Code != pactomcp.RefusalNotAllowed || !strings.Contains(refused.Message, "lists no inferred offering") {
				t.Errorf("%s: %s %+v %v", name, raw, refused, err)
			}
		}
	}
	q, _ := contributionFleet(ref).Src(context.Background())
	if !listsContribution(q, "oci://"+ref) || !listsContribution(q, "ghcr.io/acme/pacto-inferred/idp-c1/services@sha256:abc") {
		t.Fatal("a listed contribution was refused")
	}
	// A listed contribution plans with the server's detection, read with the
	// caller's own client; the plan's refusal is its own.
	rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://ghcr.io/acme/offerings"}, contributionFleet(ref))
	rd.detected = "k8s:pacto-system/pacto-operator-detected"
	req := app.DeclareRequest{Service: "offering:xbucket"}
	rd.offering(&req)
	if req.Detected != rd.detected {
		t.Fatalf("%+v", req)
	}
	req = app.DeclareRequest{Service: "invoices"}
	rd.offering(&req)
	if req.Detected != "" {
		t.Fatalf("a workload took the detection: %+v", req)
	}
}
