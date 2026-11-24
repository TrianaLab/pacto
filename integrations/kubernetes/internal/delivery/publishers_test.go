/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/sigstore/sigstore-go/pkg/root"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	orasregistry "oras.land/oras-go/v2/registry"
	orasremote "oras.land/oras-go/v2/registry/remote"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	ghIssuer    = "https://token.actions.githubusercontent.com"
	acmeRepo    = "https://github.com/acme/payments"
	acmeSubject = acmeRepo + "/.github/workflows/release.yml@refs/heads/main"
	otherRepo   = "https://github.com/contractor/payments"
	otherSub    = otherRepo + "/.github/workflows/ci.yml@refs/heads/main"
)

// flakyRegistry is a publish.Registry whose tag listing or signature reads
// can fail, and which counts the signature reads.
type flakyRegistry struct {
	publish.Registry
	tagsErr, referrersErr error
	// headErr fails reading a policy reference, layerErr reading a layer.
	headErr, layerErr error
	referrers         atomic.Int32
}

func (f *flakyRegistry) Head(ctx context.Context, ref string) (*publish.Remote, error) {
	if f.headErr != nil && strings.Contains(ref, "/policy") {
		return nil, f.headErr
	}
	return f.Registry.Head(ctx, ref)
}

func (f *flakyRegistry) Layer(ctx context.Context, repo, digest string) ([]byte, map[string]string, error) {
	if f.layerErr != nil {
		return nil, nil, f.layerErr
	}
	return f.Registry.Layer(ctx, repo, digest)
}

func (f *flakyRegistry) Tags(ctx context.Context, repo string) ([]string, error) {
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	return f.Registry.Tags(ctx, repo)
}

func (f *flakyRegistry) Referrers(ctx context.Context, repo, digest, artifactType string) ([]string, error) {
	f.referrers.Add(1)
	if f.referrersErr != nil {
		return nil, f.referrersErr
	}
	return f.Registry.Referrers(ctx, repo, digest, artifactType)
}

// watchWorld is an in-process registry, a private Sigstore and a promoter
// whose registry watch judges against the rule derived from ghcr.io/acme.
type watchWorld struct {
	t    *testing.T
	host string
	reg  *flakyRegistry
	ss   *sigtest.Sigstore
	p    *Publishers
	e    *env
}

func newWatch(t *testing.T, cands delivery.Candidates, objs ...client.Object) *watchWorld {
	t.Helper()
	// The process probe cache is keyed by host:port and httptest reuses
	// ports: a server that held this port earlier must not decide how this
	// one, which serves no Referrers API, attaches referrers.
	process := ocireferrer.Probes
	ocireferrer.Probes = &ocireferrer.Prober{}
	t.Cleanup(func() { ocireferrer.Probes = process })
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	w := &watchWorld{t: t, host: strings.TrimPrefix(srv.URL, "http://"), ss: sigtest.New(t)}
	w.reg = &flakyRegistry{Registry: publish.NewClient(authn.DefaultKeychain, w.host)}
	if cands.Registry != nil && cands.Registry.Repository == "-" {
		cands.Registry.Repository = w.host + "/payments"
	}
	s := stageObj("")
	s.Spec.Candidates, s.Status.Published = cands, nil
	w.e = newEnv(t, append(objs, s, ociRepo("payments", "", "", false))...)
	rule, _ := delivery.DeriveRule("ghcr.io/acme", "main")
	w.p = &Publishers{Reg: func(...string) publish.Registry { return w.reg }, Rule: rule, HasRule: true, Now: w.e.st.Now,
		Root: func() (root.TrustedMaterial, error) { return w.ss.Root, nil }}
	w.e.st.Publishers = w.p
	return w
}

func registryAt(repo string) delivery.Candidates {
	return delivery.Candidates{Registry: &delivery.RegistryCandidates{Repository: repo}}
}

// signer is a keyless signer of the world's Sigstore for token.
func (w *watchWorld) signer(token string) signing.Signer {
	w.t.Helper()
	s, err := signing.Keyless(w.ss.Fulcio, w.ss.TSA, "", func(context.Context) (string, error) { return token, nil }, w.ss.Root)
	must(w.t, err)
	return s
}

func (w *watchWorld) ci(subject, repo string) signing.Signer {
	return w.signer(sigtest.CIToken(subject, ghIssuer, repo, "refs/heads/main"))
}

