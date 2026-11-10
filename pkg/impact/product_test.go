package impact

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func validDigest(fill string) string {
	return "sha256:" + strings.Repeat(fill, 64/len(fill)+1)[:64]
}

func TestImmutableRef(t *testing.T) {
	d1 := validDigest("a")
	d2 := validDigest("b")
	// A canonical digest-pinned ResolvedRef consistent with the content digest is exact.
	if ref, err := ImmutableRef(&fleet.ContractRevision{ResolvedRef: "oci://x/a@" + d1, Digest: d1}); err != nil || ref != "oci://x/a@"+d1 {
		t.Errorf("digest-pinned ref must be exact: %q %v", ref, err)
	}
	// A canonical digest-pinned ref with no recorded digest is still exact (nothing to contradict).
	if _, err := ImmutableRef(&fleet.ContractRevision{ResolvedRef: "oci://x/a@" + d1}); err != nil {
		t.Errorf("digest-pinned ref without a recorded digest must be exact: %v", err)
	}
	// The dashboard bug: a digest-pinned ref with the oci:// scheme STRIPPED is a
	// local path to the resolver and must be rejected, not silently accepted.
	if _, err := ImmutableRef(&fleet.ContractRevision{ResolvedRef: "x/a@" + d1, Digest: d1}); err == nil {
		t.Error("a scheme-less digest ref must be rejected (it resolves as a local path)")
	}
	// A mutable tag ResolvedRef must NOT be accepted as exact merely for being non-empty.
	if _, err := ImmutableRef(&fleet.ContractRevision{ResolvedRef: "oci://x/a:1.0", Digest: d1}); err == nil {
		t.Error("a tag ResolvedRef must be rejected as non-exact")
	}
	// A short/invalid digest body must be rejected even with the oci:// scheme.
	if _, err := ImmutableRef(&fleet.ContractRevision{ResolvedRef: "oci://x/a@sha256:abc", Digest: "sha256:abc"}); err == nil {
		t.Error("a malformed digest body must be rejected")
	}
	// A local path is mutable.
	if _, err := ImmutableRef(&fleet.ContractRevision{RequestedRef: "file:///abs"}); err == nil {
		t.Error("a local-path revision must be rejected")
	}
	// A revision with no ref at all is rejected.
	if _, err := ImmutableRef(&fleet.ContractRevision{}); err == nil {
		t.Error("a revision with no ref must error")
	}
	// An inconsistent digest-pinned ref (valid ref digest != valid content digest) is rejected.
	if _, err := ImmutableRef(&fleet.ContractRevision{ResolvedRef: "oci://x/a@" + d1, Digest: d2}); err == nil {
		t.Error("an inconsistent digest/ref must be rejected")
	}

	// The revision label comes from the record (service + version), not the key.
	l := revisionRef(&fleet.ContractRevision{Key: "svc@d", Service: "svc", Version: "1.2.3", Digest: "d"})
	if l == nil || l.Label != "svc 1.2.3" || l.Version != "1.2.3" {
		t.Errorf("revision link must be a record label: %+v", l)
	}
}

// TestImmutableRef_ExactMatchButNonRetrievable proves that Product Impact
// by canonical identity rejects non-retrievable content EVEN WHEN a target's revision
// match is exact. The two dimensions are independent -- a runtime target carrying a
// trusted content digest with no canonical ref is an EXACT revision match (see
// fleet.TestMatchRevision_RecordedDigestNoRef_Exact and
// fleet.TestTargetIdentity_ExactMatch_NonRetrievable), yet its content cannot be
// retrieved through a canonical ref, so canonical Product Impact must refuse it.
func TestImmutableRef_ExactMatchButNonRetrievable(t *testing.T) {
	dA := validDigest("a")
	// A revision known only by its recorded content digest (no canonical ResolvedRef):
	// exactly the shape a k8s-observed target matches EXACTLY.
	rev := &fleet.ContractRevision{Key: "svc@a", Service: "svc", ServiceKey: fleet.NewServiceKey("svc"), Digest: dA}
	if _, err := ImmutableRef(rev); err == nil {
		t.Error("Product Impact must reject content that is not resolver-retrievable, even when a target matches this revision exactly")
	}
	// The predicate ImmutableRef enforces (content retrievability) is false, which is
	// independent of the exact revision-match certainty a linked target would report.
	if fleet.ClassifyContentIdentity(rev.ResolvedRef, rev.Digest).Retrievable() {
		t.Error("a recorded digest with no canonical ref must not be retrievable")
	}
}

