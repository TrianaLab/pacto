package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/discover"
)

// intentOf is the --intent value a command names.
func intentOf(t *testing.T, cli string) string {
	t.Helper()
	_, after, ok := strings.Cut(cli, "--intent ")
	if !ok {
		t.Fatalf("%q names no --intent", cli)
	}
	return strings.Fields(after)[0]
}

// TestAcceptPlanStaleCommandReplaysTheRun: a stale plan's command names
// every argument of the run, --from included, and the new intent digest,
// so running it plans exactly the digest it carries.
func TestAcceptPlanStaleCommandReplaysTheRun(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	opts := e.trust()
	opts.Break, opts.From, opts.Intent = "ledger@3.0.0", writeDir(t, ledgerFiles("3.0.0", "2026-12-31", "reports")), "sha256:00"
	de := e.refusedAccept(t, opts)
	if de.Code != codePlanStale || !strings.Contains(de.CLI, "--break ledger@3.0.0 --from "+opts.From+" --intent sha256:") {
		t.Fatalf("refusal = %+v", de)
	}
	opts.Intent = intentOf(t, de.CLI)
	e.plan(t, opts)
	// A stale draft names the draft again.
	draft := e.agentDraft(t, e.proposedOpts())
	e.publishBundle(t, e.repo, "1.7.0", e.reportsFiles("1.7.0", true), nil)
	de = e.refusedAccept(t, e.draftOpts(draft))
	if de.Code != codePlanStale || !strings.HasPrefix(de.CLI, "pacto accept "+e.repo+" --draft "+draft+" --intent sha256:") {
		t.Fatalf("refusal = %+v", de)
	}
	do := e.draftOpts(draft)
	do.Intent = intentOf(t, de.CLI)
	e.plan(t, do)
}

// TestAcceptDraftOfUnpublishedBreakNamesFrom: the command a person accepts
// an agent's draft of an unpublished break with reads it from the same
// --from, and a refusal of a draft run names the draft.
func TestAcceptDraftOfUnpublishedBreakNamesFrom(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	opts := e.trust()
	opts.Signing = e.signing()
	opts.Break, opts.From = "ledger@3.0.0", writeDir(t, ledgerFiles("3.0.0", "2026-12-31", "reports"))
	opts.Agent, opts.OnBehalfOf = true, "ana"
	res := e.accept(t, opts)
	if res.AcceptCLI != "pacto accept "+e.repo+" --draft "+res.Draft+" --from "+opts.From {
		t.Fatalf("acceptCli = %q", res.AcceptCLI)
	}
	de := e.refusedAccept(t, e.draftOpts(res.Draft))
	if de.Code != codeBreakNotAnnounced || !strings.Contains(de.CLI, "--draft "+res.Draft+" --from <checkout") {
		t.Fatalf("refusal = %+v", de)
	}
}

// TestAcceptRefusesTwoKindsOfRecord: a break with ranges, or a draft with
// anything else, is refused before the registry is read.
func TestAcceptRefusesTwoKindsOfRecord(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	e.reset()
	for _, o := range []AcceptOptions{
		{Ref: e.repo, Break: "ledger@3.0.0", Proposed: true},
		{Ref: e.repo, Break: "ledger@3.0.0", Fields: []string{"dependencies[billing].compatibility"}},
		{Ref: e.repo, Draft: e.repo + ":pacto-accept-draft-1", Proposed: true},
		{Ref: e.repo, Draft: e.repo + ":pacto-accept-draft-1", Break: "ledger@3.0.0"},
	} {
		if de := e.refusedAccept(t, o); de.Code != codeFieldNotAcceptable {
			t.Errorf("%+v: %+v", o, de)
		}
	}
	if n := e.requests(""); n != 0 {
		t.Errorf("a refused combination sent %d requests", n)
	}
}

// TestAcceptTaggedOrPinnedRef: a tagged or pinned reference names its
// repository's newest version.
func TestAcceptTaggedOrPinnedRef(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	for _, ref := range []string{"oci://" + e.repo + ":1.6.0", "oci://" + e.subject, e.repo + ":latest"} {
		opts := e.trust()
		opts.Ref, opts.Proposed = ref, true
		if p := e.plan(t, opts); p.Subject != e.subject {
			t.Errorf("%s: subject %s", ref, p.Subject)
		}
	}
}

// TestAcceptBreakFromCheckoutDatesUndatedBreak: an undated break read from
// a checkout expires as pacto ci and pacto impact date it.
func TestAcceptBreakFromCheckoutDatesUndatedBreak(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	from := writeDir(t, ledgerFiles("3.0.0", "", "reports"))
	commit := "0123456789abcdef0123456789abcdef01234567"
	if err := os.MkdirAll(filepath.Join(from, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(from, ".git", "HEAD"), []byte(commit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := e.trust()
	opts.Break, opts.From, opts.Getenv = "ledger@3.0.0", from, mapEnv(map[string]string{"SOURCE_DATE_EPOCH": "1795000000"})
	want, _ := discover.BreakExpiry(1795000000, commit)
	if p := e.plan(t, opts); want == "" || p.Expires != want {
		t.Fatalf("expires %q, want %q", p.Expires, want)
	}
}
