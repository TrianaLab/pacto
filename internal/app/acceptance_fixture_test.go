package app

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// acceptNow is when the acceptance tests read.
var acceptNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

const (
	snapSrc  = "snapshot:ghcr.io/acme/pacto-target-snapshots@sha256:0000000000000000000000000000000000000000000000000000000000000000"
	indexSrc = "index:ghcr.io/acme/pacto-index:0123456789abcdef-2.4.0"
)

// acceptEnv is a registry holding reports 1.6.0, with proposals in its
// record, and the test Sigstore people sign acceptances with.
type acceptEnv struct {
	*declareEnv
	prov    *provRegistry
	repo    string
	digest  string
	subject string
	mu      sync.Mutex
	paths   []string
	fail    func(*http.Request) bool
}

func newAcceptEnv(t *testing.T, referrers bool) *acceptEnv {
	t.Helper()
	p := provenanceTestRegistryOf(t, referrers)
	e := &acceptEnv{prov: p}
	p.failWhen(func(r *http.Request) bool {
		e.mu.Lock()
		e.paths = append(e.paths, r.Method+" "+r.URL.Path)
		fail := e.fail
		e.mu.Unlock()
		return fail != nil && fail(r)
	})
	e.declareEnv = &declareEnv{host: p.host, reg: p.client, ss: testutil.New(t),
		svc: &Service{sys: sysDeps{publishRegistry: func() publish.Registry { return p.client }, now: func() time.Time { return acceptNow },
			refusalRepo: func(path string) (*remote.Repository, error) {
				return ocireferrer.Open(path, ocireferrer.Options{Keychain: anonymous})
			}}}}
	e.root = filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(e.root, e.ss.RootJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	e.repo = p.host + "/acme/reports"
	e.digest = e.publishBundle(t, e.repo, "1.6.0", e.reportsFiles("1.6.0", true), nil)
	e.subject = e.repo + "@" + e.digest
	return e
}

// key is the fleet key of name in the env's registry domain.
func (e *acceptEnv) key(name string) string {
	return string(fleet.NewServiceKeyDomain(e.host+"/acme", name))
}

// signing is how the test person signs: the test Sigstore.
func (e *acceptEnv) signing() declareSigning {
	return declareSigning{TrustedRoot: e.root, FulcioURL: e.ss.Fulcio, TimestampURL: e.ss.TSA}
}

// failWhen fails every request f matches; nil fails none.
func (e *acceptEnv) failWhen(f func(*http.Request) bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fail = f
}

// reset forgets the requests served so far.
func (e *acceptEnv) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.paths = nil
}

// requests is how many requests whose "<METHOD> <path>" contains s the
// registry served.
func (e *acceptEnv) requests(s string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, p := range e.paths {
		if strings.Contains(p, s) {
			n++
		}
	}
	return n
}

// reportsFiles is reports at version, owned by ana, depending on ledger by
// name and on billing by ref; with proposals its record proposes both
// ranges and ledger's ref.
func (e *acceptEnv) reportsFiles(version string, proposals bool) map[string]string {
	files := map[string]string{"pacto.yaml": `pactoVersion: "2.5"
service:
  name: reports
  version: ` + version + `
  owner: {team: reporting, dri: ana@acme.com}
dependencies:
  - name: ledger
    required: true
  - name: billing
    ref: oci://` + e.host + `/acme/billing
    compatibility: unknown
    required: true
`}
	if proposals {
		files[contract.InferredRecordFile] = string(acceptRecordOf(
			contract.Inferred{Field: "dependencies[ledger].ref", Value: "oci://" + e.host + "/acme/ledger", Source: indexSrc, Confidence: contract.ConfidenceLikely},
			contract.Inferred{Field: "dependencies[ledger].compatibility", Value: "compatibility unknown", Proposed: "^2.3.0", Source: snapSrc, Confidence: contract.ConfidenceLikely},
			contract.Inferred{Field: "dependencies[billing].compatibility", Value: "compatibility unknown", Proposed: "^1.4.0", Source: snapSrc, Confidence: contract.ConfidenceLikely},
		).Marshal())
	}
	return files
}

func acceptRecordOf(fields ...contract.Inferred) *contract.InferredRecord {
	return &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Commit: "4e1a9c0d", Pacto: "4.6.0", Discovery: "1"}, Fields: fields}
}

