package fleet

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/diff"
)

// changesQuery is a query over one service "svc" whose revision i (version
// versions[i]) holds contracts[i]; a nil entry is a revision whose contract
// was not read.
func changesQuery(versions []string, contracts []*contract.Contract) *Query {
	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{}, Services: map[ServiceKey]*ServiceRecord{}}
	svc := &ServiceRecord{Key: "svc", Name: "svc"}
	for i, v := range versions {
		k := RevisionKey("svc@" + v)
		snap.Revisions[k] = &ContractRevision{Key: k, Service: "svc", ServiceKey: "svc", Version: v, Contract: contracts[i]}
		svc.Revisions = append(svc.Revisions, k)
	}
	snap.Services["svc"] = svc
	return NewQuery(snap)
}

func svcChanges(q *Query) ChangeHistoryPreview { return q.serviceChanges(q.snap.Services["svc"]) }

// changeText is each event as "<version> <kind> <subject> <change> <fields> <from>-><to> <previous version>".
func changeText(h ChangeHistoryPreview) []string {
	out := make([]string, len(h.Items))
	for i, e := range h.Items {
		prev := "-"
		if e.Previous != nil {
			prev = e.Previous.Version
		}
		out[i] = strings.Join([]string{e.Revision.Version, e.Kind, e.Subject, e.Change, strings.Join(e.Fields, ","), e.From + "->" + e.To, prev}, " ")
	}
	return out
}

// only keeps the events of kind k.
func only(h ChangeHistoryPreview, k string) []string {
	var out []string
	for i, l := range changeText(h) {
		if h.Items[i].Kind == k {
			out = append(out, l)
		}
	}
	return out
}

func owned(team string) *contract.Contract {
	return &contract.Contract{Service: contract.Service{Owner: contract.Owner{Team: team}}}
}

