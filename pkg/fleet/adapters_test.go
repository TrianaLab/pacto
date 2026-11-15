package fleet

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// reporter is one current report on a status, as provisioningStatus keeps it.
func reporter(slug, producer string, verified bool, at time.Time) provisioningReporter {
	return provisioningReporter{Provider: slug, Producer: producer, Verified: verified, ObservedAt: at, Source: "s", SourceKind: "evidence-http"}
}

// reportedStatus is a status in env with phase, read from reps: current
// Observed reports of phase unless a reporter says otherwise.
func reportedStatus(env, phase string, reps ...provisioningReporter) ProvisioningStatus {
	for i := range reps {
		if reps[i].Outcome == "" {
			reps[i].Outcome, reps[i].Phase, reps[i].Current = "Observed", phase, true
		}
	}
	st := ProvisioningStatus{Environment: env, Phase: phase, Provenance: ProvenanceObserved, reporters: reps}
	if len(reps) > 0 {
		at := reps[0].ObservedAt
		st.AsOf = &at
	}
	return st
}

// adapterLine is the part of an adapter the tests compare.
func adapterLine(a OfferingAdapter) string {
	last := ""
	if a.LastReportedAt != nil {
		last = a.LastReportedAt.Format(time.RFC3339)
	}
	return fmt.Sprintf("%s/%s verified=%v envs=%v ready=%d unsigned=%d unknown=%d failed=%d disagrees=%d last=%s %s",
		a.Provider, a.Producer, a.Verified, a.Environments, a.Phases.Ready, a.Phases.ReadyUnsigned, a.Phases.Unknown, a.Phases.Failed, a.Disagrees, last, a.Provenance)
}

func adapterLines(p OfferingAdaptersPreview) []string {
	var out []string
	for _, a := range p.Items {
		out = append(out, adapterLine(a))
	}
	return out
}

func TestOfferingAdaptersGroupsBySlugProducerAndSignature(t *testing.T) {
	at := fixedNow()
	got := offeringAdapters([]OfferingUse{{Provisioning: []ProvisioningStatus{
		reportedStatus("production", "Ready", reporter("scanner", "security-adapter", false, at)),
		reportedStatus("staging", "Ready", reporter("scanner", "security-adapter", true, at)),
	}}})
	ts := at.Format(time.RFC3339)
	want := []string{
		"scanner/security-adapter verified=true envs=[staging] ready=1 unsigned=0 unknown=0 failed=0 disagrees=0 last=" + ts + " observed",
		"scanner/security-adapter verified=false envs=[production] ready=1 unsigned=1 unknown=0 failed=0 disagrees=0 last=" + ts + " observed",
	}
	if l := adapterLines(got); !slices.Equal(l, want) || got.Total != 2 || got.Count != 2 || got.Truncated {
		t.Errorf("adapters:\n got %q\nwant %q (%+v)", l, want, got)
	}
}

func TestOfferingAdaptersEnvironmentsAndPhases(t *testing.T) {
	newer, older := fixedNow(), fixedNow().Add(-48*time.Hour)
	got := offeringAdapters([]OfferingUse{
		{Provisioning: []ProvisioningStatus{reportedStatus("staging", "Unknown", reporter("scanner", "a", true, older))}},
		{Provisioning: []ProvisioningStatus{reportedStatus("production", "Ready", reporter("scanner", "a", true, newer)), reportedStatus("", "Ready", reporter("scanner", "a", true, older))}},
	})
	want := []string{"scanner/a verified=true envs=[default production staging] ready=2 unsigned=0 unknown=1 failed=0 disagrees=0 last=" + newer.Format(time.RFC3339) + " observed"}
	if l := adapterLines(got); !slices.Equal(l, want) {
		t.Errorf("adapters:\n got %q\nwant %q", l, want)
	}
}

func TestOfferingAdaptersUnattributed(t *testing.T) {
	got := offeringAdapters([]OfferingUse{{Provisioning: []ProvisioningStatus{
		reportedStatus("production", "Ready", reporter("scanner", "a", true, fixedNow())),
		{Environment: "staging", Phase: StatusUnknown, Reason: noProvisioningReport},
	}}})
	if got.Unattributed != 1 || got.Conflicting != 0 || got.Total != 1 {
		t.Errorf("adapters = %+v", got)
	}
}

func TestOfferingAdaptersEmptyIsUnknown(t *testing.T) {
	none := ProvisioningStatus{Phase: StatusUnknown, Reason: noProvisioningReport}
	got := offeringAdapters([]OfferingUse{{Provisioning: []ProvisioningStatus{none, none}}, {Provisioning: []ProvisioningStatus{none}}, {}})
	if got.Items == nil || len(got.Items) != 0 || got.Total != 0 || got.Unattributed != 3 {
		t.Errorf("adapters = %+v", got)
	}
	data, err := json.Marshal(got)
	if err != nil || !strings.Contains(string(data), `"items":[]`) {
		t.Errorf("json %s %v", data, err)
	}
}

