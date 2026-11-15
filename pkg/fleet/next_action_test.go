package fleet

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// editCodes are the attention codes whose next step is a contract edit.
// SUCCESSOR_OUT_OF_RANGE and a past-sunset item carry it only when their next
// step widens a range; an unowned service whose contracts cannot be read gets
// none (unknown is never positive).
var editCodes = map[string]bool{
	"DEPENDENCY_UNDECLARED": true, "UNOWNED_SERVICE": true, "INVALID_CONTRACT": true,
	"MISSING_READINESS": true, LimitationRangeUnsatisfied: true, "GOLDEN_PATH_DEPLOYED": true,
	CodeSuccessorOutOfRange: true,
}

func allAttention(t *testing.T) []AttentionItem {
	t.Helper()
	var out []AttentionItem
	for _, q := range []*Query{productFleet(t), rangeFleet(t), undeclaredFleet(t), domainFleet(t), deployedTemplateFleet(t), lifecycleRangeFleet(t)} {
		out = append(out, attentionOf(t, q, AttentionFilter{}).Items...)
	}
	return out
}

func wantsEditContract(it AttentionItem) bool {
	switch it.Code {
	case "UNOWNED_SERVICE":
		return !strings.Contains(it.Summary, "Owner unknown")
	case "DEPENDENCY_SERVICE_SUNSET":
		return strings.HasPrefix(it.NextStep, "Widen the range")
	}
	return editCodes[it.Code]
}

func TestAttentionNextActionEditContract(t *testing.T) {
	seen := map[string]bool{}
	for _, it := range allAttention(t) {
		want := wantsEditContract(it)
		got := it.NextAction != nil
		if want != got {
			t.Errorf("%s %q: nextAction %+v, want edit-contract %v", it.Code, it.Label, it.NextAction, want)
			continue
		}
		if got {
			seen[it.Code] = true
			if *it.NextAction != (NextAction{Verb: "edit-contract", Tool: "pacto_edit"}) {
				t.Errorf("%s: nextAction %+v", it.Code, it.NextAction)
			}
		}
	}
	for code := range editCodes {
		if !seen[code] {
			t.Errorf("no fixture produces %s with an edit-contract action", code)
		}
	}
}

func TestAttentionNoActionOnNonCompliant(t *testing.T) {
	n := 0
	for _, it := range allAttention(t) {
		if it.Code == "NON_COMPLIANT" {
			n++
			if it.NextAction != nil {
				t.Errorf("NON_COMPLIANT %q carries %+v", it.Label, it.NextAction)
			}
		}
	}
	if n == 0 {
		t.Fatal("no NON_COMPLIANT item in the fixtures")
	}
}

func TestAttentionNoActionOnUnknownOrStale(t *testing.T) {
	seen := map[string]bool{}
	for _, it := range allAttention(t) {
		switch it.Category {
		case categoryUnknown, categoryStale, categoryUnresolved:
			seen[it.Category] = true
			if it.NextAction != nil {
				t.Errorf("%s %q carries %+v", it.Code, it.Label, it.NextAction)
			}
		}
	}
	for _, c := range []string{categoryUnknown, categoryStale, categoryUnresolved} {
		if !seen[c] {
			t.Errorf("no %s item in the fixtures", c)
		}
	}
	// A service with no readable contract raises a knowledge item, never an
	// unowned one, and neither carries an action.
	q := NewQuery(&FleetSnapshot{
		Services:  map[ServiceKey]*ServiceRecord{"unread": {Key: "unread", Name: "unread", Revisions: []RevisionKey{"u@1"}}},
		Revisions: map[RevisionKey]*ContractRevision{"u@1": {Key: "u@1", Service: "unread", ServiceKey: "unread"}},
	})
	if l := attentionOf(t, q, AttentionFilter{Category: categoryUnowned}); len(l.Items) != 0 {
		t.Errorf("unreadable service raises unowned items %+v", l.Items)
	}
	k := attentionOf(t, q, AttentionFilter{Category: categoryKnowledge}).Items
	if len(k) == 0 {
		t.Error("unreadable service raises no knowledge item")
	}
	for _, it := range k {
		if it.NextAction != nil {
			t.Errorf("unreadable service's knowledge item carries %+v", it.NextAction)
		}
	}
}

