package evidenceoci

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// failing serves inner, except that requests matching fail get status.
type failing struct {
	inner  http.Handler
	fail   func(*http.Request) bool
	status int
}

func (f failing) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.fail(r) {
		http.Error(w, `{"errors":[{"code":"UNKNOWN"}]}`, f.status)
		return
	}
	f.inner.ServeHTTP(w, r)
}

// ledgerPath matches requests on any ledger repository.
func ledgerPath(r *http.Request) bool { return strings.Contains(r.URL.Path, "/pacto-evidence/") }

// prefixFor is the prefix acme/ on host.
func prefixFor(t *testing.T, host string) Prefix {
	t.Helper()
	l, err := ParsePrefixes([]string{Scheme + host + "/acme/"})
	if err != nil {
		t.Fatal(err)
	}
	return l[0]
}

// prefixStore is a store with only the acme/ prefix on host.
func prefixStore(t *testing.T, host string, subjects ...Subject) *Store {
	t.Helper()
	s, err := NewStore(SubjectList(subjects), PrefixList{prefixFor(t, host)}, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// plantAnchor writes art under tag in host's acme ledger with no scope, as
// anyone with push on the ledger could.
func plantAnchor(t *testing.T, host string, art ocireferrer.Artifact, tag string) {
	t.Helper()
	repo, err := ocireferrer.Open(host+"/acme/pacto-evidence", RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ocireferrer.Push(t.Context(), repo, art); err != nil {
		t.Fatal(err)
	}
	if err := repo.Tag(t.Context(), art.ManifestDesc, tag); err != nil {
		t.Fatal(err)
	}
}

func TestLedgerAnchorRoundTrip(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	s := prefixStore(t, host)
	p := prefixFor(t, host)
	for range 2 { // the second write is an idempotent no-op
		if err := s.writeAnchor(t.Context(), p, subj); err != nil {
			t.Fatalf("writeAnchor: %v", err)
		}
	}
	read, err := s.ledgerSubjects(t.Context(), p)
	got, invalid := read.subjects, read.invalid
	if err != nil || invalid != 0 || len(got) != 1 || got[0] != subj {
		t.Fatalf("ledgerSubjects = %v, %d, %v, want [%s]", got, invalid, err, subj.Ref())
	}
}

func TestLedgerAnchorRejects(t *testing.T) {
	p := Prefix{Registry: "reg", Path: "acme"}
	inside := "oci://reg/acme/invoices@" + digestA
	for name, tc := range map[string]struct{ tag, payload string }{
		"bad json":       {anchorTag(inside), `{"subject":`},
		"unknown field":  {anchorTag(inside), `{"subject":"` + inside + `","x":1}`},
		"not a subject":  {anchorTag("oci://reg/acme/invoices:1"), `{"subject":"oci://reg/acme/invoices:1"}`},
		"outside":        {anchorTag("oci://reg/other/x@" + digestA), `{"subject":"oci://reg/other/x@` + digestA + `"}`},
		"pacto-inferred": {anchorTag("oci://reg/acme/pacto-inferred/x@" + digestA), `{"subject":"oci://reg/acme/pacto-inferred/x@` + digestA + `"}`},
		"hash mismatch":  {anchorTag("oci://reg/acme/other@" + digestA), `{"subject":"` + inside + `"}`},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validAnchor(tc.tag, []byte(tc.payload), p); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("validAnchor = %v, want ErrInvalidArtifact", err)
			}
		})
	}
	if s, err := validAnchor(anchorTag(inside), []byte(`{"subject":"`+inside+`"}`), p); err != nil || s.Ref() != inside {
		t.Fatalf("validAnchor(valid) = %v, %v", s, err)
	}
}