// renderChangeValue bounds EACH value: one changed OpenAPI schema must not make the
// answer unbounded, and the cut is reported rather than silent.
func TestRenderChangeValue(t *testing.T) {
	if s, cut := renderChangeValue(nil); s != "" || cut {
		t.Errorf("nil = %q/%v, want empty and uncut", s, cut)
	}
	if s, cut := renderChangeValue("plain"); s != "plain" || cut {
		t.Errorf("string = %q/%v, want passthrough", s, cut)
	}
	// A composite value renders as indented JSON, the shape a reviewer can read.
	s, cut := renderChangeValue(map[string]any{"a": 1})
	if cut || !strings.Contains(s, "\"a\": 1") {
		t.Errorf("composite = %q/%v, want indented JSON", s, cut)
	}
	// A value JSON cannot encode still renders something rather than vanishing.
	if s, _ := renderChangeValue(make(chan int)); s == "" {
		t.Error("an unmarshalable value must still render")
	}
	long, cut := renderChangeValue(strings.Repeat("x", MaxChangeValueRunes+50))
	if !cut || len([]rune(long)) != MaxChangeValueRunes {
		t.Errorf("over-long value = %d runes cut=%v, want %d runes cut", len([]rune(long)), cut, MaxChangeValueRunes)
	}
	// Truncation counts RUNES, so a multi-byte value is never cut mid-character.
	multi, cut := renderChangeValue(strings.Repeat("é", MaxChangeValueRunes+10))
	if !cut || len([]rune(multi)) != MaxChangeValueRunes {
		t.Errorf("multibyte cut = %d runes, want %d", len([]rune(multi)), MaxChangeValueRunes)
	}
}

func productSnapshot() *fleet.FleetSnapshot {
	api := fleet.NewServiceKeyDomain("acme", "api")
	return &fleet.FleetSnapshot{
		SnapshotID: "snap-1",
		Services:   map[fleet.ServiceKey]*fleet.ServiceRecord{api: {Key: api, Domain: "acme", Name: "api"}},
		Revisions: map[fleet.RevisionKey]*fleet.ContractRevision{
			"api@1": {Key: "api@1", ServiceKey: api, Service: "api", Version: "1.0.0", Domain: "acme"},
			"api@2": {Key: "api@2", ServiceKey: api, Service: "api", Domain: "acme"},
			"web@1": {Key: "web@1", ServiceKey: fleet.NewServiceKey("web"), Service: "web"},
		},
		Targets: map[fleet.TargetKey]*fleet.TargetRecord{"prod/k8s/api": {Scope: "prod", Kind: "k8s", Name: "api", Domain: "acme"}},
	}
}