func checkChanges(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("changes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestChangesOwnerChanged(t *testing.T) {
	b := owned("b")
	b.Service.Owner.DRI = "bob"
	q := changesQuery([]string{"v1.0.0", "v2.0.0"}, []*contract.Contract{owned("a"), b})
	checkChanges(t, only(svcChanges(q), "owner"), []string{
		"v2.0.0 owner  changed team,dri team:a->team:b v1.0.0",
		"v1.0.0 owner  present  ->team:a -",
	})
	// A contacts-only edit is one owner event; a keyless owner reads its display form.
	c := owned("b")
	c.Service.Owner.Contacts = []contract.OwnerContact{{Type: "email", Value: "x@y"}, {Type: "slack", Value: "#b"}}
	none := &contract.Contract{}
	keyless := &contract.Contract{Service: contract.Service{Owner: contract.Owner{Contacts: c.Service.Owner.Contacts}}}
	q = changesQuery([]string{"v1.0.0", "v2.0.0", "v3.0.0", "v4.0.0", "v5.0.0"}, []*contract.Contract{owned("b"), c, none, keyless, owned("z")})
	checkChanges(t, only(svcChanges(q), "owner"), []string{
		"v5.0.0 owner  changed team,contacts ->team:z v4.0.0",
		"v4.0.0 owner  added contacts -> v3.0.0",
		"v3.0.0 owner  removed team,contacts team:b-> v2.0.0",
		"v2.0.0 owner  changed contacts team:b->team:b v1.0.0",
		"v1.0.0 owner  present  ->team:b -",
	})
}

func TestChangesDomainMoved(t *testing.T) {
	a, b, c := owned("t"), owned("t"), owned("t")
	a.Service.Domain, b.Service.Domain = "payments", "billing"
	q := changesQuery([]string{"v1.0.0", "v2.0.0", "v3.0.0"}, []*contract.Contract{a, b, c})
	checkChanges(t, only(svcChanges(q), "domain"), []string{
		"v3.0.0 domain  removed  billing-> v2.0.0",
		"v2.0.0 domain  changed  payments->billing v1.0.0",
		"v1.0.0 domain  present  ->payments -",
	})
}

func TestChangesOfferingChanged(t *testing.T) {
	a, b := owned("t"), owned("t")
	b.Service.Offering = "database"
	q := changesQuery([]string{"v1.0.0", "v2.0.0"}, []*contract.Contract{a, b})
	checkChanges(t, only(svcChanges(q), "offering"), []string{"v2.0.0 offering  added  ->database v1.0.0"})
}

func TestChangesDependencyAddedRemovedChanged(t *testing.T) {
	a, b := owned("t"), owned("t")
	a.Dependencies = []contract.Dependency{{Name: "db", Ref: "oci://x/db", Compatibility: "^1.0.0"}, {Name: "gone", Ref: "oci://x/gone"}, {Name: "same", Ref: "oci://x/s"}}
	b.Dependencies = []contract.Dependency{
		{Name: "db", Ref: "oci://x/db", Compatibility: "^2.0.0", Required: true, Requires: []contract.Requirement{{Interface: "api"}}},
		{Name: "new", Ref: "oci://x/new", Version: "1.0.0"},
		{Name: "same", Ref: "oci://x/s"},
	}
	q := changesQuery([]string{"v1.0.0", "v2.0.0"}, []*contract.Contract{a, b})
	checkChanges(t, only(svcChanges(q), "dependency"), []string{
		"v2.0.0 dependency db changed compatibility,required,requires compatibility=^1.0.0, required=false->compatibility=^2.0.0, required=true v1.0.0",
		"v2.0.0 dependency gone removed  oci://x/gone-> v1.0.0",
		"v2.0.0 dependency new added  ->oci://x/new 1.0.0 v1.0.0",
	})
}

// TestChangesDependencySubjectWithColon: a value holding ": " never moves the
// subject, which comes from the dependency's name.
func TestChangesDependencySubjectWithColon(t *testing.T) {
	a, b := owned("t"), owned("t")
	a.Dependencies = []contract.Dependency{{Name: "db", Ref: "oci://x/db: odd", Version: "a: b"}}
	b.Dependencies = []contract.Dependency{{Name: "db", Ref: "oci://x/db: odd", Version: "c: d"}}
	q := changesQuery([]string{"v1.0.0", "v2.0.0"}, []*contract.Contract{a, b})
	checkChanges(t, only(svcChanges(q), "dependency"), []string{"v2.0.0 dependency db changed version version=a: b->version=c: d v1.0.0"})
}

func TestChangesLifecycleDeprecated(t *testing.T) {
	a, b, c, d := owned("t"), owned("t"), owned("t"), owned("t")
	b.Service.Lifecycle = &contract.InterfaceLifecycle{Deprecated: "2026-01-01"}
	c.Service.Lifecycle = &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Sunset: "2027-01-01"}
	q := changesQuery([]string{"v1.0.0", "v2.0.0", "v3.0.0", "v4.0.0"}, []*contract.Contract{a, b, c, d})
	checkChanges(t, only(svcChanges(q), "lifecycle"), []string{
		"v4.0.0 lifecycle  removed  deprecated 2026-01-01, sunset 2027-01-01-> v3.0.0",
		"v3.0.0 lifecycle  changed sunset deprecated 2026-01-01->deprecated 2026-01-01, sunset 2027-01-01 v2.0.0",
		"v2.0.0 lifecycle  added  ->deprecated 2026-01-01 v1.0.0",
	})
}

func env(name, file string) contract.Environment { return contract.Environment{Name: name, File: file} }

// envChanges is a query over "svc" whose revision i (version versions[i])
// declares envs[i].
func envChanges(versions []string, envs [][]contract.Environment) ChangeHistoryPreview {
	cs := make([]*contract.Contract, len(envs))
	for i, e := range envs {
		cs[i] = &contract.Contract{Environments: e}
	}
	return svcChanges(changesQuery(versions, cs))
}