// What the registry serves under an anchor tag is checked before it is read:
// a wrong artifact or layer type or an oversized layer is an invalid anchor,
// counted and never a subject.
func TestLedgerAnchorRejectsWhatTheRegistryServes(t *testing.T) {
	big := make([]byte, maxAnchorBytes+1)
	for name, build := range map[string]func(payload []byte) ocireferrer.Artifact{
		"wrong artifact type": func(p []byte) ocireferrer.Artifact {
			return ocireferrer.Build("application/x-other", LedgerArtifactType, p, nil, nil)
		},
		"wrong layer type": func(p []byte) ocireferrer.Artifact {
			return ocireferrer.Build(LedgerArtifactType, "application/x-other", p, nil, nil)
		},
		"oversized layer": func([]byte) ocireferrer.Artifact {
			return ocireferrer.Build(LedgerArtifactType, LedgerArtifactType, big, nil, nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			host := startRegistry(t, testutil.ReferrersOptions{})
			subj, _ := ParseSubject(Scheme + host + "/acme/invoices@" + digestA)
			payload, _ := json.Marshal(anchorDoc{Subject: subj.Ref()})
			plantAnchor(t, host, build(payload), anchorTag(subj.Ref()))
			read, err := prefixStore(t, host).ledgerSubjects(t.Context(), prefixFor(t, host))
			got, invalid := read.subjects, read.invalid
			if err != nil || invalid != 1 || len(got) != 0 {
				t.Fatalf("ledgerSubjects = %v, %d, %v, want one invalid anchor", got, invalid, err)
			}
		})
	}
}

// A ledger tag whose manifest or layer the registry cannot serve is an
// invalid anchor, and a tag of another length (a record) is no anchor.
func TestLedgerAnchorUnreadable(t *testing.T) {
	var breakLayer atomic.Bool
	inner := testutil.NewReferrersRegistry(testutil.ReferrersOptions{})
	host := serve(t, failing{inner: inner, status: http.StatusForbidden, fail: func(r *http.Request) bool {
		return breakLayer.Load() && ledgerPath(r) && strings.Contains(r.URL.Path, "/blobs/")
	}})
	subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	plantAnchor(t, host, buildAnchor(subj), anchorTag(subj.Ref()))
	plantAnchor(t, host, buildAnchor(subj), anchorTag(subj.Ref())+"-0123456789ab")
	breakLayer.Store(true)
	read, err := prefixStore(t, host).ledgerSubjects(t.Context(), prefixFor(t, host))
	got, invalid := read.subjects, read.invalid
	if err != nil || invalid != 1 || len(got) != 0 {
		t.Fatalf("ledgerSubjects = %v, %d, %v, want one invalid anchor and the record tag skipped", got, invalid, err)
	}
	if _, err := fetchAnchor(t.Context(), prefixStore(t, host).ledgers[prefixFor(t, host)], "evidence-missing"); err == nil {
		t.Fatal("fetchAnchor of a missing tag succeeded")
	}
}

func TestLedgerMissingRepositoryIsEmpty(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	read, err := prefixStore(t, host).ledgerSubjects(t.Context(), prefixFor(t, host))
	got, invalid := read.subjects, read.invalid
	if err != nil || invalid != 0 || len(got) != 0 {
		t.Fatalf("ledgerSubjects of a missing ledger = %v, %d, %v, want empty", got, invalid, err)
	}
}

func TestLedgerUnreadableListingFails(t *testing.T) {
	host := serve(t, failing{inner: testutil.NewReferrersRegistry(testutil.ReferrersOptions{}), status: http.StatusForbidden, fail: ledgerPath})
	if _, err := prefixStore(t, host).ledgerSubjects(t.Context(), prefixFor(t, host)); err == nil {
		t.Fatal("ledgerSubjects over a failing listing succeeded")
	}
}

func TestLedgerAnchorMemoized(t *testing.T) {
	var fetches atomic.Int32
	inner := testutil.NewReferrersRegistry(testutil.ReferrersOptions{})
	host := serve(t, failing{inner: inner, fail: func(r *http.Request) bool {
		if r.Method == http.MethodGet && ledgerPath(r) && strings.Contains(r.URL.Path, "/manifests/") {
			fetches.Add(1)
		}
		return false
	}})
	subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	s := prefixStore(t, host)
	p := prefixFor(t, host)
	if err := s.writeAnchor(t.Context(), p, subj); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if read, err := s.ledgerSubjects(t.Context(), p); err != nil || len(read.subjects) != 1 {
			t.Fatalf("ledgerSubjects = %v, %v", read.subjects, err)
		}
	}
	if n := fetches.Load(); n != 1 {
		t.Errorf("anchor manifest fetched %d times, want once", n)
	}
}

// reversedTags serves inner but lists ledger tags in reverse order, as CNCF
// distribution lists an anchor's record tags before the anchor itself.
type reversedTags struct{ inner http.Handler }

