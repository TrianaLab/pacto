package fleet

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// historyQuery is a query over one service "svc" whose revision i (version
// versions[i]) declares envs[i]; a nil entry is a revision whose contract
// was not read.
func historyQuery(versions []string, envs [][]contract.Environment) (*Query, []*ContractRevision) {
	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{}, Services: map[ServiceKey]*ServiceRecord{}}
	svc := &ServiceRecord{Key: "svc", Name: "svc"}
	var revs []*ContractRevision
	for i, v := range versions {
		k := RevisionKey("svc@" + v)
		r := &ContractRevision{Key: k, Service: "svc", ServiceKey: "svc", Version: v}
		if envs[i] != nil {
			r.Contract = &contract.Contract{Environments: envs[i]}
		}
		snap.Revisions[k] = r
		svc.Revisions = append(svc.Revisions, k)
		revs = append(revs, cloneRevision(r))
	}
	snap.Services["svc"] = svc
	return NewQuery(snap), revs
}

func env(name, file string) contract.Environment { return contract.Environment{Name: name, File: file} }

// historyText is each event as "<version> <change> <env> <fields> <previous version>".
func historyText(h EnvironmentHistoryPreview) []string {
	out := make([]string, len(h.Items))
	for i, e := range h.Items {
		prev := "-"
		if e.Previous != nil {
			prev = e.Previous.Version
		}
		out[i] = strings.Join([]string{e.Revision.Version, e.Change, e.Environment, strings.Join(e.Fields, ","), prev}, " ")
	}
	return out
}