// ledgerFiles is ledger at version announcing a break for consumers.
func ledgerFiles(version, expires string, consumers ...string) map[string]string {
	y := `pactoVersion: "2.5"
service: {name: ledger, version: ` + version + `}
breaks:
  - consumers: [` + strings.Join(consumers, ", ") + `]
    reason: the v1 API is removed
`
	if expires != "" {
		y += "    expires: \"" + expires + "\"\n"
	}
	return map[string]string{"pacto.yaml": y}
}

// writeDir writes files into a new folder and returns it.
func writeDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, c := range files {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// trust is an installation whose signer policy lets people of personIssuer
// sign, under the test Sigstore.
func (e *acceptEnv) trust() AcceptOptions {
	return AcceptOptions{Ref: e.repo, Trust: func(context.Context) (Installation, error) {
		return Installation{Signers: []delivery.PersonSigner{{Issuer: personIssuer, Subjects: ".*", TrustedRoot: string(e.ss.RootJSON)}}}, nil
	}}
}

// rangesRecord accepts ledger's proposed range for reports 1.6.0.
func (e *acceptEnv) rangesRecord() delivery.AcceptanceRecord {
	return delivery.AcceptanceRecord{Service: e.key("reports"), Version: "1.6.0", Kind: delivery.AcceptKindRanges,
		Ranges: []delivery.AcceptedRange{{Field: "dependencies[ledger].compatibility", Value: "^2.3.0", Proposed: "^2.3.0", Source: snapSrc}}}
}

// attachment is one acceptance a test attaches: the record, the token of
// who signs it ("" forges an unsigned bundle), whether it is a write-once
// tag, and the digests its manifest, statement and tag name (default the
// env's subject).
type attachment struct {
	record                     delivery.AcceptanceRecord
	token                      string
	tag                        bool
	manifest, statement, tagOf string
	artifactType               string
	pad                        int
	// detached builds a manifest with no subject: a tag only, never a referrer.
	detached bool
}

// attach pushes a and returns the manifest digest.
func (e *acceptEnv) attach(t *testing.T, a attachment) string {
	t.Helper()
	ctx := t.Context()
	manifest := cmp.Or(a.manifest, e.digest)
	_, hex, _ := strings.Cut(cmp.Or(a.statement, manifest), ":")
	st := delivery.AcceptanceStatement{Type: delivery.StatementType, PredicateType: delivery.PredicateAcceptance, Predicate: a.record,
		Subject: []delivery.Subject{{Name: e.repo, Digest: map[string]string{"sha256": hex}}}}
	payload, _ := json.Marshal(st)
	bundle := forgedBundle(payload)
	if a.token != "" {
		s := e.signer(t, a.token)
		var err error
		if bundle, err = s.Sign(ctx, delivery.PayloadType, payload); err != nil {
			t.Fatal(err)
		}
	}
	bundle = append(bundle, []byte(strings.Repeat(" ", a.pad))...)
	repo, err := ocireferrer.Open(e.repo, ocireferrer.Options{Keychain: anonymous})
	if err != nil {
		t.Fatal(err)
	}
	subject, err := repo.Resolve(ctx, manifest)
	if err != nil {
		t.Fatal(err)
	}
	subj := &subject
	if a.detached {
		subj = nil
	}
	art := ocireferrer.Build(cmp.Or(a.artifactType, delivery.AcceptanceArtifactType), delivery.AcceptanceArtifactType, bundle, subj, nil)
	if a.tag {
		_, tagHex, _ := strings.Cut(cmp.Or(a.tagOf, manifest), ":")
		err = ocireferrer.PushWriteOnce(ctx, repo, art, delivery.AcceptanceTagPrefix+tagHex+"-"+art.ManifestDesc.Digest.Encoded()[:12])
	} else {
		err = ocireferrer.Push(ctx, repo, art)
	}
	if err != nil {
		t.Fatal(err)
	}
	return art.ManifestDesc.Digest.String()
}

// forgedBundle is a Sigstore bundle carrying payload under a signature
// nobody made.
func forgedBundle(payload []byte) []byte {
	b, _ := json.Marshal(map[string]any{"mediaType": delivery.BundleMediaType, "dsseEnvelope": map[string]any{
		"payload": base64.StdEncoding.EncodeToString(payload), "payloadType": delivery.PayloadType,
		"signatures": []map[string]string{{"sig": "AAAA"}}}})
	return b
}

// personTok is a person's Sigstore login token.
func personTok(email string) string { return testutil.Token(email, personIssuer) }
