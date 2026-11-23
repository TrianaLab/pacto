package cli_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/cli"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// contributionE2E is one producer, my-idp, on a real Evidence Server over a
// real in-memory registry that holds the declared ledger: everything the
// producer does goes through the pacto CLI.
type contributionE2E struct {
	host, evidenceURL, keys string
	store                   oci.BundleStore
	ledger                  string // oci://<host>/acme/ledger@sha256:…
}

const (
	ctbProducer = "my-idp"
	ctbKeyID    = "my-idp-2026"
	ctbSubject  = "ledger-prod"
)

func newContributionE2E(t *testing.T) *contributionE2E {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	t.Cleanup(srv.Close)
	e := &contributionE2E{host: strings.TrimPrefix(srv.URL, "http://"), keys: t.TempDir()}
	t.Setenv("PACTO_INSECURE_REGISTRIES", e.host)
	e.store = oci.NewClient(authn.NewMultiKeychain(), oci.WithInsecureRegistries(e.host))

	ledger := t.TempDir()
	mustWrite(t, filepath.Join(ledger, "pacto.yaml"), "pactoVersion: \"2.5\"\nservice:\n  name: ledger\n  version: 1.0.0\n  owner:\n    team: ledger\n")
	out := e.run(t, "push", "oci://"+e.host+"/acme/ledger", "-p", ledger, "--index", "none", "--output-format", "json")
	var pushed struct{ Ref, Digest string }
	if err := json.Unmarshal([]byte(out), &pushed); err != nil || pushed.Digest == "" {
		t.Fatalf("push ledger: %v\n%s", err, out)
	}
	e.ledger = "oci://" + e.host + "/acme/ledger@" + pushed.Digest

	// The producer's key lives outside every directory it pushes.
	e.run(t, "evidence", "keygen", "--out", e.keys, "--key-id", ctbKeyID, "--producer", ctbProducer)
	trust := filepath.Join(e.keys, "trust.yaml")
	mustWrite(t, trust, "apiVersion: pacto.dev/evidence-trust/v1\nkeys:\n"+
		"  - keyId: "+ctbKeyID+"\n    producerId: "+ctbProducer+"\n    publicKeyFile: "+ctbProducer+"__"+ctbKeyID+".pub\n"+
		"    allowedContractRepos: ["+e.host+"/acme/ledger, "+e.host+"/acme/"+contract.InferredRepository+"/"+ctbProducer+"]\n")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.evidenceURL = "http://" + ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.NewService(e.store, nil).ServeEvidenceOnListener(ctx, ln, app.ServeOptions{
			TrustPath: trust, Subjects: []string{e.ledger}, Producers: []string{ctbProducer}})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("evidence server: %v", err)
		}
	})
	return e
}

// run runs the pacto CLI and fails the test on an error.
func (e *contributionE2E) run(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, err := execFleetStore(t, e.store, args...)
	if err != nil {
		t.Fatalf("pacto %s: %v\n%s%s", strings.Join(args, " "), err, out, errOut)
	}
	return out
}

// contribute pushes and signs my-idp's record of checkout, which says it
// calls ledger.
func (e *contributionE2E) contribute(t *testing.T) {
	t.Helper()
	export := []byte(`{"checkout":["ledger"]}`)
	sum := sha256.Sum256(export)
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1", Sources: []contract.InferredSourceFile{{Path: "export.json", Digest: "sha256:" + hex.EncodeToString(sum[:])}}},
		Fields: []contract.Inferred{{Field: "dependencies[ledger]", Value: "ledger", Source: "export.json:1", Confidence: contract.ConfidenceLikely}}}
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "pacto.yaml"), "pactoVersion: \"2.5\"\nservice:\n  name: checkout\n  version: 0.1.0\n")
	mustWrite(t, filepath.Join(dir, "export.json"), string(export))
	mustWrite(t, filepath.Join(dir, contract.InferredRecordFile), string(rec.Marshal()))
	out := e.run(t, "push", "oci://"+e.host+"/acme/"+contract.InferredRepository+"/"+ctbProducer+"/services", "-p", dir,
		"--sign-key", filepath.Join(e.keys, ctbKeyID+".key"))
	if !strings.Contains(out, "checkout_0.1.0") {
		t.Fatalf("push checkout:\n%s", out)
	}
}

// report signs and sends my-idp's WorkloadObserved for ledger-prod.
func (e *contributionE2E) report(t *testing.T) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	subject := evidence.SubjectRef{Kind: "service", Name: ctbSubject}
	set := evidence.EvidenceSet{Subject: subject, ContractRef: e.ledger, Source: ctbProducer, ObservedAt: now,
		Observations: []evidence.Observation{evidence.NewWorkloadObserved(subject, "service", evidence.Provenance{Collector: ctbProducer, DetectedAt: now})}}
	data, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	setPath, envPath := filepath.Join(dir, "set.json"), filepath.Join(dir, "envelope.json")
	mustWrite(t, setPath, string(data))
	env := e.run(t, "evidence", "sign", "--key", filepath.Join(e.keys, ctbKeyID+".key"), "--key-id", ctbKeyID, "--producer", ctbProducer, "--sequence", "1", setPath)
	mustWrite(t, envPath, env)
	e.run(t, "evidence", "send", envPath, "--url", e.evidenceURL+"/api/evidence/v1/envelopes")
}