func TestEnvironmentHistory(t *testing.T) {
	owned := env("preview", "p.yaml")
	owned.Owner = &contract.Owner{Team: "previews"}
	owned.Lifetime = contract.EnvironmentTemporary
	q, revs := historyQuery([]string{"v1.0.0", "v1.1.0", "v1.2.0"}, [][]contract.Environment{
		{env("staging", "s.yaml"), env("preview", "p.yaml")},
		{env("staging", "s2.yaml"), owned, env("prod", "prod.yaml")},
		{owned, env("prod", "prod.yaml")},
	})
	h := q.environmentHistory(revs, false)
	want := []string{
		"v1.2.0 removed staging  v1.1.0",
		"v1.1.0 changed preview lifetime,owner v1.0.0",
		"v1.1.0 added prod  v1.0.0",
		"v1.1.0 changed staging file v1.0.0",
		"v1.0.0 present preview  -",
		"v1.0.0 present staging  -",
	}
	if got := historyText(h); !slices.Equal(got, want) {
		t.Errorf("history:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if h.Total != 6 || h.Count != 6 || h.Truncated || h.Gaps != 0 || h.Completeness != "complete" || h.CompletenessReason != "" {
		t.Errorf("counts: %+v", h)
	}
	if h.Items[0].Revision.Kind != KindRevision || h.Items[0].Revision.Key != "svc@v1.2.0" {
		t.Errorf("revision ref = %+v", h.Items[0].Revision)
	}
	if l := h.Lines(); l[1] != "  svc v1.1.0  changed preview (lifetime, owner)  time not recorded" || l[0] != "  svc v1.2.0  removed staging  time not recorded" {
		t.Errorf("lines = %q", l)
	}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	for _, timeKey := range []string{`"at"`, `"time"`, `"createdAt"`, `"date"`} {
		if strings.Contains(string(b), timeKey) {
			t.Errorf("history JSON carries a time %s: %s", timeKey, b)
		}
	}
}

// TestEnvironmentHistoryOldestIsPresentNotAdded: older revisions may not be in
// the answer, so the oldest one held never reads added.
func TestEnvironmentHistoryOldestIsPresentNotAdded(t *testing.T) {
	q, revs := historyQuery([]string{"v2.0.0"}, [][]contract.Environment{{env("prod", "p.yaml")}})
	h := q.environmentHistory(revs, false)
	if got := historyText(h); !slices.Equal(got, []string{"v2.0.0 present prod  -"}) {
		t.Errorf("history = %q", got)
	}
	if l := h.Lines(); len(l) != 1 || !strings.Contains(l[0], "declared since at least svc v2.0.0") || strings.Contains(l[0], "added") {
		t.Errorf("lines = %q", l)
	}
}

// TestEnvironmentHistoryRestrictedTemplateViewPartial: a golden path visible
// only as one keeps only its template revisions, so the history is partial
// and names nothing dropped.
func TestEnvironmentHistoryRestrictedTemplateViewPartial(t *testing.T) {
	q, _ := historyQuery([]string{"v1.0.0", "v2.0.0"}, [][]contract.Environment{{env("dev", "d.yaml")}, {env("dev", "d.yaml")}})
	q.snap.Services["svc"].Template = true
	q.snap.Revisions["svc@v2.0.0"].Contract.Service.Template = &contract.Template{}
	if full := q.serviceEnvironmentHistory(q.snap.Services["svc"]); full.Completeness != "complete" {
		t.Fatalf("unrestricted: %+v", full)
	}
	r := q.Restrict(func(string) bool { return false })
	s := r.snap.Services["svc"]
	if len(s.Revisions) != 1 {
		t.Fatalf("restricted revisions = %v", s.Revisions)
	}
	h := r.serviceEnvironmentHistory(s)
	if h.Completeness != "partial" || h.CompletenessReason == "" || strings.Contains(h.CompletenessReason, "v1.0.0") {
		t.Errorf("restricted: %+v", h)
	}
	if got := historyText(h); !slices.Equal(got, []string{"v2.0.0 present dev  -"}) {
		t.Errorf("history = %q", got)
	}
	if l := h.Heading(); !strings.Contains(l, "partial: "+h.CompletenessReason) {
		t.Errorf("heading = %q", l)
	}
	q.snap.Sources = []SourceState{{ID: "oci", Status: SourcePartial}}
	if h := q.serviceEnvironmentHistory(q.snap.Services["svc"]); h.Completeness != "partial" {
		t.Errorf("a partial source: %+v", h)
	}
}

// TestEnvironmentHistoryHotfixOrderNoted: a hotfix published after a newer
// minor sits between its neighbours, because the order is the version order.
func TestEnvironmentHistoryHotfixOrderNoted(t *testing.T) {
	q, revs := historyQuery([]string{"v1.2.0", "v1.1.1", "v1.1.0"}, [][]contract.Environment{
		{env("a", "a.yaml"), env("c", "c.yaml")}, {env("a", "a.yaml"), env("b", "b.yaml")}, {env("a", "a.yaml")},
	})
	want := []string{"v1.2.0 removed b  v1.1.1", "v1.2.0 added c  v1.1.1", "v1.1.1 added b  v1.1.0", "v1.1.0 present a  -"}
	if got := historyText(q.environmentHistory(revs, false)); !slices.Equal(got, want) {
		t.Errorf("history = %q", got)
	}
	f, _ := reflect.TypeFor[EnvironmentHistoryPreview]().FieldByName("Items")
	if !strings.Contains(f.Tag.Get("doc"), "version order, not a timeline") {
		t.Errorf("items doc = %q", f.Tag.Get("doc"))
	}
	if h := (EnvironmentHistoryPreview{Total: 1, Count: 1}).Heading(); !strings.Contains(h, "version order") {
		t.Errorf("heading = %q", h)
	}
}

// TestEnvironmentHistoryGap: an unread revision is counted and no change is
// reported across it.
func TestEnvironmentHistoryGap(t *testing.T) {
	q, revs := historyQuery([]string{"v1.0.0", "v1.1.0", "v1.2.0"}, [][]contract.Environment{
		{env("a", "a.yaml")}, nil, {env("b", "b.yaml")},
	})
	h := q.environmentHistory(revs, false)
	if got := historyText(h); h.Gaps != 1 || !slices.Equal(got, []string{"v1.0.0 present a  -"}) {
		t.Errorf("gaps %d history %q", h.Gaps, got)
	}
	if l := h.Heading(); !strings.Contains(l, "1 revision(s) unread") {
		t.Errorf("heading = %q", l)
	}
	q, revs = historyQuery([]string{"v1.0.0", "v1.1.0"}, [][]contract.Environment{nil, {env("b", "b.yaml")}})
	if got := historyText(q.environmentHistory(revs, false)); !slices.Equal(got, []string{"v1.1.0 present b  -"}) {
		t.Errorf("oldest unread: %q", got)
	}
}

func TestEnvironmentHistoryBounded(t *testing.T) {
	var many []contract.Environment
	for i := range MaxDetailPreview + 1 {
		many = append(many, env(fmt.Sprintf("e%d", i), "x.yaml"))
	}
	q, revs := historyQuery([]string{"v1.0.0"}, [][]contract.Environment{many})
	h := q.environmentHistory(revs, false)
	if h.Total != MaxDetailPreview+1 || h.Count != MaxDetailPreview || !h.Truncated || len(h.Items) != MaxDetailPreview {
		t.Errorf("bounds: %d %d %v", h.Total, h.Count, h.Truncated)
	}
	if !strings.Contains(h.Heading(), fmt.Sprintf("%d of %d", MaxDetailPreview, MaxDetailPreview+1)) {
		t.Errorf("heading = %q", h.Heading())
	}
	if empty := q.environmentHistory(nil, false); empty.Items == nil || empty.Total != 0 || len(empty.Lines()) != 0 {
		t.Errorf("empty = %+v", empty)
	}
}

// TestEnvironmentHistoryRestricted: a revision the view hides contributes
// nothing; the service detail reads the restricted revisions.
func TestEnvironmentHistoryRestricted(t *testing.T) {
	q, _ := historyQuery([]string{"v1.0.0", "v2.0.0"}, [][]contract.Environment{{env("secret", "s.yaml")}, {env("dev", "d.yaml")}})
	q.snap.Services["svc"].Template = true
	q.snap.Revisions["svc@v2.0.0"].Contract.Service.Template = &contract.Template{}
	r := q.Restrict(func(string) bool { return false })
	d, err := r.EntityDetail(KindService, "svc")
	if err != nil {
		t.Fatal(err)
	}
	h := d.Service.EnvironmentHistory
	b, _ := json.Marshal(h)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "v1.0.0") || h.Total != 1 {
		t.Errorf("a hidden revision shaped the history: %s", b)
	}
}
