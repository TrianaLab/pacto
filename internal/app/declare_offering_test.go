package app

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// closedOffering is a closed provisioning schema: a class enum and a
// bounded size.
const closedOffering = `{"type":"object","additionalProperties":false,"required":["class"],"properties":{` +
	`"class":{"type":"string","enum":["large","small"],"x-pacto-tier":"contract"},` +
	`"storageGB":{"type":"integer","minimum":1,"maximum":100,"x-pacto-tier":"contract"}}}`

// openOffering leaves tags untyped and backups an open object.
const openOffering = `{"type":"object","additionalProperties":false,"properties":{` +
	`"class":{"type":"string","enum":["small"]},"tags":{"description":"anything"},` +
	`"backups":{"type":"object","properties":{"days":{"type":"integer"}}}}}`

// offeringProducer is the installation of offeringCluster that produces
// inferred offerings.
const offeringProducer = "idp-" + offeringCluster

// offeringWorld is a declare world with an installation producer whose key
// is kp and whose detection file is detected.
type offeringWorld struct {
	*declareEnv
	kp       *signing.Keypair
	repo     string
	detected string
}

func newOfferingWorld(t *testing.T) *offeringWorld {
	t.Helper()
	e := newDeclareEnv(t)
	w := &offeringWorld{declareEnv: e, kp: newKeypair(t), repo: contract.ContributionRepository(e.host+"/acme", offeringProducer)}
	w.detected = w.detection(t, offeringDetection(t, e.host+"/acme", w.kp))
	return w
}