func TestProductRevisions(t *testing.T) {
	snap := productSnapshot()
	key, from, to, err := ProductRevisions(snap, "", "api@1", "api@2")
	if err != nil || key != fleet.NewServiceKeyDomain("acme", "api") || from.Key != "api@1" || to.Key != "api@2" {
		t.Fatalf("ProductRevisions = %q %v %v %v", key, from, to, err)
	}
	for name, keys := range map[string][3]string{
		"unknown from":       {"", "nope", "api@2"},
		"unknown to":         {"", "api@1", "nope"},
		"different services": {"", "api@1", "web@1"},
		"service mismatch":   {"web", "api@1", "api@2"},
	} {
		if _, _, _, err := ProductRevisions(snap, keys[0], keys[1], keys[2]); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// boundedResult is a result whose every list is one past its bound.
func boundedResult() *Result {
	longPath := make([]string, MaxProductPath+3)
	for i := range longPath {
		longPath[i] = "acme/api"
	}
	res := &Result{
		Classification: "BREAKING",
		Consumers: []AffectedConsumer{
			{Service: "web", Path: longPath, Confidence: ConfidenceDeclared, CompatibilityVerdict: CompatibilityIncompatible},
			{Service: "api", Domain: "acme", Path: []string{"acme/api"}, Confidence: "novel", CompatibilityVerdict: CompatibilityCompatible, Note: "range only",
				Unmet: []contract.Mismatch{{Consumer: "api", Provider: "api", Interface: "events", Guarantee: "delivery", Required: "at-least-once"}}},
		},
		Gate:          GateUnverified,
		GateReason:    "why",
		Owners:        make([]string, MaxProductOwners+1),
		ActiveTargets: []string{"prod/k8s/api", "gone/k8s/x"},
		Limitations:   make([]fleet.Limitation, fleet.MaxMetaLimitations),
		// The analysis already capped the list; the preview must keep saying so.
		LimitationsTruncated: true,
	}
	for range MaxProductChanges + 1 {
		res.NonBreakingChanges = append(res.NonBreakingChanges, diff.Change{Path: "p", Classification: diff.NonBreaking})
	}
	return res
}

func TestBuildProduct_IdentityAndConsumers(t *testing.T) {
	snap := productSnapshot()
	res := boundedResult()
	from, to := snap.Revisions["api@1"], snap.Revisions["api@2"]
	p := BuildProduct(fleet.ProductMeta{}, snap, from.ServiceKey, from, to, res, 1, 0)

	if p.Service.Label != "api" || p.OldRevision.Label != "api 1.0.0" || p.NewRevision.Label != "api" || !p.SnapshotMatch {
		t.Errorf("identity = %+v %+v %+v", p.Service, p.OldRevision, p.NewRevision)
	}
	c := p.Consumers
	if c.Total != 2 || c.Count != 1 || !c.Truncated || c.Items[0].Service.Key != "acme/api" {
		t.Errorf("consumers page = %+v", c)
	}
	// The second consumer by key is web, whose path is cut.
	all := BuildProduct(fleet.ProductMeta{}, snap, from.ServiceKey, from, to, res, 0, 0).Consumers.Items
	if web := all[1]; !web.PathTruncated || web.PathTotal != MaxProductPath+3 || len(web.Path) != MaxProductPath || web.Service.Label != "web" {
		t.Errorf("web consumer = %+v", web)
	}
	// Tallies cover every consumer, known buckets first, an unknown value after.
	if last := c.ByConfidence[len(c.ByConfidence)-1]; last.Key != "novel" || last.Count != 1 {
		t.Errorf("confidence tally = %+v", c.ByConfidence)
	}
}

func TestBuildProduct_CarriesGateAndNote(t *testing.T) {
	snap := productSnapshot()
	from, to := snap.Revisions["api@1"], snap.Revisions["api@2"]
	p := BuildProduct(fleet.ProductMeta{}, snap, from.ServiceKey, from, to, boundedResult(), 0, 0)
	if p.Gate != GateUnverified || p.GateReason != "why" {
		t.Errorf("gate = %q %q", p.Gate, p.GateReason)
	}
	// Refused counts every consumer left with a guarantee unmet, not the page.
	if second := BuildProduct(fleet.ProductMeta{}, snap, from.ServiceKey, from, to, boundedResult(), 1, 1).Consumers; second.Refused != 1 || len(second.Items[0].Unmet) != 0 {
		t.Errorf("refused tally over a page without it = %d", second.Refused)
	}
	if p.Consumers.Items[0].Note != "range only" {
		t.Errorf("note = %q", p.Consumers.Items[0].Note)
	}
	if u := p.Consumers.Items[0].Unmet; len(u) != 1 || u[0].Guarantee != "delivery" {
		t.Errorf("unmet = %+v", u)
	}
}

func TestBuildProduct_BoundsEveryList(t *testing.T) {
	snap := productSnapshot()
	rev := snap.Revisions["api@1"]
	p := BuildProduct(fleet.ProductMeta{}, snap, rev.ServiceKey, rev, rev, boundedResult(), 0, 0)
	if !p.Owners.Truncated || p.Owners.Count != MaxProductOwners || p.Owners.Total != MaxProductOwners+1 {
		t.Errorf("owners = %+v", p.Owners)
	}
	if p.ActiveTargets.Items[0].Label != "prod/k8s/api" || p.ActiveTargets.Items[1].Label != "gone/k8s/x" {
		t.Errorf("targets = %+v", p.ActiveTargets.Items)
	}
	if !p.Limitations.Truncated || p.Limitations.Count != fleet.MaxMetaLimitations {
		t.Errorf("limitations = %+v", p.Limitations)
	}
	if !p.Changes.Truncated || p.Changes.Count != MaxProductChanges || p.Changes.Total != MaxProductChanges+1 {
		t.Errorf("changes = %+v", p.Changes)
	}
}

func TestBuildProduct_SmallAnswerIsComplete(t *testing.T) {
	snap := productSnapshot()
	rev := snap.Revisions["api@1"]
	res := &Result{BreakingChanges: []diff.Change{{Path: "a", OldValue: "x", NewValue: map[string]any{"k": 1}}}}
	p := BuildProduct(fleet.ProductMeta{}, snap, rev.ServiceKey, rev, rev, res, 0, 0)
	if p.Changes.Truncated || p.Changes.Count != 1 || p.Changes.Breaking != 1 || p.Changes.Items[0].OldValue != "x" {
		t.Errorf("changes = %+v", p.Changes)
	}
	if p.Owners.Truncated || p.Limitations.Truncated || p.Consumers.Truncated {
		t.Errorf("a small answer must not be truncated: %+v", p)
	}
}

// Regression: the preview used to re-derive Truncated from its own (equal) cap,
// so a list Analyze had already cut at fleet.MaxMetaLimitations read as complete.
func TestBuildProduct_LimitationsCarryTheAnalysisTruncation(t *testing.T) {
	snap := productSnapshot()
	rev := snap.Revisions["api@1"]
	res := &Result{Limitations: make([]fleet.Limitation, fleet.MaxMetaLimitations), LimitationsTruncated: true}
	if p := BuildProduct(fleet.ProductMeta{}, snap, rev.ServiceKey, rev, rev, res, 0, 0); !p.Limitations.Truncated {
		t.Errorf("an analysis-truncated list must stay truncated in the preview: %+v", p.Limitations)
	}
	res.LimitationsTruncated = false
	if p := BuildProduct(fleet.ProductMeta{}, snap, rev.ServiceKey, rev, rev, res, 0, 0); p.Limitations.Truncated {
		t.Errorf("a complete list must not read as truncated: %+v", p.Limitations)
	}
}

// MaxProductLimitations shipped in v4.0; it stays as an alias in a minor release.
func TestMaxProductLimitationsDeprecatedAlias(t *testing.T) {
	if MaxProductLimitations != fleet.MaxMetaLimitations {
		t.Fatalf("MaxProductLimitations = %d, want %d", MaxProductLimitations, fleet.MaxMetaLimitations)
	}
}

// Each consumer carries its declared range and whether it is one of the reasons
// the gate blocks, using the gate's own predicate.
func TestBuildProduct_CompatibilityAndBlocking(t *testing.T) {
	snap := productSnapshot()
	from, to := snap.Revisions["api@1"], snap.Revisions["api@2"]
	res := &Result{
		Classification: "BREAKING",
		Consumers: []AffectedConsumer{
			{Service: "ran", Direct: true, Compatibility: "^1.0.0", CompatibilityVerdict: CompatibilityIncompatible, Targets: []string{"prod/k8s/ran"}},
			{Service: "idle", Direct: true, Compatibility: "^1.0.0", CompatibilityVerdict: CompatibilityIncompatible},
		},
	}
	// Both are incompatible; the blocking one ranks first among the highlights, so it
	// is listed first.
	items := BuildProduct(fleet.ProductMeta{}, snap, from.ServiceKey, from, to, res, 0, 0).Consumers.Items
	if items[1].Service.Key != "idle" || items[1].Blocking || items[1].Compatibility != "^1.0.0" {
		t.Errorf("untargeted consumer = %+v", items[1])
	}
	if items[0].Service.Key != "ran" || !items[0].Blocking {
		t.Errorf("targeted incompatible consumer must block: %+v", items[0])
	}
	if active, _ := res.blockingConsumers(); len(active) != 1 || active[0] != "ran" {
		t.Errorf("gate names %v, want [ran]", active)
	}
}

// The product answer reports the analysis's completeness when it is lower than
// the snapshot's: a truncated blast radius over a complete snapshot is partial.
func TestBuildProduct_LowersCompleteness(t *testing.T) {
	snap := productSnapshot()
	rev := snap.Revisions["api@1"]
	for _, tc := range []struct{ meta, res, want fleet.Completeness }{
		{fleet.CompletenessComplete, fleet.CompletenessPartial, fleet.CompletenessPartial},
		{fleet.CompletenessComplete, fleet.CompletenessEmpty, fleet.CompletenessEmpty},
		{fleet.CompletenessPartial, fleet.CompletenessComplete, fleet.CompletenessPartial},
		{fleet.CompletenessEmpty, fleet.CompletenessPartial, fleet.CompletenessEmpty},
		{fleet.CompletenessComplete, "", fleet.CompletenessComplete},
	} {
		res := &Result{Completeness: tc.res, Limitations: []fleet.Limitation{{Code: "GRAPH_TRUNCATED"}}}
		p := BuildProduct(fleet.ProductMeta{Completeness: tc.meta}, snap, rev.ServiceKey, rev, rev, res, 0, 0)
		if p.Meta.Completeness != tc.want {
			t.Errorf("meta %q, analysis %q: completeness = %q, want %q", tc.meta, tc.res, p.Meta.Completeness, tc.want)
		}
	}
}

// Highlights rank over EVERY consumer, so a blocking consumer that sorts last by
// key leads them on every page; Direct counts across pages.
func TestBuildProduct_HighlightsAcrossPages(t *testing.T) {
	snap := productSnapshot()
	from, to := snap.Revisions["api@1"], snap.Revisions["api@2"]
	res := &Result{Classification: "BREAKING"}
	for i := range 150 {
		res.Consumers = append(res.Consumers, highlightFixtureConsumer(i))
	}
	for _, offset := range []int{0, 100} {
		page := BuildProduct(fleet.ProductMeta{}, snap, from.ServiceKey, from, to, res, 100, offset).Consumers
		h := page.Highlights
		if len(h) != MaxHighlights || page.Direct != 75 {
			t.Fatalf("offset %d: %d highlights, %d direct", offset, len(h), page.Direct)
		}
		got := [3]string{h[0].Service.Key, h[1].Service.Key, h[2].Service.Key}
		if got != [3]string{"zz-blocks", "c010", "c012"} {
			t.Errorf("offset %d: highlights lead with %v", offset, got)
		}
		if !h[0].Blocking || h[3].CompatibilityVerdict != CompatibilityUnknown || h[MaxHighlights-1].CompatibilityVerdict != CompatibilityUnknown {
			t.Errorf("offset %d: ranking = %+v", offset, h)
		}
		for _, c := range h {
			if !c.Direct {
				t.Errorf("offset %d: transitive consumer %s highlighted", offset, c.Service.Key)
			}
		}
	}
	if h := BuildProduct(fleet.ProductMeta{}, snap, from.ServiceKey, from, to, &Result{}, 0, 0).Consumers.Highlights; h == nil {
		t.Error("highlights must be an empty list, never null")
	}
}

// highlightFixtureConsumer is consumer i of the 150 the highlight test pages over: one
// blocking, one incompatible, one refused, every sixth unknown, the rest compatible.
func highlightFixtureConsumer(i int) AffectedConsumer {
	c := AffectedConsumer{Service: fmt.Sprintf("c%03d", i), Direct: i%2 == 0, CompatibilityVerdict: CompatibilityCompatible}
	switch {
	case i == 148:
		c.Service, c.CompatibilityVerdict, c.Targets = "zz-blocks", CompatibilityIncompatible, []string{"prod/k8s/zz"}
	case i == 10:
		c.CompatibilityVerdict = CompatibilityIncompatible
	case i == 12:
		c.Unmet = []contract.Mismatch{{Guarantee: "delivery"}}
	case i%6 == 0:
		c.CompatibilityVerdict = CompatibilityUnknown
	}
	return c
}

// A consumer the viewer may not read is counted, never named.
func TestBuildProduct_HiddenUnderRestrict(t *testing.T) {
	auth := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
	dep := contract.Dependency{Name: "auth-service", Ref: "ghcr.io/x/auth-service:1.0.0", Compatibility: "^1.0.0"}
	col := &fleet.Collection{
		Revisions: []fleet.RawRevision{
			{Bundle: mkBundle(auth)},
			{Bundle: mkBundle(svcContract("seen", "1.0.0", contract.Owner{}, dep))},
			{Bundle: mkBundle(svcContract("secret", "1.0.0", contract.Owner{Team: "vault-team"}, dep))},
		},
		Targets: []fleet.RawTarget{
			{Scope: "shop", Kind: fleet.SourceKindKubernetes, Name: "auth", Service: "auth-service", Compliance: fleet.StatusCompliant},
			{Scope: "shop", Kind: fleet.SourceKindKubernetes, Name: "seen", Service: "seen", Compliance: fleet.StatusCompliant},
			{Scope: "vault", Kind: fleet.SourceKindKubernetes, Name: "secret", Service: "secret", Compliance: fleet.StatusCompliant},
		},
	}
	full, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("test", "memory", col))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(full).Restrict(func(ns string) bool { return ns == "shop" })
	snap := q.SnapshotForSerialization()
	newC := svcContract("auth-service", "2.0.0", contract.Owner{Team: "platform"})
	res := Analyze(context.Background(), auth, newC, fstest.MapFS{}, fstest.MapFS{}, snap, Options{})
	svc := fleet.NewServiceKey("auth-service")
	rev := &fleet.ContractRevision{Key: "auth@1", ServiceKey: svc, Service: "auth-service", Version: "1.0.0"}
	page := BuildProduct(q.ProductMeta(), snap, svc, rev, rev, res, 0, 0).Consumers
	if page.Hidden != 1 || page.Total != 1 || page.Items[0].Service.Label != "seen" {
		t.Errorf("hidden %d, total %d, items %+v", page.Hidden, page.Total, page.Items)
	}
	// The tallies come from the caller's view too: the hidden consumer is in no
	// outcome bucket and its owner is never named.
	sum := 0
	for _, b := range page.ByOutcome {
		sum += b.Count
	}
	if sum != 1 || page.Owners != 0 || len(page.ByOwner) != 0 || page.NoOwner != 1 || page.BeyondRanking != 0 {
		t.Errorf("tallies under restrict = %+v", page.ConsumerTallies)
	}
	if b, _ := json.Marshal(page); bytes.Contains(b, []byte("secret")) || bytes.Contains(b, []byte("vault-team")) {
		t.Errorf("the restricted page names a hidden consumer: %s", b)
	}
}

// outcomeFixture is one consumer of each kind the consumers page orders: two
// highlighted (direct), a refused one, an unknown verdict and an unrecognized one.
func outcomeFixture() *Result {
	return &Result{Classification: "BREAKING", Consumers: []AffectedConsumer{
		{Service: "a-ok", CompatibilityVerdict: CompatibilityCompatible},
		{Service: "b-ok-hl", Direct: true, CompatibilityVerdict: CompatibilityCompatible},
		{Service: "c-unk", CompatibilityVerdict: CompatibilityUnknown},
		{Service: "d-refused", CompatibilityVerdict: CompatibilityCompatible, Unmet: []contract.Mismatch{{Guarantee: "g"}}},
		{Service: "e-novel", CompatibilityVerdict: "novel"},
		{Service: "f-bad", CompatibilityVerdict: CompatibilityIncompatible},
		{Service: "g-bad-hl", Direct: true, CompatibilityVerdict: CompatibilityIncompatible},
	}}
}

func consumerKeys(items []ProductConsumer) []string {
	out := make([]string, 0, len(items))
	for _, c := range items {
		out = append(out, c.Service.Key)
	}
	return out
}

// The table lists consumers as the figure reads them: by outcome, the highlighted
// ones first within each, then by key.
func TestBuildProduct_OutcomeOrder(t *testing.T) {
	snap := productSnapshot()
	from, to := snap.Revisions["api@1"], snap.Revisions["api@2"]
	all := BuildProduct(fleet.ProductMeta{}, snap, from.ServiceKey, from, to, outcomeFixture(), 0, 0).Consumers
	want := []string{"g-bad-hl", "d-refused", "f-bad", "c-unk", "b-ok-hl", "a-ok", "e-novel"}
	if got := consumerKeys(all.Items); !slices.Equal(got, want) || all.Matching != 7 || all.Outcome != "" {
		t.Errorf("order = %v (matching %d), want %v", got, all.Matching, want)
	}
}

// A filtered page keeps one outcome and says how many match, while every tally still
// covers every consumer.
func TestBuildProduct_OutcomeFilter(t *testing.T) {
	snap := productSnapshot()
	from, to := snap.Revisions["api@1"], snap.Revisions["api@2"]
	all := BuildProduct(fleet.ProductMeta{}, snap, from.ServiceKey, from, to, outcomeFixture(), 0, 0).Consumers
	inc := BuildProductOutcome(fleet.ProductMeta{}, snap, from.ServiceKey, from, to, outcomeFixture(), 2, 0, OutcomeIncompatible).Consumers
	if got := consumerKeys(inc.Items); !slices.Equal(got, []string{"g-bad-hl", "d-refused"}) || inc.Matching != 3 || inc.Total != 7 || inc.Outcome != OutcomeIncompatible {
		t.Errorf("incompatible page = %v, matching %d, total %d", got, inc.Matching, inc.Total)
	}
	if inc.NextOffset == nil || *inc.NextOffset != 2 {
		t.Errorf("next offset = %v, want 2", inc.NextOffset)
	}
	if !reflect.DeepEqual(inc.ConsumerTallies, all.ConsumerTallies) || len(inc.Highlights) != len(all.Highlights) {
		t.Errorf("a filter changed the tallies: %+v vs %+v", inc.ConsumerTallies, all.ConsumerTallies)
	}
}

// TestVerdictOrder pins the canonical order the dashboard draws: err, unknown, ok.
// Every bucket is emitted even when empty, so a lane keeps its place between two
// analyses of the same change.
func TestVerdictOrder(t *testing.T) {
	got := tallyConsumers(nil, verdictOrder, func(c ProductConsumer) string { return c.CompatibilityVerdict })
	want := []string{CompatibilityIncompatible, CompatibilityUnknown, CompatibilityCompatible}
	if len(got) != len(want) {
		t.Fatalf("buckets = %+v, want %v", got, want)
	}
	for i, b := range got {
		if b.Key != want[i] || b.Count != 0 {
			t.Errorf("bucket %d = %+v, want {%s 0}", i, b, want[i])
		}
	}
}