func (h reversedTags) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !ledgerPath(r) || !strings.HasSuffix(r.URL.Path, "/tags/list") {
		h.inner.ServeHTTP(w, r)
		return
	}
	rec := httptest.NewRecorder()
	h.inner.ServeHTTP(rec, r)
	var body struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	slices.Reverse(body.Tags)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rec.Code)
	_ = json.NewEncoder(w).Encode(body)
}

// A record tag listed before its anchor still groups under it: the ledger read
// never depends on the registry's tag order.
func TestLedgerRecordListedBeforeAnchor(t *testing.T) {
	host := serve(t, reversedTags{inner: testutil.NewReferrersRegistry(testutil.ReferrersOptions{})})
	subj, _ := seedContractIn(t, host, "acme/invoices", "invoices", RepositoryOptions{})
	s := prefixStore(t, host)
	p := prefixFor(t, host)
	if err := s.writeAnchor(t.Context(), p, subj); err != nil {
		t.Fatal(err)
	}
	art := ocireferrer.Build(ArtifactType, ArtifactType, []byte(`{}`), nil, nil)
	if err := s.writeRecord(t.Context(), p, subj, art); err != nil {
		t.Fatal(err)
	}
	read, err := s.ledgerSubjects(t.Context(), p)
	if err != nil || read.invalid != 0 || len(read.subjects) != 1 {
		t.Fatalf("ledgerSubjects = %v, %d, %v, want one subject", read.subjects, read.invalid, err)
	}
	want := recordTag(subj.Ref(), art.ManifestDesc.Digest)
	if got := read.records[anchorTag(subj.Ref())]; len(got) != 1 || got[0] != want {
		t.Errorf("records = %v, want [%s]", got, want)
	}
}

func TestEvidenceTagsNeverUseTheReferrersTagScheme(t *testing.T) {
	tag := anchorTag("oci://reg/acme/invoices@" + digestA)
	if !strings.HasPrefix(tag, "evidence-") || strings.HasPrefix(tag, "sha256-") || len(tag) > 128 || len(tag) != anchorTagLen {
		t.Fatalf("anchorTag = %q (%d characters)", tag, len(tag))
	}
	rec := recordTag("oci://reg/acme/invoices@"+digestA, digestA)
	if !strings.HasPrefix(rec, tag+"-") || strings.HasPrefix(rec, "sha256-") || len(rec) > 128 || len(rec) != recordTagLen {
		t.Fatalf("recordTag = %q (%d characters)", rec, len(rec))
	}
	if anchorTag("oci://reg/acme/invoices@"+digestA) == anchorTag("oci://reg/acme/billing@"+digestA) {
		t.Error("one digest in two repositories must be two anchors")
	}
}

// A manifest that is not strict JSON, or an anchor whose payload names a
// subject the tag does not hash to, is an invalid anchor.
func TestLedgerAnchorRejectsMalformedContent(t *testing.T) {
	host := startRegistry(t, testutil.ReferrersOptions{})
	subj, _ := ParseSubject(Scheme + host + "/acme/invoices@" + digestA)
	other, _ := ParseSubject(Scheme + host + "/acme/billing@" + digestA)
	plantAnchor(t, host, buildAnchor(other), anchorTag(subj.Ref()))

	art := buildAnchor(other)
	var m map[string]any
	_ = json.Unmarshal(art.Manifest, &m)
	m["x"] = 1
	raw, _ := json.Marshal(m)
	repo, _ := ocireferrer.Open(host+"/acme/pacto-evidence", RepositoryOptions{})
	if err := repo.PushReference(t.Context(), ocireferrer.Descriptor(ocispec.MediaTypeImageManifest, raw), bytes.NewReader(raw), anchorTag(other.Ref())); err != nil {
		t.Fatal(err)
	}
	read, err := prefixStore(t, host).ledgerSubjects(t.Context(), prefixFor(t, host))
	got, invalid := read.subjects, read.invalid
	if err != nil || invalid != 2 || len(got) != 0 {
		t.Fatalf("ledgerSubjects = %v, %d, %v, want two invalid anchors", got, invalid, err)
	}
}

func TestPrefixListLedgers(t *testing.T) {
	l := PrefixList{{Registry: "ghcr.io"}, {Registry: "reg", Path: "acme"}}
	if got := l.Ledgers(); len(got) != 2 || got[0] != "ghcr.io/pacto-evidence" || got[1] != "reg/acme/pacto-evidence" {
		t.Fatalf("Ledgers() = %v", got)
	}
}
