package app

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// accept runs pacto accept with opts and fails the test on an error.
func (e *acceptEnv) accept(t *testing.T, opts AcceptOptions) *AcceptResult {
	t.Helper()
	res, err := e.svc.Accept(t.Context(), opts)
	if err != nil {
		t.Fatalf("%v\n%+v", err, res)
	}
	return res
}

// acceptTags are the reports repository's acceptance and draft tags.
func (e *acceptEnv) acceptTags(t *testing.T) []string {
	t.Helper()
	r, err := ocireferrer.Open(e.repo, ocireferrer.Options{Keychain: anonymous})
	if err != nil {
		t.Fatal(err)
	}
	tags, err := ocireferrer.Tags(t.Context(), r, delivery.AcceptanceTagPrefix)
	if err != nil {
		t.Fatal(err)
	}
	return tags
}

// proposedOpts accepts every proposed range of reports, signed by the test
// Sigstore under an installation that lets its people sign.
func (e *acceptEnv) proposedOpts() AcceptOptions {
	opts := e.trust()
	opts.Proposed, opts.Signing = true, e.signing()
	return opts
}

func TestWriteAcceptanceNativeReferrer(t *testing.T) {
	e := newAcceptEnv(t, true)
	bundle := []byte(`{"not":"verified here"}`)
	ref, err := e.svc.writeAcceptance(t.Context(), nil, e.repo, e.digest, bundle)
	if err != nil || !strings.HasPrefix(ref, e.repo+"@sha256:") || len(e.acceptTags(t)) != 0 {
		t.Fatalf("%q %v %v", ref, err, e.acceptTags(t))
	}
	r, _ := ocireferrer.Open(e.repo, ocireferrer.Options{Keychain: anonymous})
	subject, _ := r.Resolve(t.Context(), e.digest)
	descs, _, err := ocireferrer.Attached(t.Context(), r, subject, "")
	if err != nil || len(descs) != 1 || e.repo+"@"+descs[0].Digest.String() != ref {
		t.Fatalf("%v %+v", err, descs)
	}
}

