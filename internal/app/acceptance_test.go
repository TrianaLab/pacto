package app

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// store is the env's acceptance store under its signer policy.
func (e *acceptEnv) store(t *testing.T) *acceptanceStore {
	t.Helper()
	return newAcceptanceStore(t.Context(), e.svc.env(), e.trust().Trust)
}

// query reads reports 1.6.0's acceptances, owned by ana, stopping at any
// counted record.
func (e *acceptEnv) query() acceptanceQuery {
	return acceptanceQuery{Subject: e.subject, Repo: e.repo, Service: e.key("reports"), Version: "1.6.0",
		Owner: contract.Owner{DRI: "ana@acme.com"}, Wants: func(impact.Acceptance) bool { return true }}
}

func (e *acceptEnv) read(t *testing.T, st *acceptanceStore, q acceptanceQuery) ([]delivery.JudgedAcceptance, []string) {
	t.Helper()
	got, notes, err := st.readAcceptances(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	return got, notes
}

func states(js []delivery.JudgedAcceptance) []string {
	var out []string
	for _, j := range js {
		out = append(out, j.State)
	}
	return out
}

func hasNote(notes []string, s string) bool {
	return slices.ContainsFunc(notes, func(n string) bool { return strings.Contains(n, s) })
}

func TestReadAcceptancesBothForms(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, true)
	e.attach(t, attachment{record: e.rangesRecord(), token: personTok("bob@acme.com")})
	tagged := e.attach(t, attachment{record: e.rangesRecord(), token: personTok("ana@acme.com"), tag: true})
	q := e.query()
	q.Wants = func(impact.Acceptance) bool { return false }
	got, notes := e.read(t, e.store(t), q)
	slices.SortFunc(got, func(a, b delivery.JudgedAcceptance) int { return strings.Compare(a.State, b.State) })
	if !slices.Equal(states(got), []string{impact.AcceptCounted, impact.AcceptRouted}) || len(notes) != 0 {
		t.Fatalf("read %v %v", states(got), notes)
	}
	if c := got[0]; c.Ref != e.repo+"@"+tagged || c.Signer != "ana@acme.com" || c.Issuer != personIssuer || c.SignedAt.IsZero() || c.ID == "" {
		t.Fatalf("counted = %+v", c)
	}
	// Without the Referrers API the tags are the whole answer.
	e = newAcceptEnv(t, false)
	e.attach(t, attachment{record: e.rangesRecord(), token: personTok("ana@acme.com"), tag: true})
	if got, _ := e.read(t, e.store(t), e.query()); !slices.Equal(states(got), []string{impact.AcceptCounted}) {
		t.Fatalf("tags only: %v", states(got))
	}
}

func TestForgedUnsignedAcceptanceIgnored(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	e.attach(t, attachment{record: e.rangesRecord(), tag: true})
	got, notes := e.read(t, e.store(t), e.query())
	if len(got) != 0 || !hasNote(notes, "it does not verify") {
		t.Fatalf("read %v %v", states(got), notes)
	}
}

func TestAcceptanceForOtherSubjectIgnored(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, true)
	older := e.publishBundle(t, e.repo, "1.5.0", e.reportsFiles("1.5.0", false), nil)
	e.attach(t, attachment{record: e.rangesRecord(), token: personTok("ana@acme.com"), statement: older})
	r := e.rangesRecord()
	r.Version = "1.5.0"
	e.attach(t, attachment{record: r, token: personTok("ana@acme.com")})
	r.Version, r.Service = "1.6.0", "billing"
	e.attach(t, attachment{record: r, token: personTok("ana@acme.com")})
	got, notes := e.read(t, e.store(t), e.query())
	if len(got) != 0 || !hasNote(notes, "names another subject") || !hasNote(notes, "accepts for "+e.key("reports")+" 1.5.0") || !hasNote(notes, "accepts for billing 1.6.0") {
		t.Fatalf("read %v %v", states(got), notes)
	}
}

func TestAcceptanceTagCopiedToOtherSubjectIgnored(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	older := e.publishBundle(t, e.repo, "1.5.0", e.reportsFiles("1.5.0", false), nil)
	r := e.rangesRecord()
	r.Version = "1.5.0"
	e.attach(t, attachment{record: r, token: personTok("ana@acme.com"), tag: true, manifest: older, tagOf: e.digest})
	got, notes := e.read(t, e.store(t), e.query())
	if len(got) != 0 || !hasNote(notes, "attached to another subject") {
		t.Fatalf("read %v %v", states(got), notes)
	}
}