// TestChangesEnvironment: an environment added, removed or with a declared
// field changed is one event per environment, sorted by name; the oldest
// revision held reads present.
func TestChangesEnvironment(t *testing.T) {
	ownedEnv := env("preview", "p.yaml")
	ownedEnv.Owner = &contract.Owner{Team: "previews"}
	ownedEnv.Lifetime = contract.EnvironmentTemporary
	got := envChanges([]string{"v1.0.0", "v1.1.0", "v1.2.0"}, [][]contract.Environment{
		{env("staging", "s.yaml"), env("preview", "p.yaml")},
		{env("staging", "s2.yaml"), ownedEnv, env("prod", "prod.yaml")},
		{ownedEnv, env("prod", "prod.yaml")},
	})
	checkChanges(t, changeText(got), []string{
		"v1.2.0 environment staging removed  -> v1.1.0",
		"v1.1.0 environment preview changed lifetime,owner -> v1.0.0",
		"v1.1.0 environment prod added  -> v1.0.0",
		"v1.1.0 environment staging changed file -> v1.0.0",
		"v1.0.0 environment preview present  -> -",
		"v1.0.0 environment staging present  -> -",
	})
	if got.Completeness != "partial" || got.CompletenessReason != versionsHeldReason {
		t.Errorf("completeness: %+v", got)
	}
}

// TestChangesHotfixInVersionOrder: a hotfix published after a newer minor
// sits between its neighbours, because the order is the version order.
func TestChangesHotfixInVersionOrder(t *testing.T) {
	got := envChanges([]string{"v1.2.0", "v1.1.1", "v1.1.0"}, [][]contract.Environment{
		{env("a", "a.yaml"), env("c", "c.yaml")}, {env("a", "a.yaml"), env("b", "b.yaml")}, {env("a", "a.yaml")},
	})
	checkChanges(t, changeText(got), []string{
		"v1.2.0 environment b removed  -> v1.1.1",
		"v1.2.0 environment c added  -> v1.1.1",
		"v1.1.1 environment b added  -> v1.1.0",
		"v1.1.0 environment a present  -> -",
	})
	f, _ := reflect.TypeFor[ChangeHistoryPreview]().FieldByName("Items")
	if !strings.Contains(f.Tag.Get("doc"), "version order, not a timeline") {
		t.Errorf("items doc = %q", f.Tag.Get("doc"))
	}
}

func TestWalkRevisionPairsGaps(t *testing.T) {
	rev := func(v string, read bool) *ContractRevision {
		r := &ContractRevision{Version: v}
		if read {
			r.Contract = &contract.Contract{}
		}
		return r
	}
	recs := []*ContractRevision{rev("v1.3.0", true), rev("v1.0.0", false), rev("v1.2.0", true), rev("v1.1.0", true), rev("v1.4.0", false), rev("v1.5.0", true)}
	var pairs []string
	gaps := walkRevisionPairs(recs, func(prev, r *ContractRevision) {
		p := "-"
		if prev != nil {
			p = prev.Version
		}
		pairs = append(pairs, p+">"+r.Version)
	})
	// The oldest is unread, so the first readable one reads as the oldest;
	// v1.4.0 breaks the comparison to v1.5.0, which is compared with nothing.
	if gaps != 2 || !slices.Equal(pairs, []string{"->v1.1.0", "v1.1.0>v1.2.0", "v1.2.0>v1.3.0", "->v1.5.0"}) {
		t.Errorf("gaps %d pairs %q", gaps, pairs)
	}
	pairs = nil
	walkRevisionPairs([]*ContractRevision{rev("v2.0.0", true), rev("v1.0.0", true)}, func(prev, r *ContractRevision) {
		if prev == nil {
			pairs = append(pairs, "-"+r.Version)
		} else {
			pairs = append(pairs, prev.Version+">"+r.Version)
		}
	})
	if !slices.Equal(pairs, []string{"-v1.0.0", "v1.0.0>v2.0.0"}) {
		t.Errorf("pairs %q", pairs)
	}
}