func TestWriteAcceptanceWriteOnceTag(t *testing.T) {
	e := newAcceptEnv(t, false)
	ref, err := e.svc.writeAcceptance(t.Context(), nil, e.repo, e.digest, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	tags := e.acceptTags(t)
	_, hex, _ := strings.Cut(e.digest, ":")
	_, mhex, _ := strings.Cut(ref, "@sha256:")
	if len(tags) != 1 || tags[0] != delivery.AcceptanceTagPrefix+hex+"-"+mhex[:12] || e.requests("/manifests/sha256-") != 0 {
		t.Fatalf("tags %v, requests %v", tags, e.paths)
	}
}

func TestWriteAcceptanceIdempotent(t *testing.T) {
	for _, referrers := range []bool{true, false} {
		e := newAcceptEnv(t, referrers)
		first, err1 := e.svc.writeAcceptance(t.Context(), nil, e.repo, e.digest, []byte(`{}`))
		second, err2 := e.svc.writeAcceptance(t.Context(), nil, e.repo, e.digest, []byte(`{}`))
		if err1 != nil || err2 != nil || first != second {
			t.Fatalf("referrers %v: %q %q %v %v", referrers, first, second, err1, err2)
		}
	}
}

func TestWriteAcceptanceFailures(t *testing.T) {
	e := newAcceptEnv(t, false)
	if _, err := e.svc.writeAcceptance(t.Context(), nil, e.repo, "sha256:"+strings.Repeat("0", 64), nil); err == nil {
		t.Fatal("a missing subject was written to")
	}
	e.failWhen(func(r *http.Request) bool { return r.Method == http.MethodPut })
	if _, err := e.svc.writeAcceptance(t.Context(), nil, e.repo, e.digest, []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "writing the acceptance") {
		t.Fatal(err)
	}
	e.failWhen(nil)
	e.svc.sys.refusalRepo = func(string) (*remote.Repository, error) { return nil, errors.New("no credentials") }
	if _, err := e.svc.writeAcceptance(t.Context(), nil, e.repo, e.digest, nil); err == nil {
		t.Fatal("an unopened repository was written to")
	}
	if _, err := e.svc.writeAcceptanceDraft(t.Context(), nil, e.repo, delivery.AcceptanceDraft{}, nil); err == nil {
		t.Fatal("an unopened repository was written to")
	}
}

func TestWriteAcceptanceProbeFails(t *testing.T) {
	e := newAcceptEnv(t, false)
	ocireferrer.Probes = &ocireferrer.Prober{}
	t.Cleanup(func() { ocireferrer.Probes = &ocireferrer.Prober{} })
	e.failWhen(func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/referrers/") })
	if _, err := e.svc.writeAcceptance(t.Context(), nil, e.repo, e.digest, nil); err == nil || !strings.Contains(err.Error(), "probing") {
		t.Fatal(err)
	}
}

func TestWriteAcceptanceDraftNumbering(t *testing.T) {
	e := newAcceptEnv(t, false)
	d := delivery.AcceptanceDraft{Record: e.rangesRecord(), Subject: e.subject, OnBehalfOf: "ana"}
	first, err := e.svc.writeAcceptanceDraft(t.Context(), nil, e.repo, d, nil)
	if err != nil || first != "pacto-accept-draft-1" {
		t.Fatalf("%q %v", first, err)
	}
	// Another run took the next number meanwhile: the draft takes the one after.
	other := d
	other.OnBehalfOf = "bob"
	seen := 0
	e.failWhen(func(r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/manifests/pacto-accept-draft-2") {
			if seen++; seen == 1 {
				if _, err := e.svc.writeAcceptanceDraft(context.Background(), nil, e.repo, other, nil); err != nil {
					t.Error(err)
				}
			}
		}
		return false
	})
	third := d
	third.OnBehalfOf = "carol"
	if tag, err := e.svc.writeAcceptanceDraft(t.Context(), nil, e.repo, third, nil); err != nil || tag != "pacto-accept-draft-3" {
		t.Fatalf("%q %v", tag, err)
	}
	e.failWhen(nil)
	tags := e.acceptTags(t)
	if !slices.Equal(tags, []string{"pacto-accept-draft-1", "pacto-accept-draft-2", "pacto-accept-draft-3"}) || slices.ContainsFunc(tags, func(s string) bool { return strings.HasPrefix(s, "draft-") }) {
		t.Fatal(tags)
	}
}

func TestWriteAcceptanceDraftRepeatedReusesNewest(t *testing.T) {
	e := newAcceptEnv(t, false)
	d := delivery.AcceptanceDraft{Record: e.rangesRecord(), Subject: e.subject, OnBehalfOf: "ana"}
	for range 2 {
		if tag, err := e.svc.writeAcceptanceDraft(t.Context(), nil, e.repo, d, nil); err != nil || tag != "pacto-accept-draft-1" {
			t.Fatalf("%q %v", tag, err)
		}
	}
	// Only the newest draft is compared: an older identical one is not.
	other := d
	other.OnBehalfOf = "bob"
	for _, w := range []struct {
		d    delivery.AcceptanceDraft
		want string
	}{{other, "pacto-accept-draft-2"}, {d, "pacto-accept-draft-3"}} {
		if tag, err := e.svc.writeAcceptanceDraft(t.Context(), nil, e.repo, w.d, nil); err != nil || tag != w.want {
			t.Fatalf("%q %v, want %s", tag, err, w.want)
		}
	}
	if tags := e.acceptTags(t); len(tags) != 3 {
		t.Fatal(tags)
	}
}

func TestReadAcceptanceDraft(t *testing.T) {
	e := newAcceptEnv(t, false)
	d := delivery.AcceptanceDraft{Record: e.rangesRecord(), Subject: e.subject, OnBehalfOf: "ana"}
	tag, err := e.svc.writeAcceptanceDraft(t.Context(), nil, e.repo, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, signed, err := e.svc.readAcceptanceDraft(t.Context(), e.repo+":"+tag)
	if err != nil || signed != nil || got.OnBehalfOf != "ana" || got.Subject != e.subject || got.Record.Service != d.Record.Service {
		t.Fatalf("%v %+v", err, got)
	}
	r, err := ocireferrer.Open(e.repo, ocireferrer.Options{Keychain: anonymous})
	if err != nil {
		t.Fatal(err)
	}
	for tag, art := range map[string]ocireferrer.Artifact{
		"pacto-accept-draft-7": ocireferrer.Build(delivery.AcceptanceArtifactType, delivery.AcceptanceArtifactType, []byte("{}"), nil, nil),
		"pacto-accept-draft-8": ocireferrer.Build(delivery.AcceptanceDraftArtifactType, delivery.AcceptanceDraftArtifactType, []byte(`{"record":{}}`), nil, nil),
	} {
		if err := ocireferrer.PushWriteOnce(t.Context(), r, art, tag); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []string{e.repo + ":pacto-accept-draft-9", e.repo + ":1.6.0", e.repo, e.repo + ":pacto-accept-draft-7", e.repo + ":pacto-accept-draft-8"} {
		var de *DeclareError
		if _, _, err := e.svc.readAcceptanceDraft(t.Context(), ref); !errors.As(err, &de) || de.Code != codeDraftNotFound {
			t.Errorf("%s: %v", ref, err)
		}
	}
}

// A registry or credential failure while reading a draft is an error, never a missing draft.
func TestReadAcceptanceDraftTransportFailures(t *testing.T) {
	e := newAcceptEnv(t, false)
	d := delivery.AcceptanceDraft{Record: e.rangesRecord(), Subject: e.subject, OnBehalfOf: "ana"}
	tag, err := e.svc.writeAcceptanceDraft(t.Context(), nil, e.repo, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"/blobs/", "/manifests/"} {
		e.failWhen(func(r *http.Request) bool { return strings.Contains(r.URL.Path, part) })
		if _, _, err := e.svc.readAcceptanceDraft(t.Context(), e.repo+":"+tag); err == nil || errors.As(err, new(*DeclareError)) {
			t.Fatalf("%s: %v", part, err)
		}
	}
	e.failWhen(nil)
	e.svc.sys.refusalRepo = func(string) (*remote.Repository, error) { return nil, errors.New("no credentials") }
	if _, _, err := e.svc.readAcceptanceDraft(t.Context(), e.repo+":"+tag); err == nil || errors.As(err, new(*DeclareError)) {
		t.Fatal(err)
	}
}

func TestWriteAcceptanceDraftLosesTwice(t *testing.T) {
	e := newAcceptEnv(t, false)
	d := delivery.AcceptanceDraft{Record: e.rangesRecord(), Subject: e.subject}
	seen := 0
	e.failWhen(func(r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/manifests/pacto-accept-draft-1") {
			if seen++; seen == 1 {
				for _, who := range []string{"bob", "carol"} {
					o := d
					o.OnBehalfOf = who
					if _, err := e.svc.writeAcceptanceDraft(context.Background(), nil, e.repo, o, nil); err != nil {
						t.Error(err)
					}
				}
			}
		}
		return false
	})
	_, err := e.svc.writeAcceptanceDraft(t.Context(), nil, e.repo, d, nil)
	var de *DeclareError
	if !errors.As(err, &de) || de.Code != codeVersionConflict {
		t.Fatal(err)
	}
	e.failWhen(func(r *http.Request) bool { return strings.HasSuffix(r.URL.Path, "/tags/list") })
	if _, err := e.svc.writeAcceptanceDraft(t.Context(), nil, e.repo, d, nil); err == nil || !strings.Contains(err.Error(), "listing the acceptance drafts") {
		t.Fatal(err)
	}
}

func TestAcceptSignsAsPerson(t *testing.T) {
	for _, referrers := range []bool{true, false} {
		e := newAcceptEnv(t, referrers)
		calls := e.login(personToken("ana@acme.com"))
		res := e.accept(t, e.proposedOpts())
		if !res.Executed || res.State != impact.AcceptCounted || res.Signer.Subject != "ana@acme.com" || res.Draft != "" || *calls != 1 ||
			!strings.HasPrefix(res.Record, e.repo+"@sha256:") {
			t.Fatalf("referrers %v: %+v", referrers, res)
		}
		// Readers count it.
		got, _ := e.read(t, e.store(t), e.query())
		if len(got) != 1 || got[0].State != impact.AcceptCounted || got[0].Record.Intent != res.Plan.IntentDigest || got[0].Ref != res.Record {
			t.Fatalf("read %+v", got)
		}
	}
}

func TestAcceptByNonOwnerIsRouted(t *testing.T) {
	e := newAcceptEnv(t, false)
	e.login(personToken("bob@acme.com"))
	res := e.accept(t, e.proposedOpts())
	if res.State != impact.AcceptRouted || res.Reason != "waiting for the owner of reports: ana@acme.com" {
		t.Fatalf("%+v", res)
	}
}

func TestAcceptSignerNotAllowed(t *testing.T) {
	e := newAcceptEnv(t, false)
	e.login(testutil.TokenWith(map[string]string{"sub": "ana@acme.com", "email": "ana@acme.com", "email_verified": "true", "iss": "https://other.example"}))
	_, err := e.svc.Accept(t.Context(), e.proposedOpts())
	var de *DeclareError
	if !errors.As(err, &de) || de.Code != codeSignerNotAllowed || len(e.acceptTags(t)) != 0 {
		t.Fatal(err, e.acceptTags(t))
	}
}

func TestAcceptCertIdentityMismatchStopsBeforeTag(t *testing.T) {
	e := newAcceptEnv(t, false)
	e.login(testutil.TokenWith(map[string]string{"sub": "mallory@acme.com", "email": "ana@acme.com", "email_verified": "true", "iss": personIssuer}))
	_, err := e.svc.Accept(t.Context(), e.proposedOpts())
	var de *DeclareError
	if !errors.As(err, &de) || de.Code != codeSignerUnverified || len(e.acceptTags(t)) != 0 {
		t.Fatal(err, e.acceptTags(t))
	}
}

func TestAcceptPlanStale(t *testing.T) {
	e := newAcceptEnv(t, false)
	calls := e.login(personToken("ana@acme.com"))
	opts := e.proposedOpts()
	opts.Intent = "sha256:00"
	_, err := e.svc.Accept(t.Context(), opts)
	var de *DeclareError
	if !errors.As(err, &de) || de.Code != codePlanStale || *calls != 0 || len(e.acceptTags(t)) != 0 {
		t.Fatal(err, *calls)
	}
}

func TestAcceptWithAmbientTokenWritesDraft(t *testing.T) {
	e := newAcceptEnv(t, false)
	t.Setenv("SIGSTORE_ID_TOKEN", ciWorkflowToken())
	calls := e.login(personToken("ana@acme.com"))
	opts := e.proposedOpts()
	opts.OnBehalfOf = "ana"
	res := e.accept(t, opts)
	if res.Draft != e.repo+":pacto-accept-draft-1" || res.Record != "" || *calls != 0 || res.Signer.Class != string(delivery.ClassCI) ||
		res.AcceptCLI != "pacto accept "+e.repo+" --draft "+res.Draft {
		t.Fatalf("%+v", res)
	}
	// A key signs a draft too, and no key at all fails before anything is written.
	noAmbient(t)
	opts.Signing.Key = "missing.key"
	if _, err := e.svc.Accept(t.Context(), opts); err == nil {
		t.Fatal("a missing key signed")
	}
}

func TestAcceptUnsignedDraftAndFailures(t *testing.T) {
	e := newAcceptEnv(t, false)
	p := e.plan(t, e.proposedOpts())
	res := &AcceptResult{Plan: p}
	if err := e.svc.draftAcceptance(t.Context(), res, p.record, unsignedChoice("an agent"), AcceptOptions{Ref: e.repo, OnBehalfOf: "ana"}); err != nil || res.Draft != e.repo+":pacto-accept-draft-1" {
		t.Fatalf("%v %+v", err, res)
	}
	e.failWhen(func(r *http.Request) bool {
		return strings.HasSuffix(r.URL.Path, "/tags/list") && r.URL.Query().Get("last") != ""
	})
	if err := e.svc.draftAcceptance(t.Context(), res, p.record, unsignedChoice("an agent"), AcceptOptions{}); err == nil {
		t.Fatal("a failed listing wrote a draft")
	}
	e.failWhen(nil)
	// No proposal plans nothing to sign.
	opts := e.proposedOpts()
	opts.Proposed, opts.Fields = false, nil
	if _, err := e.svc.Accept(t.Context(), opts); err == nil {
		t.Fatal("nothing was accepted")
	}
	// A login that fails writes nothing.
	e.svc.sys.login = func(context.Context, declareLogin) (string, error) { return "", errors.New("cancelled") }
	if _, err := e.svc.Accept(t.Context(), e.proposedOpts()); err == nil || len(e.acceptTags(t)) != 1 {
		t.Fatal(err)
	}
}

func TestInstallationSigners(t *testing.T) {
	if installationSigners(t.Context(), nil) != nil ||
		installationSigners(t.Context(), func(context.Context) (Installation, error) { return Installation{}, errors.New("x") }) != nil {
		t.Fatal("no policy read refuses nobody")
	}
}

// reader reads the acceptances of reports in snap through e's registry.
func (e *acceptEnv) reader(t *testing.T, snap *fleet.FleetSnapshot) *acceptanceReader {
	t.Helper()
	entry := contract.Break{Consumers: []string{"reports"}, Reason: "the v1 API is removed", Expires: "2026-12-31"}
	in := &impact.BreakInput{Announced: map[string]impact.BreakAnnouncement{"reports": {Announcement: delivery.BreakAnnouncement(entry), Expires: "2026-12-31"}}}
	return &acceptanceReader{s: e.svc, snap: snap, in: in, store: e.store(t)}
}

func TestAcceptanceReaderLookupSet(t *testing.T) {
	e := newAcceptEnv(t, false)
	e.attach(t, attachment{record: e.rangesRecord(), token: personToken("ana@acme.com"), tag: true})
	key := fleet.ServiceKey(e.key("reports"))
	c := &contract.Contract{Service: contract.Service{Name: "reports", Version: "1.6.0", Owner: contract.Owner{DRI: "ana@acme.com"}}}
	snap := &fleet.FleetSnapshot{
		Revisions: map[fleet.RevisionKey]*fleet.ContractRevision{
			"run":   {Key: "run", ServiceKey: key, Version: "1.6.0", Contract: c, ResolvedRef: "oci://" + e.subject},
			"other": {Key: "other", ServiceKey: "billing", Contract: c},
			"bare":  {Key: "bare", ServiceKey: key},
		},
		Targets: map[fleet.TargetKey]*fleet.TargetRecord{"t": {ServiceKey: key, ContractRevision: "run"}, "u": {ServiceKey: "billing"}},
	}
	rd := e.reader(t, snap)
	brk := delivery.AcceptanceRecord{Service: e.key("reports"), Version: "1.6.0", Kind: delivery.AcceptKindBreak,
		Break: &delivery.AcceptedBreak{Provider: e.key("ledger"), Version: "3.0.0", Announcement: rd.in.Announced["reports"].Announcement}}
	e.attach(t, attachment{record: brk, token: personToken("ana@acme.com"), tag: true})
	accs, complete := rd.read(t.Context(), key)
	if a := accs[len(accs)-1]; !complete || a.State != impact.AcceptCounted || a.Break == nil {
		t.Fatalf("%v %+v", complete, accs)
	}
	// A repository with no version is nothing to read; one that fails to
	// list leaves the answer incomplete, and so does a failed record read.
	none := fleet.ServiceKey(string(fleet.NewServiceKeyDomain(e.host+"/acme", "nobody")))
	if accs, complete := e.reader(t, snap).read(t.Context(), none); !complete || len(accs) != 0 {
		t.Fatalf("%v %+v", complete, accs)
	}
	e.failWhen(func(r *http.Request) bool { return strings.HasSuffix(r.URL.Path, "/reports/tags/list") })
	if _, complete := e.reader(t, snap).read(t.Context(), key); complete {
		t.Fatal("a failed listing read complete")
	}
	e.failWhen(func(r *http.Request) bool {
		return strings.Contains(r.URL.Path, "/manifests/"+delivery.AcceptanceTagPrefix)
	})
	if _, complete := e.reader(t, snap).read(t.Context(), key); complete {
		t.Fatal("a failed record read complete")
	}
	e.failWhen(nil)
	if isNotPublished(errors.New("x")) {
		t.Fatal("any error")
	}
}

// TestAcceptanceReaderSkipsAnotherServicesBundle: a consumer whose
// contract names another service's repository in publish.bundle never
// reads that service's acceptances as its own.
func TestAcceptanceReaderSkipsAnotherServicesBundle(t *testing.T) {
	e := newAcceptEnv(t, false)
	billing := fleet.ServiceKey(e.key("billing"))
	c := &contract.Contract{Service: contract.Service{Name: "billing", Version: "1.0.0"}, Publish: &contract.Publish{Bundle: "oci://" + e.repo}}
	snap := &fleet.FleetSnapshot{Revisions: map[fleet.RevisionKey]*fleet.ContractRevision{"b": {Key: "b", ServiceKey: billing, Version: "1.0.0", Contract: c}}}
	rd := e.reader(t, snap)
	rd.in.Announced["billing"] = rd.in.Announced["reports"]
	brk := delivery.AcceptanceRecord{Service: e.key("reports"), Version: "1.6.0", Kind: delivery.AcceptKindBreak,
		Break: &delivery.AcceptedBreak{Provider: e.key("ledger"), Version: "3.0.0", Announcement: rd.in.Announced["reports"].Announcement}}
	e.attach(t, attachment{record: brk, token: personToken("ana@acme.com"), tag: true})
	if accs, complete := rd.read(t.Context(), billing); !complete || len(accs) != 0 {
		t.Fatalf("billing read reports's acceptances: %v %+v", complete, accs)
	}
}

func TestAcceptSigningFailures(t *testing.T) {
	e := newAcceptEnv(t, false)
	e.login(personToken("ana@acme.com"))
	opts := e.proposedOpts()
	opts.Signing.FulcioURL = "http://127.0.0.1:1"
	if _, err := e.svc.Accept(t.Context(), opts); err == nil || len(e.acceptTags(t)) != 0 {
		t.Fatal("a failed signature wrote")
	}
	e.failWhen(func(r *http.Request) bool { return r.Method == http.MethodPut })
	if _, err := e.svc.Accept(t.Context(), e.proposedOpts()); err == nil {
		t.Fatal("a failed write accepted")
	}
	e.failWhen(nil)
	s, err := signing.Keyless(e.ss.Fulcio, e.ss.TSA, "", func(context.Context) (string, error) { return "", errors.New("no token") }, e.ss.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.writeAcceptanceDraft(t.Context(), nil, e.repo, delivery.AcceptanceDraft{Subject: e.subject}, &s); err == nil || !strings.Contains(err.Error(), "signing the acceptance draft") {
		t.Fatal(err)
	}
}