// push pushes a random image to host/payments tagged tag, its source
// annotation set to source when not empty, signs it with each signer and
// returns its digest.
func (w *watchWorld) push(tag, source string, signers ...signing.Signer) string {
	w.t.Helper()
	img, _ := random.Image(32, 1)
	if source != "" {
		img = mutate.Annotations(img, map[string]string{publish.AnnotationSource: source}).(v1.Image)
	}
	repo := w.host + "/payments"
	d, err := w.reg.Write(w.t.Context(), repo, img)
	must(w.t, err)
	must(w.t, w.reg.Tag(w.t.Context(), repo, d, tag))
	for _, s := range signers {
		must(w.t, publish.SignDigest(w.t.Context(), w.reg, s, repo, d))
	}
	return d
}

// reconcile runs the stage reconciler once, the listing memo expired.
func (w *watchWorld) reconcile() *delivery.StageStatus {
	w.t.Helper()
	w.p.listings = nil
	must(w.t, w.e.reconcileStage("payments-prod"))
	st := w.e.getStage().Status
	return &st
}

func watchVerdict(t *testing.T, st *delivery.StageStatus, digest string) delivery.PublisherVerdict {
	t.Helper()
	if st.Publisher == nil {
		t.Fatalf("no status.publisher: %+v", st)
	}
	for _, v := range st.Publisher.Verdicts {
		if v.Digest == digest {
			return v
		}
	}
	t.Fatalf("no verdict on %s: %+v", digest, st.Publisher.Verdicts)
	return delivery.PublisherVerdict{}
}

func readyReasonOf(t *testing.T, st *delivery.StageStatus) *delivery.Condition {
	t.Helper()
	c := conditionOf(st.Conditions, "Ready")
	if c == nil {
		t.Fatalf("no Ready: %+v", st.Conditions)
	}
	return c
}

func TestWatchAcceptsRuleSigner(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	st := w.reconcile()
	v := watchVerdict(t, st, d)
	if v.Verdict != delivery.VerdictAccepted || v.Source != delivery.PublisherSourceRule || v.Version != "1.0.0" || v.At == "" ||
		!slices.Contains(st.Published, d) || st.Publisher.Source != delivery.PublisherSourceRule || st.Publisher.Rule == nil || st.Publisher.CheckedAt == "" {
		t.Fatalf("status %+v verdict %+v", st, v)
	}
	if c := readyReasonOf(t, st); c.Status != "True" {
		t.Fatalf("ready %+v", c)
	}
	if st.Candidate != d {
		t.Fatalf("the accepted digest is the candidate: %q", st.Candidate)
	}
}

func TestWatchUnsignedNeverPublishes(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", "")
	st := w.reconcile()
	if v := watchVerdict(t, st, d); v.Verdict != delivery.VerdictUnsigned || len(st.Published) != 0 || st.Candidate != "" {
		t.Fatalf("verdict %+v published %v", v, st.Published)
	}
}

func TestWatchUnknownNeverPublishes(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	w.reg.referrersErr = errors.New("referrers down")
	st := w.reconcile()
	if v := watchVerdict(t, st, d); v.Verdict != delivery.VerdictUnknown || !strings.Contains(v.Reason, "referrers down") || len(st.Published) != 0 {
		t.Fatalf("verdict %+v published %v", v, st.Published)
	}
}

func TestWatchNewSignerNeverPublishes(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	st := w.reconcile()
	v := watchVerdict(t, st, d)
	if v.Verdict != delivery.VerdictNewSigner || v.Identity == nil || v.Identity.Subject != otherSub || len(st.Published) != 0 {
		t.Fatalf("verdict %+v published %v", v, st.Published)
	}
	if ops := w.e.ops(); len(ops) != 1 || ops[0].Spec.Type != delivery.TypeTrust {
		t.Fatalf("one trust operation, and nothing promoted: %+v", ops)
	}
}

func TestWatchPersonSignedDeployRefused(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", "", w.signer(sigtest.Token("dana@example.com", "https://accounts.google.com")))
	st := w.reconcile()
	if v := watchVerdict(t, st, d); v.Verdict != delivery.VerdictRefused || !strings.Contains(v.Reason, "must reuse its CI-signed baseline's deploy digest") ||
		len(st.Published) != 0 {
		t.Fatalf("verdict %+v", v)
	}
}

func TestWatchSourceAnnotationMismatchRefused(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", "https://github.com/acme/ledger", w.ci(acmeSubject, acmeRepo))
	st := w.reconcile()
	if v := watchVerdict(t, st, d); v.Verdict != delivery.VerdictRefused || !strings.Contains(v.Reason, "acme/ledger") || len(st.Published) != 0 {
		t.Fatalf("verdict %+v", v)
	}
}