// TestChangesOldestIsPresent: the oldest revision held reads present for its
// owner, domain and environments only; dependencies and lifecycle never do.
func TestChangesOldestIsPresent(t *testing.T) {
	c := owned("a")
	c.Service.Domain, c.Service.Offering = "payments", "database"
	c.Service.Lifecycle = &contract.InterfaceLifecycle{Deprecated: "2026-01-01"}
	c.Dependencies = []contract.Dependency{{Name: "db"}}
	c.Environments = []contract.Environment{env("prod", "p.yaml")}
	h := svcChanges(changesQuery([]string{"v1.0.0"}, []*contract.Contract{c}))
	checkChanges(t, changeText(h), []string{
		"v1.0.0 owner  present  ->team:a -",
		"v1.0.0 domain  present  ->payments -",
		"v1.0.0 environment prod present  -> -",
	})
	if h.Completeness != "complete" {
		t.Errorf("one revision compares nothing: %+v", h)
	}
	want := []string{
		"  svc v1.0.0  owner team:a declared since at least svc v1.0.0  time not read",
		"  svc v1.0.0  domain payments declared since at least svc v1.0.0  time not read",
		"  svc v1.0.0  environment prod declared since at least svc v1.0.0  time not read",
	}
	checkChanges(t, h.Lines(), want)
	if empty := svcChanges(changesQuery([]string{"v1.0.0"}, []*contract.Contract{{}})); empty.Items == nil || empty.Total != 0 || len(empty.Lines()) != 0 {
		t.Errorf("empty = %+v", empty)
	}
}

// TestChangesGapBreaksComparison: an unread revision is counted, makes no
// event and breaks the comparison across it.
func TestChangesGapBreaksComparison(t *testing.T) {
	h := svcChanges(changesQuery([]string{"v1.0.0", "v1.1.0", "v1.2.0"}, []*contract.Contract{owned("a"), nil, owned("b")}))
	// The revision after the gap still says what it declares; the answer is
	// partial, since what changed across the gap is unknown.
	checkChanges(t, changeText(h), []string{"v1.2.0 owner  present  ->team:b -", "v1.0.0 owner  present  ->team:a -"})
	if h.Gaps != 1 || !strings.Contains(h.Heading(), "1 revision(s) unread") {
		t.Errorf("gaps %d heading %q", h.Gaps, h.Heading())
	}
	if h.Completeness != "partial" || !strings.Contains(h.CompletenessReason, "1 revision(s) could not be read; changes across them are unknown") {
		t.Errorf("completeness %q %q", h.Completeness, h.CompletenessReason)
	}
}

// Two revisions of one version have no order: when they disagree, no
// direction is reported between them; when they agree, nothing is.
func TestChangesSameVersionNoDirection(t *testing.T) {
	q := changesQuery([]string{"v1.0.0"}, []*contract.Contract{owned("a")})
	for k, team := range map[RevisionKey]string{"svc@sha256:1a": "a", "svc@sha256:9f": "b"} {
		q.snap.Revisions[k] = &ContractRevision{Key: k, Service: "svc", ServiceKey: "svc", Version: "v1.2.0", Contract: owned(team)}
	}
	q.snap.Services["svc"].Revisions = []RevisionKey{"svc@v1.0.0", "svc@sha256:1a", "svc@sha256:9f"}
	h := svcChanges(q)
	for _, e := range h.Items {
		if e.Previous != nil && e.Previous.Version == e.Revision.Version {
			t.Errorf("directional event between one version's revisions: %+v", e)
		}
	}
	if h.Completeness != "partial" || !strings.Contains(h.CompletenessReason, "1 version(s) are held as revisions that disagree") {
		t.Errorf("completeness %q %q", h.Completeness, h.CompletenessReason)
	}
	// Two identical copies of one version are one version.
	q.snap.Revisions["svc@sha256:9f"].Contract = owned("a")
	q.snap.Revisions["svc@sha256:1a"].Contract = owned("a")
	if h := svcChanges(q); strings.Contains(h.CompletenessReason, "disagree") {
		t.Errorf("identical copies: %q", h.CompletenessReason)
	}
}