func TestOfferingAdaptersBounded(t *testing.T) {
	var sts []ProvisioningStatus
	for i := range MaxOfferingAdapters + 2 {
		sts = append(sts, reportedStatus("production", "Ready", reporter(fmt.Sprintf("a%02d", i), "p", true, fixedNow())))
	}
	got := offeringAdapters([]OfferingUse{{Provisioning: sts}})
	if got.Total != 12 || got.Count != 10 || len(got.Items) != 10 || !got.Truncated || got.Items[9].Provider != "a09" {
		t.Errorf("adapters = %+v", got)
	}
}

// One adapter listed twice on one slot (two sources, same slug, producer and
// signature) counts that slot once.
func TestOfferingAdaptersOneSlotOncePerAdapter(t *testing.T) {
	at := fixedNow()
	got := offeringAdapters([]OfferingUse{{Provisioning: []ProvisioningStatus{
		reportedStatus("production", "Ready", reporter("scanner", "a", true, at.Add(-time.Hour)), reporter("scanner", "a", true, at)),
	}}})
	if len(got.Items) != 1 || got.Items[0].Phases.Ready != 1 || !got.Items[0].LastReportedAt.Equal(at) {
		t.Errorf("adapters = %+v", got)
	}
}

// postgresAdapters is the adapters of the postgresql offering over the
// orders fleet and the reports, one source per slice.
func postgresAdapters(t *testing.T, reports ...[]RawProvisioning) OfferingAdaptersPreview {
	t.Helper()
	d, err := NewQuery(provisioningSnap(t, 0, ordersFleet(), reports...)).EntityDetail(KindOffering, "postgresql")
	if err != nil {
		t.Fatal(err)
	}
	return d.Offering.Adapters
}

func TestOfferingAdaptersListsAgreeingReporters(t *testing.T) {
	older := fixedNow().Add(-2 * time.Hour)
	scanner := func(r *RawProvisioning) { r.Provider, r.Producer = "scanner", "security-adapter" }
	got := postgresAdapters(t, []RawProvisioning{report(scanner)},
		[]RawProvisioning{report(scanner, func(r *RawProvisioning) { r.ObservedAt, r.AcceptedAt, r.Verified = older, nil, false })})
	l := adapterLines(got)
	if len(l) != 2 || !strings.HasPrefix(l[0], "scanner/security-adapter verified=true envs=[production] ready=1 unsigned=0") ||
		!strings.HasPrefix(l[1], "scanner/security-adapter verified=false envs=[production] ready=1 unsigned=1") ||
		got.Conflicting != 0 || got.Unattributed != 1 { // the dependency use nobody reported on
		t.Errorf("adapters %q (%+v)", l, got)
	}
}

func TestOfferingAdaptersConflictNamesReporters(t *testing.T) {
	got := postgresAdapters(t, []RawProvisioning{report()}, []RawProvisioning{report(func(r *RawProvisioning) { r.Phase, r.Provider, r.Producer = "Failed", "other", "other-adapter" })})
	l := adapterLines(got)
	if len(l) != 2 || !strings.HasPrefix(l[0], "aws/adapter verified=true envs=[production] ready=0 unsigned=0 unknown=1 failed=0 disagrees=1") ||
		!strings.HasPrefix(l[1], "other/other-adapter verified=true envs=[production] ready=0 unsigned=0 unknown=1 failed=0 disagrees=1") ||
		got.Conflicting != 1 || got.Unattributed != 1 {
		t.Errorf("adapters %q (%+v)", l, got)
	}
}

func TestOfferingAdaptersEmptySlug(t *testing.T) {
	got := postgresAdapters(t, []RawProvisioning{report(), report(func(r *RawProvisioning) {
		r.Outcome, r.Phase, r.Provider, r.ExternalRef, r.Producer = "Insufficient", "", "", "", "zz-adapter"
	})})
	if len(got.Items) != 2 || got.Items[0].Provider != "aws" || got.Items[1].Provider != "" || got.Items[1].Producer != "zz-adapter" {
		t.Errorf("adapters %q", adapterLines(got))
	}
	// A named unsigned adapter still sorts after an empty signed one.
	at := fixedNow()
	sorted := offeringAdapters([]OfferingUse{{Provisioning: []ProvisioningStatus{reportedStatus("p", "Ready",
		reporter("b", "x", false, at), reporter("", "x", true, at), reporter("a", "y", true, at), reporter("a", "x", true, at))}}})
	var order []string
	for _, a := range sorted.Items {
		order = append(order, a.Provider+"/"+a.Producer)
	}
	if want := []string{"a/x", "a/y", "/x", "b/x"}; !slices.Equal(order, want) {
		t.Errorf("order %q, want %q", order, want)
	}
}