func TestAcceptanceTagSuffixMismatchIgnored(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	d := e.attach(t, attachment{record: e.rangesRecord(), token: personTok("ana@acme.com"), tag: true})
	_, hex, _ := strings.Cut(e.digest, ":")
	if err := e.reg.Tag(t.Context(), e.repo, d, delivery.AcceptanceTagPrefix+hex+"-000000000000"); err != nil {
		t.Fatal(err)
	}
	if err := e.reg.Tag(t.Context(), e.repo, d, delivery.AcceptanceTagPrefix+hex+"-short"); err != nil {
		t.Fatal(err)
	}
	q := e.query()
	q.Wants = func(impact.Acceptance) bool { return false }
	got, notes := e.read(t, e.store(t), q)
	if !slices.Equal(states(got), []string{impact.AcceptCounted}) || !hasNote(notes, "-000000000000: the tag does not name the manifest") || !hasNote(notes, "-short: the tag does not name") {
		t.Fatalf("read %v %v", states(got), notes)
	}
}

func TestAcceptanceOnForeignHostNeverRead(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, true)
	foreign := provenanceTestRegistryOf(t, true)
	q := e.query()
	q.Repo = foreign.host + "/acme/reports"
	if _, _, err := e.store(t).readAcceptances(t.Context(), q); err == nil || !strings.Contains(err.Error(), "read only on "+e.host) {
		t.Fatalf("err = %v", err)
	}
	if n := foreign.requests.Load(); n != 0 {
		t.Fatalf("the foreign host served %d requests", n)
	}
}

func TestOversizedAcceptanceNeverDownloaded(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, true)
	big := e.attach(t, attachment{record: e.rangesRecord(), token: personTok("ana@acme.com"), pad: delivery.MaxAcceptanceBytes})
	e.reset()
	got, notes := e.read(t, e.store(t), e.query())
	if len(got) != 0 || !hasNote(notes, "its record is larger than") {
		t.Fatalf("read %v %v", states(got), notes)
	}
	if n := e.requests("/blobs/"); n != 0 {
		t.Fatalf("%d blob requests read the oversized record (manifest %s)", n, big)
	}
	// A manifest over 4 KiB is never fetched either.
	e = newAcceptEnv(t, true)
	r := e.rangesRecord()
	r.Ranges[0].Source = strings.Repeat("x", 5000)
	e.attach(t, attachment{record: r, token: personTok("ana@acme.com")})
	if got, notes := e.read(t, e.store(t), e.query()); len(got) != 1 || len(notes) != 0 {
		t.Fatalf("a large record in a small manifest: %v %v", states(got), notes)
	}
}

func TestOversizedAcceptanceManifestNeverFetched(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	st := e.store(t)
	st.listings[e.repo+"@"+e.digest] = []acceptanceCandidate{{name: "big", desc: ocispec.Descriptor{Digest: "sha256:00", Size: maxAcceptanceManifest + 1}}}
	e.reset()
	got, notes := e.read(t, st, e.query())
	if len(got) != 0 || !hasNote(notes, "its manifest is larger than") || e.requests("/manifests/") != 0 {
		t.Fatalf("read %v %v", states(got), notes)
	}
}

func TestReadAcceptancesBounded(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	for i := range delivery.MaxAcceptances + 1 {
		r := e.rangesRecord()
		r.Ranges[0].Value = strings.Repeat("^", i+1)
		e.attach(t, attachment{record: r, tag: true, artifactType: "application/vnd.example.other"})
	}
	got, notes := e.read(t, e.store(t), e.query())
	if len(got) != 0 || !hasNote(notes, AcceptancesTruncated) || !hasNote(notes, "it is no acceptance record") {
		t.Fatalf("read %v %d notes", states(got), len(notes))
	}
}

func TestAcceptanceFloodDoesNotHideCountedRecord(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, true)
	for i := range delivery.MaxAcceptances + 1 {
		r := e.rangesRecord()
		r.Ranges[0].Value = strings.Repeat("^", i+1)
		e.attach(t, attachment{record: r, tag: true, detached: true})
	}
	e.attach(t, attachment{record: e.rangesRecord(), token: personTok("ana@acme.com")})
	got, notes := e.read(t, e.store(t), e.query())
	if !slices.Equal(states(got), []string{impact.AcceptCounted}) || hasNote(notes, AcceptancesTruncated) {
		t.Fatalf("read %v %v", states(got), notes)
	}
}

func TestAcceptanceListingMemoized(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	e.attach(t, attachment{record: e.rangesRecord(), token: personTok("ana@acme.com"), tag: true})
	st := e.store(t)
	e.read(t, st, e.query())
	first := e.requests("/tags/list")
	e.read(t, st, e.query())
	if first == 0 || e.requests("/tags/list") != first {
		t.Fatalf("tag listings: %d, then %d", first, e.requests("/tags/list"))
	}
}

