package main

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/examples/story"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// trustItems are the trust operations in alice's inbox.
func trustItems(t *testing.T, d *demoDelivery, h http.Handler) []dashboard.InboxItem {
	t.Helper()
	_ = d.SetPersona("alice", false)
	return slices.DeleteFunc(inbox(t, h), func(it dashboard.InboxItem) bool { return it.Type != delivery.TypeTrust })
}

// TestDemoInboxHasOneNewSigner: audit-log's hotfix workflow is the one CI
// identity no rule or pin covers, so exactly one trust item waits, for a
// person, with the signer and the repository it attests.
func TestDemoInboxHasOneNewSigner(t *testing.T) {
	d, h := demoServer(t)
	items := trustItems(t, d, h)
	if len(items) != 1 {
		t.Fatalf("trust items %+v, want one", items)
	}
	it := items[0]
	if it.Service != "audit-log" || it.Publisher == nil || it.Phase != delivery.PhaseHeld || it.Decision != delivery.Approve ||
		it.Publisher.Subject != "https://github.com/trianalab/audit-log/.github/workflows/hotfix.yml@refs/heads/hotfix" ||
		it.Publisher.Repository != "https://github.com/trianalab/audit-log" || !strings.Contains(it.Question, "Confirm it may publish audit-log") {
		t.Fatalf("trust item %+v publisher %+v", it, it.Publisher)
	}
	_ = d.SetPersona(story.PayAgent.Name, false)
	if items := inbox(t, h); len(items) != 0 {
		t.Fatalf("an agent is asked to confirm a signer: %+v", items)
	}
}

// TestDemoConfirmSignerPins: alice confirms it; the operation concludes
// moving nothing, the signer is pinned under her name and the watch accepts
// the digest by the pin. An agent cannot confirm it.
func TestDemoConfirmSignerPins(t *testing.T) {
	d, h := demoServer(t)
	it := trustItems(t, d, h)[0]
	decide := "/api/delivery/operations/" + it.Namespace + "/" + it.Name + "/decision"
	_ = d.SetPersona(story.PayAgent.Name, false)
	if code := call(t, h, "POST", decide, `{"decision":"approve"}`, nil); code != 403 {
		t.Fatalf("an agent confirmed a signer: %d", code)
	}
	_ = d.SetPersona("alice", false)
	if code := call(t, h, "POST", decide, `{"decision":"approve"}`, nil); code != 201 {
		t.Fatalf("alice: %d", code)
	}
	o, _ := d.Get(t.Context(), it.Namespace, it.Name)
	st, _ := d.Stage(t.Context(), story.Prod, "audit-log")
	if o.Status.Outcome != delivery.Succeeded || !strings.HasPrefix(o.Status.Reason, "SignerConfirmed") || st.Status.Pointer != "" || slices.Contains(st.Status.Verified, auditLogPending) {
		t.Fatalf("trust operation %+v, Stage %+v", o.Status, st.Status)
	}
	pins := d.pins["audit-log"]
	if len(pins) != 1 || pins[0].Approver != story.Alice.User || pins[0].Subject != it.Publisher.Subject || pins[0].Operation != o.Metadata.UID {
		t.Fatalf("pins %+v", pins)
	}
	v := st.Status.Publisher.Verdicts[0]
	if v.Digest != auditLogPending || v.Verdict != delivery.VerdictAccepted || v.Source != delivery.PublisherSourcePin {
		t.Fatalf("verdict after the pin %+v", v)
	}
	if items := trustItems(t, d, h); len(items) != 0 {
		t.Fatalf("the confirmed signer is still asked: %+v", items)
	}
}

// TestDemoTrustView: the installation's rule for ghcr.io/trianalab, bob's
// pin of orders, audit-log's pending signer and payments accepted by the rule.
func TestDemoTrustView(t *testing.T) {
	_, h := demoServer(t)
	var v delivery.TrustView
	if code := call(t, h, "GET", "/api/delivery/trust", "", &v); code != 200 {
		t.Fatalf("trust %d", code)
	}
	if v.RuleSource != delivery.PublisherSourceRule || v.Rule == nil || v.Rule.Name != "derived from ghcr.io/trianalab" || v.AsOf == "" || v.Completeness != "complete" {
		t.Fatalf("trust view %+v", v)
	}
	checkServiceTrust(t, v)
}

// checkServiceTrust checks what the trust view says of each demo service.
func checkServiceTrust(t *testing.T, v delivery.TrustView) {
	t.Helper()
	by := map[string]delivery.ServiceTrust{}
	for _, s := range v.Services {
		by[s.Service.Name] = s
	}
	if _, ok := by["fraud"]; ok {
		t.Fatal("fraud reads a Flux OCIRepository, so it has no registry watch")
	}
	if o := by["orders"]; len(o.Pins) != 1 || o.Pins[0].Approver != "bob@example.com" || o.Verdicts[0].Source != delivery.PublisherSourcePin {
		t.Fatalf("orders %+v", o)
	}
	if a := by["audit-log"]; len(a.Pending) != 1 || a.Pending[0].Operation == "" || a.Verdicts[0].Verdict != delivery.VerdictNewSigner {
		t.Fatalf("audit-log %+v", a)
	}
	if p := by["payments"]; len(p.Verdicts) == 0 || slices.ContainsFunc(p.Verdicts, func(v delivery.PublisherVerdict) bool {
		return v.Verdict != delivery.VerdictAccepted || v.Source != delivery.PublisherSourceRule
	}) {
		t.Fatalf("payments %+v", p)
	}
}

// TestDemoEntryStagesReadRegistry: only the watch writes status.publisher,
// so every Stage with one reads candidates.registry, and exactly one entry
// Stage keeps a Flux OCIRepository.
func TestDemoEntryStagesReadRegistry(t *testing.T) {
	d, _ := demoServer(t)
	stages, _ := d.Stages(t.Context(), "")
	var flux []string
	for _, st := range stages {
		c := st.Spec.Candidates
		if st.Status.Publisher != nil && c.Registry == nil {
			t.Errorf("%s/%s has a status.publisher but no registry watch", st.Metadata.Namespace, st.Metadata.Name)
		}
		if c.Registry != nil && st.Status.Publisher == nil {
			t.Errorf("%s/%s watches the registry and was never judged", st.Metadata.Namespace, st.Metadata.Name)
		}
		if c.OCIRepositoryRef != nil {
			flux = append(flux, st.Metadata.Namespace+"/"+st.Metadata.Name)
		}
	}
	if !slices.Equal(flux, []string{"test/fraud"}) {
		t.Fatalf("Flux-fed Stages %v, want test/fraud", flux)
	}
}