// A signed adapter whose own report failed is never credited the Ready a newer
// unsigned file reported on the same request.
func TestOfferingAdaptersCreditsOnlyItsOwnPhase(t *testing.T) {
	got := postgresAdapters(t,
		[]RawProvisioning{report(func(r *RawProvisioning) {
			r.Outcome, r.Phase, r.Provider, r.ExternalRef, r.Producer = "Failed", "", "", "", "security-adapter"
			r.ObservedAt, r.AcceptedAt = fixedNow().Add(-2*time.Minute), nil
		})},
		[]RawProvisioning{report(func(r *RawProvisioning) {
			r.Producer, r.Verified, r.ObservedAt, r.AcceptedAt = "file", false, fixedNow().Add(-time.Minute), nil
		})})
	l := adapterLines(got)
	if len(l) != 2 || !strings.HasPrefix(l[0], "/security-adapter verified=true envs=[production] ready=0 unsigned=0 unknown=1") ||
		!strings.HasPrefix(l[1], "aws/file verified=false envs=[production] ready=1 unsigned=1 unknown=0") {
		t.Errorf("adapters %q", l)
	}
}

// A slot with no current report lists its newest reporter with Unknown, never
// the phase its stale report said.
func TestOfferingAdaptersStaleReporterIsUnknown(t *testing.T) {
	got := postgresAdapters(t, []RawProvisioning{report(func(r *RawProvisioning) {
		r.ObservedAt, r.AcceptedAt = fixedNow().Add(-30*24*time.Hour), nil
	})})
	l := adapterLines(got)
	if len(l) != 1 || !strings.HasPrefix(l[0], "aws/adapter verified=true envs=[production] ready=0 unsigned=0 unknown=1 failed=0 disagrees=0") {
		t.Errorf("adapters %q", l)
	}
}

// An adapter that took no part in a disagreement is not counted as disagreeing.
func TestOfferingAdaptersDisagreesOnlyWithAPhase(t *testing.T) {
	at := fixedNow()
	st := reportedStatus("production", StatusUnknown, reporter("a", "x", true, at), reporter("b", "y", true, at), reporter("c", "z", true, at))
	st.Conflict = []string{"Failed", "Ready"}
	st.reporters[0].Phase, st.reporters[1].Phase = "Ready", "Failed"
	st.reporters[2].Outcome, st.reporters[2].Phase = "Insufficient", ""
	got := offeringAdapters([]OfferingUse{{Provisioning: []ProvisioningStatus{st}}})
	var dis []int
	for _, a := range got.Items {
		dis = append(dis, a.Disagrees)
	}
	if !slices.Equal(dis, []int{1, 1, 0}) || got.Conflicting != 1 {
		t.Errorf("disagrees %v (%+v)", dis, got)
	}
}

func TestOfferingAdaptersLines(t *testing.T) {
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.FixedZone("x", 3600))
	p := OfferingAdaptersPreview{Total: 3, Count: 2, Truncated: true, Conflicting: 1, Unattributed: 2, Items: []OfferingAdapter{
		{Provider: "scanner", Producer: "security-adapter", Verified: true, Environments: []string{"production"}, Phases: PhaseTally{Ready: 1}, LastReportedAt: &at},
		{Environments: []string{"default", "staging"}, Phases: PhaseTally{Ready: 1, ReadyUnsigned: 1, Unknown: 1}, Disagrees: 1},
	}}
	want := []string{
		"scanner by security-adapter, signed: production; 1 ready; last reported 2026-11-01T09:00:00Z",
		"unnamed adapter, unsigned target-state file: default, staging; 1 ready (1 unsigned), 1 unknown, disagrees on 1",
		"1 more", "1 requests where adapters disagree, so their status is Unknown", "2 requests no adapter reports on",
	}
	if got := p.Lines(0); !slices.Equal(got, want) {
		t.Errorf("lines =\n%q\nwant\n%q", got, want)
	}
	if got := (OfferingAdaptersPreview{}).Lines(0); !slices.Equal(got, []string{"No adapter has reported. Provisioning status is Unknown."}) {
		t.Errorf("empty = %q", got)
	}
	// A view that hides consumers speaks only for the requests it shows.
	if got := (OfferingAdaptersPreview{}).Lines(1); !slices.Equal(got, []string{NoAdapterVisible}) {
		t.Errorf("empty with hidden consumers = %q", got)
	}
}