func TestNextActionMatchesNextStep(t *testing.T) {
	for _, it := range allAttention(t) {
		if it.NextAction == nil {
			continue
		}
		if !strings.Contains(it.NextStep, "contract") && !strings.Contains(it.NextStep, "readiness assessment") {
			t.Errorf("%s: edit-contract with next step %q", it.Code, it.NextStep)
		}
	}
}

func TestNextStepNamesNoDeliveryAction(t *testing.T) {
	for _, it := range allAttention(t) {
		s := strings.ToLower(it.NextStep)
		if strings.Contains(s, "rollback") || strings.Contains(s, "roll back") || strings.Contains(s, "plan a") {
			t.Errorf("%s: next step %q names a delivery action", it.Code, it.NextStep)
		}
	}
}

// TestAttentionActionNarrowedWithItem: a restricted view hides the item and
// with it its action; the visible items keep theirs.
func TestAttentionActionNarrowedWithItem(t *testing.T) {
	q := undeclaredFleet(t)
	all := attentionOf(t, q, AttentionFilter{})
	hiddenLabel := "a calls c"
	if !slices.ContainsFunc(all.Items, func(it AttentionItem) bool { return it.Label == hiddenLabel && it.NextAction != nil }) {
		t.Fatalf("fixture lacks %q with an action: %+v", hiddenLabel, all.Items)
	}
	r := attentionOf(t, q.Restrict(func(ns string) bool { return ns != "secret" }), AttentionFilter{})
	if r.Total != len(r.Items) {
		t.Errorf("restricted total %d, items %d", r.Total, len(r.Items))
	}
	for _, it := range r.Items {
		if it.Label == hiddenLabel || it.Service == "c" {
			t.Errorf("restricted view shows %+v", it)
		}
		if it.NextAction != nil && !wantsEditContract(it) {
			t.Errorf("restricted %s carries %+v", it.Code, it.NextAction)
		}
	}
}

// TestAttentionPreviewCarriesNextAction: the overview and owner detail
// previews carry the same action as the list.
func TestAttentionPreviewCarriesNextAction(t *testing.T) {
	q := undeclaredFleet(t)
	list := attentionOf(t, q, AttentionFilter{})
	byLabel := map[string]*NextAction{}
	for _, it := range list.Items {
		byLabel[it.Label] = it.NextAction
	}
	od, err := q.EntityDetail(KindOwner, "team:t")
	if err != nil {
		t.Fatal(err)
	}
	ov := q.Overview()
	for name, items := range map[string][]AttentionItem{"owner detail": od.Owner.Attention.Items, "overview": ov.Attention.Items} {
		n := 0
		for _, it := range items {
			want, ok := byLabel[it.Label]
			if !ok {
				continue
			}
			if want != nil {
				n++
			}
			if !reflect.DeepEqual(it.NextAction, want) {
				t.Errorf("%s %q: %+v, list %+v", name, it.Label, it.NextAction, want)
			}
		}
		if n == 0 {
			t.Errorf("%s preview shares no item with an action with the list", name)
		}
	}
}

// TestAttentionItemJSONKeepsFields: every existing key keeps its name and the
// new one is additive and omitted when empty.
func TestAttentionItemJSONKeepsFields(t *testing.T) {
	it := AttentionItem{Entity: EntityRef{Kind: KindService, Key: "s"}, Service: "s", Label: "l", Severity: severityInfo,
		Code: "c", Category: categoryUnowned, Summary: "x", Reason: "y", Source: "z", NextStep: "n"}
	b, _ := json.Marshal(it)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"entity", "service", "label", "severity", "code", "category", "summary", "reason", "source", "nextStep"} {
		if _, ok := m[k]; !ok {
			t.Errorf("key %q missing from %s", k, b)
		}
	}
	if _, ok := m["nextAction"]; ok {
		t.Errorf("empty nextAction is emitted: %s", b)
	}
	it.NextAction = editContract()
	b, _ = json.Marshal(it)
	if !strings.Contains(string(b), `"nextAction":{"verb":"edit-contract","tool":"pacto_edit"}`) {
		t.Errorf("nextAction = %s", b)
	}
}