// detection writes res as a detection file and returns its path.
func (w *offeringWorld) detection(t *testing.T, res detect.Result) string {
	t.Helper()
	raw, _ := json.Marshal(res)
	p := filepath.Join(t.TempDir(), "detection.json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// contributionFiles is xbucket contributed at generation with schema.
func contributionFiles(generation int, schema string) map[string]string {
	return map[string]string{
		"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: xbucket\n  version: 0.0.0-inferred." + strconv.Itoa(generation) + ".0\n" +
			"  offering: xbucket\n  summary: Object storage bucket\nconfigurations:\n" +
			"  - name: provisioning\n    schema: configuration/provisioning.schema.json\n    required: true\n",
		"configuration/provisioning.schema.json": schema,
		"source/object.yaml":                     "apiVersion: example.io/v1\nkind: Definition\nmetadata:\n  name: xbuckets.storage.acme.io\n",
	}
}

// contribute pushes files to repo tagged for generation, signed by kp (nil
// for unsigned), and returns its ref and digest.
func (w *offeringWorld) contribute(t *testing.T, repo string, generation int, files map[string]string, kp *signing.Keypair) (string, string) {
	t.Helper()
	tag := "xbucket_0.0.0-inferred." + strconv.Itoa(generation) + ".0"
	var s *signing.Signer
	if kp != nil {
		k := signing.Keyed(kp, "")
		s = &k
	}
	d := w.publishBundle(t, repo, tag, files, s)
	return "oci://" + repo + ":" + tag, d
}

// xbucket is xbucket contributed closed at generation by the installation.
func (w *offeringWorld) xbucket(t *testing.T, generation int) (string, string) {
	t.Helper()
	return w.contribute(t, w.repo, generation, contributionFiles(generation, closedOffering), w.kp)
}

// offerReq is a person's acceptance of xbucket from from, owned by
// team-storage, at 1.0.0.
func (w *offeringWorld) offerReq(from string) DeclareRequest {
	return DeclareRequest{Service: "offering:xbucket", From: from, Detected: w.detected, Version: "1.0.0", Owner: "team-storage",
		Registry: w.host + "/acme/offerings", Dynamic: w.dyn,
		Signing: declareSigning{TrustedRoot: w.root, FulcioURL: w.ss.Fulcio, TimestampURL: w.ss.TSA}}
}

// accepted publishes xbucket at version from a fresh contribution at
// generation, signed by the default approver.
func (w *offeringWorld) accepted(t *testing.T, generation int, version string) *DeclareResult {
	t.Helper()
	from, _ := w.xbucket(t, generation)
	req := w.offerReq(from)
	req.Version = version
	w.login(personToken("admin@acme.com"))
	return w.declare(t, req)
}

// planRefusal plans req and returns its refusal.
func (w *offeringWorld) planRefusal(t *testing.T, req DeclareRequest) *DeclareError {
	t.Helper()
	_, _, err := w.svc.PlanDeclare(t.Context(), req)
	return refusal(t, err)
}

func TestAcceptOfferingPlanFromContribution(t *testing.T) {
	w := newOfferingWorld(t)
	from, digest := w.xbucket(t, 2)
	p, d := w.plan(t, w.offerReq(from))
	if p.Service != "xbucket" || p.Version != "1.0.0" || p.BundleRepo != w.host+"/acme/offerings/xbucket" || p.Owner != "team:team-storage" ||
		p.InferredFrom != contract.OfferingInferredFrom(offeringProducer, "xbucket", digest) || p.State != contract.DeclarationDeclared {
		t.Fatalf("%+v", p)
	}
	if !slices.Contains(p.Notes, "accepted from "+w.repo+"@"+digest+", signed by the installation key of cluster "+offeringCluster) {
		t.Fatalf("%v", p.Notes)
	}
	wantAcceptedFolder(t, d, p.InferredFrom)
}

// wantAcceptedFolder: d declares xbucket for team-storage from the
// contribution's offering section, without its source files, and records
// inferredFrom.
func wantAcceptedFolder(t *testing.T, d *declaration, inferredFrom string) {
	t.Helper()
	c := d.bundle.Contract
	if c.Service.Offering != "xbucket" || c.Service.Summary != "Object storage bucket" || c.Service.Owner.Team != "team-storage" ||
		len(c.Configurations) != 1 || c.Configurations[0].Name != "provisioning" {
		t.Fatalf("%s", d.raw)
	}
	if _, ok := d.files["source/object.yaml"]; ok || d.files["configuration/provisioning.schema.json"] == nil {
		t.Fatalf("files %v", d.files)
	}
	if d.record.Declaration.InferredFrom != inferredFrom || len(d.record.Fields) != 0 {
		t.Fatalf("%s", d.record.Marshal())
	}
}

func TestAcceptOfferingPublishesThroughDeclare(t *testing.T) {
	w := newOfferingWorld(t)
	w.withOwnerPolicy(t)
	res := w.accepted(t, 2, "1.0.0")
	if !res.Executed || res.Draft || !res.Signed || res.Tag != "1.0.0" {
		t.Fatalf("%+v", res)
	}
	b, err := w.reg.Pull(t.Context(), w.host+"/acme/offerings/xbucket:1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := contract.ReadInferredRecord(b.FS)
	if _, _, _, ok := contract.InferredFromOffering(rec.Declaration.InferredFrom); !ok || rec.Declaration.Asker.Subject != "admin@acme.com" ||
		b.Contract.Service.Version != "1.0.0" {
		t.Fatalf("%s", rec.Marshal())
	}
	if got := w.signers(t, res.Published); len(got) != 1 || got[0].Subject != "admin@acme.com" {
		t.Fatalf("%+v", got)
	}
}

func TestAcceptRefusesOpenSchema(t *testing.T) {
	w := newOfferingWorld(t)
	from, _ := w.contribute(t, w.repo, 2, contributionFiles(2, openOffering), w.kp)
	de := w.planRefusal(t, w.offerReq(from))
	if de.Code != codeOfferingSchemaOpen || !strings.Contains(de.Message, "provisioning: schema.properties.backups, provisioning: schema.properties.tags") {
		t.Fatalf("%+v", de)
	}
}

// offeringDir is a person's folder offering name with the configurations
// cfgs (YAML lines) and files.
func offeringDir(t *testing.T, name, cfgs string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files["pacto.yaml"] = "pactoVersion: \"2.5\"\nservice:\n  name: " + name + "\n  version: 0.1.0\n  offering: " + name + "\n" + cfgs
	for p, c := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const provisioningCfg = "configurations:\n  - name: provisioning\n    schema: configuration/provisioning.schema.json\n    required: true\n"

func TestAcceptOfferingDirClosesAndAccepts(t *testing.T) {
	w := newOfferingWorld(t)
	from, digest := w.contribute(t, w.repo, 2, contributionFiles(2, openOffering), w.kp)
	tuned := strings.Replace(closedOffering, `"maximum":100,"x-pacto-tier":"contract"`, `"maximum":100,"x-pacto-tier":"tunable"`, 1)
	req := w.offerReq(from)
	req.Dir = offeringDir(t, "xbucket", provisioningCfg, map[string]string{"configuration/provisioning.schema.json": tuned})
	p, d := w.plan(t, req)
	if p.InferredFrom != contract.OfferingInferredFrom(offeringProducer, "xbucket", digest) ||
		!strings.Contains(string(d.files["configuration/provisioning.schema.json"].Data), `"tunable"`) {
		t.Fatalf("%+v", p)
	}
}

func TestAcceptOfferingDirOpenConfigurations(t *testing.T) {
	w := newOfferingWorld(t)
	from, _ := w.xbucket(t, 2)
	req := w.offerReq(from)
	req.Dir = offeringDir(t, "xbucket", "configurations:\n  - name: a\n    required: false\n  - name: b\n    schema: missing.json\n    required: false\n",
		map[string]string{})
	if de := w.planRefusal(t, req); de.Code != codeOfferingSchemaOpen || !strings.Contains(de.Message, "a: no schema, b: schema unreadable") {
		t.Fatalf("%+v", de)
	}
	req.Dir = offeringDir(t, "other", "", map[string]string{})
	if de := w.planRefusal(t, req); de.Code != codeOfferingMismatch || !strings.Contains(de.Message, "the folder offers other") {
		t.Fatalf("%+v", de)
	}
	req.Dir = t.TempDir() // no pacto.yaml
	if _, _, err := w.svc.PlanDeclare(t.Context(), req); err == nil {
		t.Fatal("planned from an empty folder")
	}
	req.Dir = filepath.Join(t.TempDir(), "missing")
	if _, _, err := w.svc.PlanDeclare(t.Context(), req); err == nil {
		t.Fatal("planned from a missing folder")
	}
}

func TestAcceptOfferingFolderWriteFails(t *testing.T) {
	w := newOfferingWorld(t)
	from, _ := w.xbucket(t, 2)
	writes := 0
	w.svc.sys.writeFile = func(name string, data []byte, perm os.FileMode) error {
		if filepath.Base(name) == DefaultContractPath {
			if writes++; writes == 2 { // the named copy
				return errors.New("disk full")
			}
		}
		return os.WriteFile(name, data, perm)
	}
	if _, _, err := w.svc.PlanDeclare(t.Context(), w.offerReq(from)); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatal(err)
	}
}

func TestAcceptOfferingVersionRequired(t *testing.T) {
	w := newOfferingWorld(t)
	req := w.offerReq("oci://" + w.repo + ":xbucket_0.0.0-inferred.2.0")
	req.Version = ""
	if de := w.planRefusal(t, req); de.Code != codeVersionRequired || !strings.Contains(de.CLI, "--version <version>") {
		t.Fatalf("%+v", de)
	}
}

func TestAcceptOfferingVersionConflict(t *testing.T) {
	w := newOfferingWorld(t)
	for _, v := range []string{"1.0", "1.0.0-rc.1", "1.0.0+build"} {
		req := w.offerReq("oci://" + w.repo + ":xbucket_0.0.0-inferred.2.0")
		req.Version = v
		if de := w.planRefusal(t, req); de.Code != codeVersionConflict {
			t.Errorf("%s: %+v", v, de)
		}
	}
	w.withOwnerPolicy(t)
	w.accepted(t, 2, "1.0.0")
	from, _ := w.xbucket(t, 3)
	if de := w.planRefusal(t, w.offerReq(from)); de.Code != codeVersionConflict || !strings.Contains(de.Message, "already holds 1.0.0") {
		t.Fatalf("%+v", de)
	}
}

func TestAcceptOfferingSourceInvalid(t *testing.T) {
	w := newOfferingWorld(t)
	for _, from := range []string{"", "oci://" + w.host + "/acme/xbucket:1.0.0"} {
		if de := w.planRefusal(t, w.offerReq(from)); de.Code != codeOfferingSourceInvalid || !strings.Contains(de.CLI, "--from <contribution>") && from == "" {
			t.Errorf("%q: %+v", from, de)
		}
	}
	files := contributionFiles(2, closedOffering)
	files["pacto.yaml"] += "workload: service\n"
	from, _ := w.contribute(t, w.repo, 2, files, w.kp)
	if de := w.planRefusal(t, w.offerReq(from)); de.Code != codeOfferingSourceInvalid || !strings.Contains(de.Message, "it declares workload") {
		t.Fatalf("%+v", de)
	}
	files = contributionFiles(3, closedOffering)
	files[contract.InferredRecordFile] = "not: a record\n"
	from, _ = w.contribute(t, w.repo, 3, files, w.kp)
	if de := w.planRefusal(t, w.offerReq(from)); de.Code != codeOfferingSourceInvalid {
		t.Fatalf("%+v", de)
	}
	if de := w.planRefusal(t, w.offerReq("oci://"+w.repo+":xbucket_9.9.9")); de.Code != codeRegistryUnreadable {
		t.Fatalf("%+v", de)
	}
}

func TestAcceptOfferingPullFails(t *testing.T) {
	w := newOfferingWorld(t)
	from, _ := w.xbucket(t, 2)
	w.faulty(&faultyRegistry{failPull: 1})
	if de := w.planRefusal(t, w.offerReq(from)); de.Code != codeRegistryUnreadable {
		t.Fatalf("%+v", de)
	}
}

func TestAcceptOfferingMismatch(t *testing.T) {
	w := newOfferingWorld(t)
	_, digest := w.xbucket(t, 2)
	req := w.offerReq("oci://" + w.repo + "@" + digest) // by digest
	req.Service = "offering:other"
	if de := w.planRefusal(t, req); de.Code != codeOfferingMismatch || !strings.Contains(de.Message, "offers xbucket, not other") {
		t.Fatalf("%+v", de)
	}
	// The repository already holds a service that offers nothing.
	from, _ := w.xbucket(t, 3)
	admin := w.signer(t, personToken("admin@acme.com"))
	w.publishBundle(t, w.host+"/acme/offerings/xbucket", "0.9.0", map[string]string{
		"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: xbucket\n  version: 0.9.0\n"}, &admin)
	if de := w.planRefusal(t, w.offerReq(from)); de.Code != codeOfferingMismatch || !strings.Contains(de.Message, "which offers nothing") {
		t.Fatalf("%+v", de)
	}
}

func TestAcceptOfferingOwnerRequiredFirstTime(t *testing.T) {
	w := newOfferingWorld(t)
	from, _ := w.xbucket(t, 2)
	req := w.offerReq(from)
	req.Owner = ""
	if de := w.planRefusal(t, req); de.Code != codeOwnerRequired || !strings.Contains(de.CLI, "pacto declare offering:xbucket") {
		t.Fatalf("%+v", de)
	}
}

func TestAcceptOfferingRepositoryInferred(t *testing.T) {
	w := newOfferingWorld(t)
	req := w.offerReq("oci://" + w.repo + ":xbucket_0.0.0-inferred.2.0")
	req.Registry = w.host + "/acme/pacto-inferred/offerings"
	if de := w.planRefusal(t, req); de.Code != codeClaimRepositoryInferred || !strings.Contains(de.Message, "accept the offering into a registry path outside") {
		t.Fatalf("%+v", de)
	}
}

// TestAcceptOfferingCarriesBaselineChoices: a re-acceptance from a newer
// contribution keeps the baseline's summary and owner.
func TestAcceptOfferingCarriesBaselineChoices(t *testing.T) {
	w := newOfferingWorld(t)
	w.withOwnerPolicy(t)
	from, _ := w.xbucket(t, 2)
	req := w.offerReq(from)
	req.Dir = offeringDir(t, "xbucket", provisioningCfg, map[string]string{"configuration/provisioning.schema.json": closedOffering})
	raw, _ := os.ReadFile(filepath.Join(req.Dir, "pacto.yaml"))
	raw = []byte(strings.Replace(string(raw), "  offering: xbucket\n", "  offering: xbucket\n  summary: Buckets for every team\n", 1))
	if err := os.WriteFile(filepath.Join(req.Dir, "pacto.yaml"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	w.login(personToken("admin@acme.com"))
	w.declare(t, req)
	files := contributionFiles(3, closedOffering)
	files["pacto.yaml"] = strings.Replace(files["pacto.yaml"], "  summary: Object storage bucket\n", "", 1) // a producer that infers none
	from, _ = w.contribute(t, w.repo, 3, files, w.kp)
	again := w.offerReq(from)
	again.Owner, again.Version = "", "1.1.0"
	p, d := w.plan(t, again)
	c := d.bundle.Contract.Service
	if c.Summary != "Buckets for every team" || c.Owner.Team != "team-storage" || !slices.Contains(p.Notes, "carried from 1.0.0: summary, owner") {
		t.Fatalf("%v\n%s", p.Notes, d.raw)
	}
	// With --dir the person's folder stands as it is.
	again.Dir = req.Dir
	if _, d := w.plan(t, again); d.bundle.Contract.Service.Summary != "Buckets for every team" {
		t.Fatalf("%s", d.raw)
	}
}

func TestAcceptOfferingAgentDrafts(t *testing.T) {
	w := newOfferingWorld(t)
	from, _ := w.xbucket(t, 2)
	req := w.offerReq(from)
	req.Agent, req.OnBehalfOf = true, "ana"
	res := w.declare(t, req)
	if !res.Draft || res.Tag != "draft-1" || !strings.HasPrefix(res.Plan.AcceptCLI, "pacto declare offering:xbucket --from "+from) ||
		!strings.HasSuffix(res.Plan.AcceptCLI, "--version 1.0.0 --from-draft draft-1") {
		t.Fatalf("%+v", res)
	}
	b, _ := w.reg.Pull(t.Context(), w.host+"/acme/offerings/xbucket:draft-1")
	rec, _ := contract.ReadInferredRecord(b.FS)
	if d := rec.Declaration; d.State != contract.DeclarationDraft || d.OnBehalfOf != "ana" || !strings.HasPrefix(d.InferredFrom, "offering:"+offeringProducer+"/xbucket@") {
		t.Fatalf("%s", rec.Marshal())
	}
}

func TestAcceptOfferingFromDraft(t *testing.T) {
	w := newOfferingWorld(t)
	w.withOwnerPolicy(t)
	from, _ := w.xbucket(t, 2)
	agent := w.offerReq(from)
	agent.Agent, agent.OnBehalfOf = true, "ana"
	w.declare(t, agent)
	req := w.offerReq(from)
	req.Owner, req.FromDraft = "", "draft-1"
	w.login(personToken("admin@acme.com"))
	res := w.declare(t, req)
	b, _ := w.reg.Pull(t.Context(), w.host+"/acme/offerings/xbucket:1.0.0")
	rec, _ := contract.ReadInferredRecord(b.FS)
	if res.Tag != "1.0.0" || rec.Declaration.Draft == nil || rec.Declaration.Draft.Tag != "draft-1" || b.Contract.Service.Owner.Team != "team-storage" {
		t.Fatalf("%+v\n%s", res, rec.Marshal())
	}
}

func TestAcceptOfferingFromDraftNotOffered(t *testing.T) {
	w := newOfferingWorld(t)
	from, _ := w.xbucket(t, 2)
	req := w.offerReq(from)
	req.FromDraft = "draft-1"
	if de := w.planRefusal(t, req); de.Code != codeDraftNotFound || !strings.HasPrefix(de.CLI, "pacto declare offering:xbucket --from") {
		t.Fatalf("%+v", de)
	}
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "dev"}, Fields: []contract.Inferred{},
		Declaration: &contract.InferredDeclaration{State: contract.DeclarationDraft, Asker: contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent},
			InferredFrom: "external:stripe"}}
	w.publishBundle(t, w.host+"/acme/offerings/xbucket", "draft-1", map[string]string{
		"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: xbucket\n  version: 0.1.0\n  external: stripe\n", contract.InferredRecordFile: string(rec.Marshal())}, nil)
	if de := w.planRefusal(t, req); de.Code != codeDraftNotFound || !strings.Contains(de.Message, "holds no draft accepting offering xbucket") {
		t.Fatalf("%+v", de)
	}
}

func TestAcceptOfferingRefusesUnverifiedProducer(t *testing.T) {
	w := newOfferingWorld(t)
	from, _ := w.xbucket(t, 2)
	req := w.offerReq(from)
	req.Detected = ""
	_, err := w.svc.Declare(t.Context(), req)
	de := refusal(t, err)
	if de.Code != codeOfferingSourceUnverified || !strings.Contains(de.CLI, "--detected "+detectedPlaceholder) ||
		!strings.Contains(de.Message, "neither a detection nor a contribution trust config was given") {
		t.Fatalf("%+v", de)
	}
	if tags, _ := w.reg.Tags(t.Context(), w.host+"/acme/offerings/xbucket"); len(tags) != 0 {
		t.Fatalf("wrote %v", tags)
	}
	// Signed by another key than the one the installation advertises.
	other, _ := w.contribute(t, w.repo, 3, contributionFiles(3, closedOffering), newKeypair(t))
	if de := w.planRefusal(t, w.offerReq(other)); de.Code != codeOfferingSourceUnverified || !strings.Contains(de.Message, "the installation key of cluster") {
		t.Fatalf("%+v", de)
	}
}

func TestPlanVerifiesProducerWithDetected(t *testing.T) {
	w := newOfferingWorld(t)
	// A contribution outside the repository the installation advertises.
	elsewhere := contract.ContributionRepository(w.host+"/acme", "idp-c2")
	from, _ := w.contribute(t, elsewhere, 2, contributionFiles(2, closedOffering), w.kp)
	req := w.offerReq(from)
	if de := w.planRefusal(t, req); de.Code != codeOfferingSourceUnverified || !strings.Contains(de.Message, "advertises no offerings repository "+elsewhere) {
		t.Fatalf("%+v", de)
	}
	from, _ = w.xbucket(t, 2)
	noKey := offeringDetection(t, w.host+"/acme", w.kp)
	noKey.Installation.SnapshotKey = ""
	for detected, why := range map[string]string{
		w.detection(t, noKey):                 "advertises no installation key",
		filepath.Join(t.TempDir(), "missing"): "was not read",
		"k8s:pacto-system/missing":            "was not read",
	} {
		req := w.offerReq(from)
		req.Detected = detected
		if de := w.planRefusal(t, req); de.Code != codeOfferingSourceUnverified || !strings.Contains(de.Message, why) {
			t.Errorf("%s: %+v", detected, de)
		}
	}
	req = w.offerReq(from)
	req.Detected, req.Dynamic = "k8s:pacto-system/detection", nil
	if de := w.planRefusal(t, req); de.Code != codeOfferingSourceUnverified || !strings.Contains(de.Message, "needs a Kubernetes client") {
		t.Fatalf("%+v", de)
	}
}

// grantFile writes a contribution trust config granting producer kp's key
// for its contribution path under root, and returns its path.
func grantFile(t *testing.T, root, producer string, kp *signing.Keypair) string {
	t.Helper()
	dir := t.TempDir()
	pub := base64.StdEncoding.EncodeToString(kp.GetPublicKey().(ed25519.PublicKey))
	cfg := "apiVersion: pacto.dev/evidence-trust/v1\nkeys:\n  - keyId: k1\n    producerId: " + producer + "\n    publicKeyFile: k1.pub\n" +
		"    allowedContractRepos: [\"" + root + "/pacto-inferred/" + producer + "\"]\n"
	for name, data := range map[string]string{"trust.yaml": cfg, "k1.pub": pub} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "trust.yaml")
}

// TestAcceptExternalProducerOffering: a hand-written offering contribution a
// third-party producer pushed, signed with its key and granted in the trust
// config, goes through the same acceptance.
func TestAcceptExternalProducerOffering(t *testing.T) {
	w := newOfferingWorld(t)
	kp, repo := newKeypair(t), contract.ContributionRepository(w.host+"/acme", "my-idp")
	from, digest := w.contribute(t, repo, 1, contributionFiles(1, closedOffering), kp)
	req := w.offerReq(from)
	req.Detected, req.ContributionTrust = "", grantFile(t, w.host+"/acme", "my-idp", kp)
	p, _ := w.plan(t, req)
	if p.InferredFrom != contract.OfferingInferredFrom("my-idp", "xbucket", digest) ||
		!slices.Contains(p.Notes, "accepted from "+repo+"@"+digest+", signed by trust-config key k1 of my-idp") {
		t.Fatalf("%+v", p)
	}
}

func TestPlanVerifiesProducerWithGrant(t *testing.T) {
	w := newOfferingWorld(t)
	kp, repo := newKeypair(t), contract.ContributionRepository(w.host+"/acme", "my-idp")
	from, _ := w.contribute(t, repo, 1, contributionFiles(1, closedOffering), kp)
	for trust, why := range map[string]string{
		grantFile(t, w.host+"/acme", "other-idp", kp): "grants my-idp no key for " + repo,
		filepath.Join(t.TempDir(), "missing.yaml"):    "no such file",
	} {
		req := w.offerReq(from)
		req.Detected, req.ContributionTrust = "", trust
		if de := w.planRefusal(t, req); de.Code != codeOfferingSourceUnverified || !strings.Contains(de.Message, why) {
			t.Errorf("%s: %+v", trust, de)
		}
	}
}

func TestAcceptOfferingCommandAndAcceptCLI(t *testing.T) {
	req := DeclareRequest{Service: "offering:xbucket", From: "oci://r/acme/pacto-inferred/p/services:xbucket_0.0.0-inferred.2.0",
		Detected: "k8s:pacto-system/pacto-operator-detected", Registry: "r/acme/offerings", Version: "1.0.0", Owner: "team-storage", Dir: "/tmp/x"}
	want := "pacto declare offering:xbucket --from " + req.From + " --detected k8s:pacto-system/pacto-operator-detected --dir /tmp/x " +
		"--registry r/acme/offerings --version 1.0.0 --owner team-storage --intent sha256:x"
	if got := req.command("sha256:x"); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	want = "pacto declare offering:xbucket --from " + req.From + " --detected k8s:pacto-system/pacto-operator-detected " +
		"--registry r/acme/offerings --version 1.0.0 --from-draft draft-2"
	if got := req.AcceptCLI("xbucket", "draft-2"); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if got := (DeclareRequest{Service: "offering:xbucket"}).command(""); got != "pacto declare offering:xbucket --from <contribution> --registry <registry> --version <version>" {
		t.Fatal(got)
	}
}