func TestWatchUnreachableRegistryNotReady(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	w.reconcile()
	w.reg.tagsErr = errors.New("connection refused")
	st := w.reconcile()
	c := readyReasonOf(t, st)
	if c.Status != "False" || c.Reason != delivery.ReadyReasonRegistryUnreadable || !strings.Contains(c.Message, "connection refused") ||
		!strings.Contains(st.Publisher.Message, "cannot be listed") {
		t.Fatalf("ready %+v publisher %+v", c, st.Publisher)
	}
	// What it accepted before stays: nothing new is published while it cannot list.
	if !slices.Contains(st.Published, d) || watchVerdict(t, st, d).Verdict != delivery.VerdictAccepted {
		t.Fatalf("published %v", st.Published)
	}
}

func TestWatchNoRootNotReady(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	w.push("1.0.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	fetches := 0
	w.p.Root = func() (root.TrustedMaterial, error) { fetches++; return nil, errors.New("tuf unreachable") }
	st := w.reconcile()
	if c := readyReasonOf(t, st); c.Reason != delivery.ReadyReasonPublisherUnverifiable || !strings.Contains(c.Message, "trusted root could not be fetched") ||
		len(st.Published) != 0 {
		t.Fatalf("ready %+v published %v", c, st.Published)
	}
	w.reconcile()
	if fetches != 1 {
		t.Fatalf("a failed root is fetched again only after a minute: %d fetches", fetches)
	}
	w.e.now = w.e.now.Add(rootRetry)
	w.p.Root = func() (root.TrustedMaterial, error) { fetches++; return w.ss.Root, nil }
	if st := w.reconcile(); len(st.Published) != 1 || fetches != 2 {
		t.Fatalf("a fetched root judges: %v, %d fetches", st.Published, fetches)
	}
	// No publisher check at all: never Ready.
	w.e.st.Publishers = nil
	if c := readyReasonOf(t, w.reconcile()); c.Reason != delivery.ReadyReasonPublisherUnverifiable || !strings.Contains(c.Message, "delivery.publishers") {
		t.Fatalf("ready %+v", c)
	}
}

// TestWatchDefaultsToApplierRepository: registry: {} watches the applier's
// repository, over plain HTTP when the applier says so.
func TestWatchDefaultsToApplierRepository(t *testing.T) {
	w := newWatch(t, delivery.Candidates{Registry: &delivery.RegistryCandidates{}})
	apply := &unstructured.Unstructured{}
	apply.SetGroupVersionKind(gvkOCI)
	must(t, w.e.c.Get(t.Context(), keyOf("payments"), apply))
	must(t, unstructured.SetNestedField(apply.Object, "oci://"+w.host+"/payments", "spec", "url"))
	must(t, unstructured.SetNestedField(apply.Object, true, "spec", "insecure"))
	must(t, w.e.c.Update(t.Context(), apply))
	var hosts []string
	w.p.Reg = func(h ...string) publish.Registry { hosts = h; return w.reg }
	d := w.push("1.0.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	if st := w.reconcile(); !slices.Contains(st.Published, d) || len(hosts) != 1 || hosts[0] != w.host {
		t.Fatalf("published %v hosts %v", st.Published, hosts)
	}
	// An applier that cannot be read names no repository to watch.
	w.e.fails["get/OCIRepository/payments"] = true
	if c, err := w.e.st.registryReady(t.Context(), w.e.getStage()); err == nil || c.Reason != "" {
		t.Fatalf("a failed applier read surfaces: %+v %v", c, err)
	}
	w.e.fails = fails{}
	must(t, w.e.c.Delete(t.Context(), apply))
	if c, err := w.e.st.registryReady(t.Context(), w.e.getStage()); err != nil || c.Reason != delivery.ReadyReasonRegistryUnreadable {
		t.Fatalf("a missing applier: %+v %v", c, err)
	}
}

func TestWatchBoundsTagsAndVerdicts(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	digests := make([]string, 0, 25)
	for i := range 25 {
		digests = append(digests, w.push(fmt.Sprintf("1.%d.0", i), ""))
	}
	must(t, w.reg.Tag(t.Context(), w.host+"/payments", digests[0], "latest")) // not a version
	st := w.reconcile()
	vs := st.Publisher.Verdicts
	if len(vs) != delivery.MaxPublisherVerdicts || vs[0].Version != "1.24.0" || vs[19].Version != "1.5.0" {
		t.Fatalf("the newest 20 versions, newest first: %d %+v", len(vs), vs)
	}
}

// TestWatchMemoReadsSignaturesOnce: the signature step is read once per
// digest; the judgement runs on every pass.
func TestWatchMemoReadsSignaturesOnce(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	repo := w.host + "/payments"
	svc := delivery.ServiceRef{Name: "payments"}
	vs, err := w.p.watch(t.Context(), w.reg, w.ss.Root, svc, repo, w.p.basisFor(t.Context(), w.reg, ns, svc, nil))
	must(t, err)
	reads := w.reg.referrers.Load()
	if reads == 0 || vs[0].Verdict != delivery.VerdictAccepted {
		t.Fatalf("first pass: %d reads %+v", reads, vs)
	}
	w.p.HasRule = false
	vs, err = w.p.watch(t.Context(), w.reg, w.ss.Root, svc, repo, w.p.basisFor(t.Context(), w.reg, ns, svc, nil))
	must(t, err)
	if w.reg.referrers.Load() != reads || vs[0].Verdict != delivery.VerdictNewSigner || vs[0].Digest != d {
		t.Fatalf("second pass: %d reads %+v", w.reg.referrers.Load(), vs)
	}
	// A failed read is read again after signerRetry, not before.
	w.reg.referrersErr = errors.New("down")
	w.p.signers = nil
	_, _ = w.p.signersOf(t.Context(), w.reg, w.ss.Root, repo, d)
	before := w.reg.referrers.Load()
	_, _ = w.p.signersOf(t.Context(), w.reg, w.ss.Root, repo, d)
	w.e.now = w.e.now.Add(signerRetry)
	_, _ = w.p.signersOf(t.Context(), w.reg, w.ss.Root, repo, d)
	if got := w.reg.referrers.Load(); got != before+1 {
		t.Fatalf("retry: %d reads after %d", got, before)
	}
	// The memo is bounded.
	w.p.signers = map[string]signersRead{}
	for i := range maxSignerMemo {
		w.p.signers[fmt.Sprint(i)] = signersRead{}
	}
	_, _ = w.p.signersOf(t.Context(), w.reg, w.ss.Root, repo, d)
	if len(w.p.signers) != 1 {
		t.Fatalf("memo bound: %d", len(w.p.signers))
	}
}

// TestWatchMemoPerService: two services that share a digest each get their
// own verdict.
func TestWatchMemoPerService(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	w.p.Rule.Services = []string{"payments"}
	repo := w.host + "/payments"
	pay, err := w.p.watch(t.Context(), w.reg, w.ss.Root, delivery.ServiceRef{Name: "payments"}, repo, w.p.basisFor(t.Context(), w.reg, ns, delivery.ServiceRef{Name: "payments"}, nil))
	must(t, err)
	ledger, err := w.p.watch(t.Context(), w.reg, w.ss.Root, delivery.ServiceRef{Name: "ledger"}, repo, w.p.basisFor(t.Context(), w.reg, ns, delivery.ServiceRef{Name: "ledger"}, nil))
	must(t, err)
	if pay[0].Verdict != delivery.VerdictAccepted || ledger[0].Verdict != delivery.VerdictNewSigner || ledger[0].Digest != d {
		t.Fatalf("payments %+v ledger %+v", pay, ledger)
	}
}

// TestWatchIgnoresContractPublishSign: the contract naming the signer in
// publish.sign never makes it a publisher.
func TestWatchIgnoresContractPublishSign(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	b := bundle("1.0.0")
	b.Contract.Publish = &contract.Publish{Sign: &contract.PublishSign{Keyless: true, Identity: otherSub, Issuer: ghIssuer}}
	w.e.reg.contracts[d], w.e.reg.bundles[contractRef(c1)] = contractRef(c1), b
	if v := watchVerdict(t, w.reconcile(), d); v.Verdict != delivery.VerdictNewSigner {
		t.Fatalf("verdict %+v", v)
	}
}

// TestWatchNoLongerAcceptedLeavesPublished: a judged digest of the window
// that is no longer accepted leaves status.published; the pointer and
// status.verified are never touched.
func TestWatchNoLongerAcceptedLeavesPublished(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	w.e.setStage(func(s *delivery.StageStatus) {
		s.Pointer, s.Verified, s.Published = d, []string{d}, []string{dA}
	})
	if st := w.reconcile(); !slices.Equal(st.Published, []string{dA, d}) {
		t.Fatalf("accepted: %v", st.Published)
	}
	w.p.HasRule, w.p.NoRule = false, "reg.example names no CI provider"
	st := w.reconcile()
	if !slices.Equal(st.Published, []string{dA}) || st.Pointer != d || !slices.Equal(st.Verified, []string{d}) ||
		st.Publisher.Source != delivery.PublisherSourceNone || st.Publisher.Message != "reg.example names no CI provider" {
		t.Fatalf("status %+v", st)
	}
}

// TestWatchWritesOnlyOnChange: an unchanged judgement is not written again
// until a resync passed, and a verdict keeps the time it was first reached.
func TestWatchWritesOnlyOnChange(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	first := watchVerdict(t, w.reconcile(), d).At
	w.e.fails["status/Stage"] = true
	w.reconcile()
	w.e.fails = fails{}
	w.e.now = w.e.now.Add(time.Hour)
	st := w.reconcile()
	if watchVerdict(t, st, d).At != first || st.Publisher.CheckedAt != w.e.now.UTC().Format(time.RFC3339) {
		t.Fatalf("at %q checked %q", watchVerdict(t, st, d).At, st.Publisher.CheckedAt)
	}
	w.e.now = w.e.now.Add(time.Hour)
	w.e.fails["status/Stage"] = true
	if err := w.e.reconcileStage("payments-prod"); err == nil {
		t.Fatal("a failed write surfaces")
	}
}

// TestWatchTagHeadFails: a tag whose manifest cannot be read is an unknown
// verdict naming it; one that vanished is left out.
func TestWatchTagHeadFails(t *testing.T) {
	reg := &headRegistry{tags: []string{"1.1.0", "1.0.0"}, heads: map[string]*publish.Remote{"r/p:1.1.0": nil}}
	got := heads(t.Context(), reg, "r/p", reg.tags)
	if len(got) != 1 || got[0].version != "1.0.0" || got[0].err == nil {
		t.Fatalf("heads %+v", got)
	}
	p := &Publishers{Now: time.Now}
	vs, err := p.watch(t.Context(), reg, nil, delivery.ServiceRef{Name: "payments"}, "r/p", basis{})
	if err != nil || len(vs) != 1 || vs[0].Verdict != delivery.VerdictUnknown || vs[0].Version != "1.0.0" {
		t.Fatalf("verdicts %+v %v", vs, err)
	}
	// The listing is reused within a resync.
	reg.tags = nil
	if vs, _ := p.watch(t.Context(), reg, nil, delivery.ServiceRef{Name: "payments"}, "r/p", basis{}); len(vs) != 1 {
		t.Fatalf("reused listing: %+v", vs)
	}
	if got := applyVerdicts([]string{dA}, vs); !slices.Equal(got, []string{dA}) {
		t.Fatalf("a verdict without a digest touches nothing: %v", got)
	}
}

// headRegistry lists tags and answers Head from heads, failing a ref it
// does not hold.
type headRegistry struct {
	publish.Registry
	tags  []string
	heads map[string]*publish.Remote
}

func (h *headRegistry) Tags(context.Context, string) ([]string, error) { return h.tags, nil }

func (h *headRegistry) Head(_ context.Context, ref string) (*publish.Remote, error) {
	if r, ok := h.heads[ref]; ok {
		return r, nil
	}
	return nil, errors.New("manifest unreadable")
}

func TestCandidateRegistryNewestAccepted(t *testing.T) {
	p := &delivery.PublisherStatus{Verdicts: []delivery.PublisherVerdict{
		{Digest: dC, Verdict: delivery.VerdictNewSigner}, {Digest: dB, Verdict: delivery.VerdictAccepted}, {Digest: dA, Verdict: delivery.VerdictAccepted}}}
	if got := newestAccepted(p); got != dB {
		t.Fatalf("newest accepted %q", got)
	}
	if newestAccepted(nil) != "" || newestAccepted(&delivery.PublisherStatus{}) != "" {
		t.Fatal("nothing accepted, no candidate")
	}
	e := newEnv(t, stageObj(dA))
	s := e.getStage()
	s.Spec.Candidates = registryAt("r/p")
	s.Status.Publisher, s.Status.Published = p, []string{dA, dB}
	if got, err := e.st.candidate(t.Context(), s, nil); err != nil || got != dB {
		t.Fatalf("candidate %q %v", got, err)
	}
}

// TestRegistryCandidatesPromoteEndToEnd: a Stage with a Flux applier and
// candidates.registry: {} sets status.candidate and promotes an accepted
// digest through pacto.published.
func TestRegistryCandidatesPromoteEndToEnd(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.1.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	w.e.reg.contracts[d] = contractRef(c2)
	st := w.reconcile()
	ops := w.e.ops()
	if st.Candidate != d || len(ops) != 1 || ops[0].Spec.Params.To != d {
		t.Fatalf("candidate %q ops %+v", st.Candidate, ops)
	}
	o := w.e.drive(ops[0].Name)
	i := slices.IndexFunc(o.Status.Verdicts, func(v delivery.Verdict) bool { return v.Gate == delivery.GatePublished })
	if i < 0 || o.Status.Verdicts[i].Result != delivery.Pass {
		t.Fatalf("pacto.published: %+v (%s)", o.Status.Verdicts, o.Status.Message)
	}
}

// TestCandidatesRegistryWithOCIRepositoryRefRefused: the apiserver refuses a
// Stage that sets candidates.registry beside candidates.ociRepositoryRef.
func TestCandidatesRegistryWithOCIRepositoryRefRefused(t *testing.T) {
	c := sarEnv(t)
	s := sarStage("payments")
	s.Spec.ApplyRef = delivery.ApplyRef{Name: "payments"}
	s.Spec.Candidates = delivery.Candidates{Registry: &delivery.RegistryCandidates{}, OCIRepositoryRef: &delivery.LocalRef{Name: "payments-candidates"}}
	err := c.Create(t.Context(), s)
	if err == nil || !strings.Contains(err.Error(), "set at most one of candidates.registry") {
		t.Fatalf("both sources admitted: %v", err)
	}
	s.Spec.Candidates.OCIRepositoryRef = nil
	must(t, c.Create(t.Context(), s))
}

// TestRootRefreshedKeepsLastGood: a fetched root is fetched again after
// rootRefresh, so keys Sigstore adds are trusted; a refresh that fails keeps
// the last good root and is tried again after rootRetry.
func TestRootRefreshedKeepsLastGood(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	fetches := 0
	w.p.Root = func() (root.TrustedMaterial, error) { fetches++; return w.ss.Root, nil }
	w.reconcile()
	w.e.now = w.e.now.Add(rootRefresh - time.Second)
	w.reconcile()
	if fetches != 1 {
		t.Fatalf("fetched %d times within rootRefresh", fetches)
	}
	w.e.now = w.e.now.Add(2 * time.Second)
	w.p.Root = func() (root.TrustedMaterial, error) { fetches++; return nil, errors.New("tuf unreachable") }
	st := w.reconcile()
	if fetches != 2 || !slices.Contains(st.Published, d) || readyReasonOf(t, st).Status != "True" {
		t.Fatalf("a failed refresh: %d fetches, published %v", fetches, st.Published)
	}
	w.reconcile()
	w.e.now = w.e.now.Add(rootRetry)
	w.reconcile()
	if fetches != 3 {
		t.Fatalf("a failed refresh is tried again after rootRetry: %d fetches", fetches)
	}
}

// hostRewrite sends every request to host, whatever host the URL names.
type hostRewrite string

func (h hostRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Host = string(h)
	return http.DefaultTransport.RoundTrip(req)
}

// TestWatchIgnoresAProbeOfAnEarlierServer: the process probe cache is keyed
// by host:port, and httptest reuses ports. A registry with the Referrers API
// that held the watch registry's port earlier never makes the watch, whose
// registry serves no Referrers API, read signatures through that API.
func TestWatchIgnoresAProbeOfAnEarlierServer(t *testing.T) {
	process := ocireferrer.Probes
	w := newWatch(t, registryAt("-"))
	native := newTestRegistry(t)
	earlier := &orasremote.Repository{
		Reference: orasregistry.Reference{Registry: w.host, Repository: "apps/payments"},
		PlainHTTP: true, Client: &http.Client{Transport: hostRewrite(native.host)},
	}
	if caps, err := process.Probe(t.Context(), earlier, native.to, false); err != nil || caps.Referrers != capabilities.ReferrersNative {
		t.Fatalf("probe of the earlier server: %+v %v", caps, err)
	}
	d := w.push("1.0.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	if v := watchVerdict(t, w.reconcile(), d); v.Verdict != delivery.VerdictAccepted {
		t.Fatalf("verdict %+v", v)
	}
}