func TestCIClaimCertificateFromUnlistedIssuerIsProposal(t *testing.T) {
	e := newAcceptEnv(t, false)
	swapPublicGood(t, e.ss.Root, nil)
	e.attach(t, attachment{record: e.rangesRecord(), tag: true,
		token: testutil.CIToken("https://gitlab.example.com/acme/reports//.gitlab-ci.yml@refs/heads/main", "https://gitlab.example.com", "https://gitlab.example.com/acme/reports", "refs/heads/main")})
	q := e.query()
	q.Owner = contract.Owner{DRI: "https://gitlab.example.com/acme/reports//.gitlab-ci.yml@refs/heads/main"}
	got, _ := e.read(t, newAcceptanceStore(t.Context(), e.svc.env(), nil), q)
	if !slices.Equal(states(got), []string{impact.AcceptProposal}) || !strings.Contains(got[0].Reason, "a ci signer") {
		t.Fatalf("read %+v", got)
	}
}

// swapPublicGood makes public-good Sigstore's root tm, or err.
func swapPublicGood(t *testing.T, tm root.TrustedMaterial, err error) {
	t.Helper()
	old := publicGoodRoot
	t.Cleanup(func() { publicGoodRoot = old })
	publicGoodRoot = func(string) (root.TrustedMaterial, error) { return tm, err }
}

func TestNoSignerPolicyNote(t *testing.T) {
	e := newAcceptEnv(t, false)
	swapPublicGood(t, e.ss.Root, nil)
	e.attach(t, attachment{record: e.rangesRecord(), token: personTok("ana@acme.com"), tag: true})
	for name, trust := range map[string]func(context.Context) (Installation, error){
		"no reader":  nil,
		"unreadable": func(context.Context) (Installation, error) { return Installation{}, errors.New("no cluster") },
		"no signers": func(context.Context) (Installation, error) { return Installation{}, nil },
	} {
		got, notes := e.read(t, newAcceptanceStore(t.Context(), e.svc.env(), trust), e.query())
		if !slices.Equal(states(got), []string{impact.AcceptCounted}) || !hasNote(notes, publicGoodNote) {
			t.Errorf("%s: %v %v", name, states(got), notes)
		}
	}
	swapPublicGood(t, nil, errors.New("tuf down"))
	got, notes := e.read(t, newAcceptanceStore(t.Context(), e.svc.env(), nil), e.query())
	if len(got) != 0 || !hasNote(notes, "tuf down") {
		t.Fatalf("an unreadable public-good root: %v %v", states(got), notes)
	}
}

func TestAcceptanceSignedOutsidePolicyIgnored(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	e.attach(t, attachment{record: e.rangesRecord(), token: testutil.Token("ana@acme.com", "https://other.example"), tag: true})
	got, notes := e.read(t, e.store(t), e.query())
	if len(got) != 0 || !hasNote(notes, "not signed by a certificate from "+personIssuer) {
		t.Fatalf("read %v %v", states(got), notes)
	}
}

func TestReadAcceptancesRegistryFailures(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	e.attach(t, attachment{record: e.rangesRecord(), token: personTok("ana@acme.com"), tag: true})
	for name, fail := range map[string]func(*http.Request) bool{
		"subject": func(r *http.Request) bool { return strings.HasSuffix(r.URL.Path, "/manifests/"+e.digest) },
		"listing": func(r *http.Request) bool { return strings.HasSuffix(r.URL.Path, "/tags/list") },
		"tag": func(r *http.Request) bool {
			return strings.Contains(r.URL.Path, "/manifests/"+delivery.AcceptanceTagPrefix)
		},
		"manifest": func(r *http.Request) bool {
			return r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/manifests/sha256:")
		},
		"layer": func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/blobs/") },
	} {
		e.failWhen(fail)
		if _, _, err := e.store(t).readAcceptances(t.Context(), e.query()); err == nil {
			t.Errorf("%s: a failed read answered", name)
		}
	}
	e.failWhen(nil)
	q := e.query()
	q.Repo = e.host + "/UPPER"
	q.Subject = q.Repo + "@" + e.digest
	if _, _, err := e.store(t).readAcceptances(t.Context(), q); err == nil {
		t.Error("an invalid repository opened")
	}
}

func TestAcceptanceTagGoneIgnored(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	_, hex, _ := strings.Cut(e.digest, ":")
	st := e.store(t)
	tag := delivery.AcceptanceTagPrefix + hex + "-000000000000"
	st.listings[e.repo+"@"+e.digest] = []acceptanceCandidate{{name: tag, tag: tag}}
	got, notes := e.read(t, st, e.query())
	if len(got) != 0 || !hasNote(notes, "listed and is gone") {
		t.Fatalf("read %v %v", states(got), notes)
	}
}

// headDown cannot say what any tag holds.
type headDown struct{ publish.Registry }

func (headDown) Head(context.Context, string) (*publish.Remote, error) {
	return nil, errors.New("down")
}

// A provider repository that cannot be read fails the break read, never
// reads as the version not being published.
func TestBreakSourceRegistryDown(t *testing.T) {
	t.Parallel()
	b, ref, err := breakSource(t.Context(), headDown{}, "r.io/acme/ledger", "ledger", "3.0.0", "")
	if b != nil || ref != "" || err == nil || err.Error() != "reading ledger 3.0.0: down" {
		t.Fatalf("%v %q %v", b, ref, err)
	}
}