// fleet runs a fleet command over the Evidence Server and ledger only.
func (e *contributionE2E) fleet(t *testing.T, freshness string, args ...string) string {
	t.Helper()
	return e.run(t, append(append([]string{"fleet"}, args...),
		"--local", "", "--evidence-url", e.evidenceURL, "--oci", e.ledger, "--freshness", freshness)...)
}

// impactExit is pacto impact's exit code and output for ledger 1.0.0 to
// itself under extra, reading no target snapshot and no index.
func (e *contributionE2E) impactExit(t *testing.T, extra ...string) (int, string) {
	t.Helper()
	args := append([]string{"impact", e.ledger, e.ledger, "--local", "", "--oci", e.ledger, "--target-snapshot", "off", "--index", "none"}, extra...)
	out, errOut, err := execFleetStore(t, e.store, args...)
	var exit *cli.ExitError
	switch {
	case err == nil:
		return 0, out
	case errors.As(err, &exit):
		return exit.Code, out
	}
	t.Fatalf("pacto impact: %v\n%s%s", err, out, errOut)
	return -1, ""
}

// TestContributionEndToEnd: a producer with one key pushes a signed record
// and reports a target; the fleet shows the target reported by it with its
// evidence age, the contributed service as inferred and contributed by it,
// one available provider row, and impact lists the contribution as an
// inferred consumer without moving the verdict. Silent past the freshness
// window, the target, the row and the contribution read stale.
func TestContributionEndToEnd(t *testing.T) {
	e := newContributionE2E(t)
	alone, _ := e.impactExit(t)
	e.contribute(t)
	e.report(t)

	key := e.reportedTarget(t)
	if out := e.fleet(t, "24h", "get", "--target", key); !strings.Contains(out, "Reported by: "+ctbProducer+" (evidence ") || !strings.Contains(out, "Stale: false") {
		t.Errorf("target:\n%s", out)
	}
	if out := e.fleet(t, "24h", "get", "checkout"); !strings.Contains(out, "Contributed by "+ctbProducer+" from "+e.host+"/acme/pacto-inferred/my-idp/services@") ||
		strings.Contains(out, ", stale") || !strings.Contains(out, "never clears a gate") {
		t.Errorf("checkout:\n%s", out)
	}
	var svc struct {
		Service struct{ Declaration string } `json:"service"`
	}
	if err := json.Unmarshal([]byte(e.fleet(t, "24h", "get", "checkout", "--output-format", "json")), &svc); err != nil || svc.Service.Declaration != "inferred" {
		t.Errorf("checkout declaration = %q, %v", svc.Service.Declaration, err)
	}
	if row := e.producerRow(t, "24h"); row.Status != "available" || row.AsOf == nil {
		t.Errorf("my-idp row: %+v", row)
	}
	code, impact := e.impactExit(t, "--evidence-url", e.evidenceURL)
	if code != alone || !regexp.MustCompile(`checkout +direct +confidence=inferred +compat=unknown`).MatchString(impact) {
		t.Errorf("impact exit %d, alone %d:\n%s", code, alone, impact)
	}

	// The producer goes silent past the window.
	time.Sleep(2 * time.Second)
	if out := e.fleet(t, "1s", "get", "--target", key); !strings.Contains(out, "Stale: true") {
		t.Errorf("silent target:\n%s", out)
	}
	if row := e.producerRow(t, "1s"); row.Status != "stale" {
		t.Errorf("silent my-idp row: %+v", row)
	}
	if out := e.fleet(t, "1s", "get", "checkout"); !strings.Contains(out, ", stale") {
		t.Errorf("silent checkout:\n%s", out)
	}
}

// reportedTarget is the key of ledger's one target, which my-idp reported.
func (e *contributionE2E) reportedTarget(t *testing.T) string {
	t.Helper()
	var view struct {
		Targets []struct{ Key, ReportedBy string } `json:"targets"`
	}
	if err := json.Unmarshal([]byte(e.fleet(t, "24h", "get", "ledger", "--output-format", "json")), &view); err != nil || len(view.Targets) != 1 || view.Targets[0].ReportedBy != ctbProducer {
		t.Fatalf("ledger's targets = %+v, %v", view.Targets, err)
	}
	return view.Targets[0].Key
}

// producerRow is my-idp's provider row under the freshness window.
func (e *contributionE2E) producerRow(t *testing.T, freshness string) fleet.Provider {
	t.Helper()
	var res fleet.ProvidersResult
	if err := json.Unmarshal([]byte(e.fleet(t, freshness, "providers", "--output-format", "json")), &res); err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Providers {
		if p.ID == fleet.ProducerRowPrefix+ctbProducer {
			return p
		}
	}
	t.Fatalf("no %s row in %+v", ctbProducer, res.Providers)
	return fleet.Provider{}
}