// TestChangesRestrictedPartial: a revision the view hides contributes
// nothing and makes the answer partial without naming it.
func TestChangesRestrictedPartial(t *testing.T) {
	secret := owned("secret")
	tmpl := owned("a")
	tmpl.Service.Template = &contract.Template{}
	q := changesQuery([]string{"v1.0.0", "v2.0.0"}, []*contract.Contract{secret, tmpl})
	q.snap.Services["svc"].Template = true
	r := q.Restrict(func(string) bool { return false })
	h := r.serviceChanges(r.snap.Services["svc"])
	checkChanges(t, changeText(h), []string{"v2.0.0 owner  present  ->team:a -"})
	if h.Completeness != "partial" || h.CompletenessReason != historyPartialReason || !strings.Contains(h.Heading(), "partial: "+historyPartialReason) {
		t.Errorf("restricted: %+v", h)
	}
	q.snap.Sources = []SourceState{{ID: "oci", Status: SourcePartial}}
	if h := svcChanges(q); h.Completeness != "partial" {
		t.Errorf("a partial source: %+v", h)
	}
}

func TestChangesBounded(t *testing.T) {
	c := owned("a")
	for i := range MaxDetailPreview + 1 {
		c.Environments = append(c.Environments, env(fmt.Sprintf("e%d", i), "x.yaml"))
	}
	h := svcChanges(changesQuery([]string{"v1.0.0"}, []*contract.Contract{c}))
	if h.Total != MaxDetailPreview+2 || h.Count != MaxDetailPreview || !h.Truncated || len(h.Items) != MaxDetailPreview {
		t.Errorf("bounds: %d %d %v", h.Total, h.Count, h.Truncated)
	}
	if !strings.Contains(h.Heading(), fmt.Sprintf("%d of %d", MaxDetailPreview, MaxDetailPreview+2)) {
		t.Errorf("heading = %q", h.Heading())
	}
}

// TestChangesIgnoresInterfaceEdits: what pacto diff reports beyond the kinds
// listed (an interface, the version, a value in an environment file) is no
// change event.
func TestChangesIgnoresInterfaceEdits(t *testing.T) {
	a, b := owned("t"), owned("t")
	a.Service.Version, b.Service.Version = "1.0.0", "2.0.0"
	a.Interfaces = []contract.Interface{{Name: "api", Type: "http", Ref: "a.yaml"}}
	b.Interfaces = []contract.Interface{{Name: "api", Type: "http", Ref: "b.yaml", Visibility: "internal"}, {Name: "grpc", Type: "grpc"}}
	h := svcChanges(changesQuery([]string{"v1.0.0", "v2.0.0"}, []*contract.Contract{a, b}))
	checkChanges(t, changeText(h), []string{"v1.0.0 owner  present  ->team:t -"})
	// Non-field environment paths are left to pacto diff.
	var out []ChangeEvent
	for _, p := range []string{"environments[prod].values.replicas", "environments[prod]", "interfaces"} {
		out = appendEnvironmentEvent(out, map[string]*ChangeEvent{}, diffChange(p))
	}
	if len(out) != 0 {
		t.Errorf("events = %+v", out)
	}
}

// TestChangesNeverUseFetchedAt: the order is the version order, whatever the
// read times say, and no event carries a time.
func TestChangesNeverUseFetchedAt(t *testing.T) {
	q := changesQuery([]string{"v1.0.0", "v2.0.0"}, []*contract.Contract{owned("a"), owned("b")})
	now := time.Now()
	q.snap.Revisions["svc@v1.0.0"].FetchedAt = &now
	q.snap.Revisions["svc@v2.0.0"].FetchedAt = new(now.Add(-time.Hour))
	h := svcChanges(q)
	checkChanges(t, changeText(h), []string{"v2.0.0 owner  changed team team:a->team:b v1.0.0", "v1.0.0 owner  present  ->team:a -"})
	// The revision refs keep their read time; the events carry none.
	for _, e := range h.Items {
		e.Revision, e.Previous = EntityRef{}, nil
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		for _, timeKey := range []string{`"at"`, `"time"`, `"fetchedAt"`, `"date"`, `"reached"`} {
			if strings.Contains(string(b), timeKey) {
				t.Errorf("event JSON carries a time %s: %s", timeKey, b)
			}
		}
	}
	for _, l := range h.Lines() {
		if !strings.HasSuffix(l, "  time not read") {
			t.Errorf("line %q", l)
		}
	}
	// Read delivery that holds no arrival: FetchedAt still gives no time.
	q.AttachReached(&h, nil, TimesRead, "", nil)
	for _, l := range h.Lines() {
		if !strings.HasSuffix(l, "  time not recorded") {
			t.Errorf("line %q", l)
		}
	}
	if l := h.Lines()[0]; l != "  svc v2.0.0  owner changed (team): team:a -> team:b, compared with svc v1.0.0  time not recorded" {
		t.Errorf("line = %q", l)
	}
	f, _ := reflect.TypeFor[ChangeHistoryPreview]().FieldByName("Items")
	if !strings.Contains(f.Tag.Get("doc"), "version order, not a timeline") || !strings.Contains(h.Heading(), "version order") {
		t.Errorf("items doc %q heading %q", f.Tag.Get("doc"), h.Heading())
	}
}

func TestChangesProvenanceDeclared(t *testing.T) {
	a, b := owned("a"), owned("b")
	b.Service.Domain = "d"
	b.Dependencies = []contract.Dependency{{Name: "x"}}
	b.Environments = []contract.Environment{env("prod", "p.yaml")}
	h := svcChanges(changesQuery([]string{"v1.0.0", "v2.0.0"}, []*contract.Contract{a, b}))
	if len(h.Items) != 5 {
		t.Fatalf("items = %q", changeText(h))
	}
	for _, e := range h.Items {
		if e.Provenance != "declared" || e.Source != "contract" || e.Revision.Kind != KindRevision {
			t.Errorf("event %+v", e)
		}
	}
	// Removed and added alone read their one value.
	if l := h.Lines(); !slices.Contains(l, "  svc v2.0.0  domain added: d, compared with svc v1.0.0  time not read") {
		t.Errorf("lines = %q", l)
	}
}

func diffChange(path string) diff.Change { return diff.Change{Path: path, Type: diff.Modified} }

// TestServiceDetailJSONKeys: the service detail's JSON keys are the released
// ones plus changes, which replaced an unreleased environment-only field.
func TestServiceDetailJSONKeys(t *testing.T) {
	want := []string{"domain", "ownership", "ownerRevision", "lifecycle", "capabilities", "sources", "unresolvedDependencies",
		"consumedOfferings", "inferredOfferings", "currentRevision", "policies", "configurations", "inferredFields", "summary", "revisions",
		"activeRevisions", "signatures", "deployments", "placement", "dependencies", "dependents", "changes",
		"hiddenDependencies", "hiddenProviders", "hiddenDependents", "dependentsStatus", "referencedBy", "relationships",
		"findings", "evidence", "limitations", "template", "origin", "creation"}
	ty := reflect.TypeFor[ServiceDetailData]()
	got := make([]string, ty.NumField())
	for i := range got {
		got[i], _, _ = strings.Cut(ty.Field(i).Tag.Get("json"), ",")
	}
	checkChanges(t, got, want)
}
