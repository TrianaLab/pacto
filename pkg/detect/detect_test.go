package detect

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var asOf = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestDecodeIgnoresUnknownFields: a later writer (510-S6 adds an advertised
// block) stays readable.
func TestDecodeIgnoresUnknownFields(t *testing.T) {
	res, err := Decode([]byte(`{"schemaVersion":"1.2","asOf":"2026-11-16T09:00:00Z","advertised":{"registry":"r"},"entries":[{"kind":"cloud","completeness":"complete","extra":1}]}`))
	if err != nil || len(res.Entries) != 1 || res.Entries[0].Kind != "cloud" || !res.AsOf.Equal(asOf) {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

func TestDecodeRefusesOtherMajor(t *testing.T) {
	if _, err := Decode([]byte(`{"schemaVersion":"2","entries":[]}`)); err == nil || !strings.Contains(err.Error(), `"2"`) {
		t.Errorf("err = %v, want the version refusal", err)
	}
	if _, err := Decode([]byte(`{`)); err == nil {
		t.Error("malformed JSON decoded")
	}
}

func TestDecodeTruncates(t *testing.T) {
	long := strings.Repeat("é", MaxRunes+10)
	e := Entry{Kind: "registry", Value: long, Completeness: Complete, Details: make([]string, MaxDetails+3),
		Namespaces: make([]string, MaxNamed+1), Objects: make([]Object, MaxNamed+1)}
	in := Result{SchemaVersion: SchemaVersion, AsOf: asOf, Cluster: long, Namespaces: make([]string, MaxNamed+2), Entries: slices.Repeat([]Entry{e}, MaxEntries+4)}
	res, err := Decode(mustJSON(t, in))
	if err != nil {
		t.Fatal(err)
	}
	got := res.Entries[0]
	if len(res.Entries) != MaxEntries || len(got.Details) != MaxDetails || len(got.Namespaces) != MaxNamed || len(got.Objects) != MaxNamed ||
		len(res.Namespaces) != MaxNamed || len([]rune(got.Value)) != MaxRunes || len([]rune(res.Cluster)) != MaxRunes {
		t.Errorf("not bounded: %d entries, %d details, %d namespaces, %d objects, %d result namespaces, %d runes",
			len(res.Entries), len(got.Details), len(got.Namespaces), len(got.Objects), len(res.Namespaces), len([]rune(got.Value)))
	}
}

func TestDecodeStripsControlAndBidi(t *testing.T) {
	dirty := "a\u001b[2Jb\u202ec\u2066d\u200ee\u0085f"
	e := Entry{Kind: dirty, Value: dirty, Evidence: dirty, Source: dirty, Completeness: dirty, Reason: dirty, Hint: dirty, Scope: dirty,
		Details: []string{dirty}, Namespaces: []string{dirty}, Objects: []Object{{Kind: dirty, Namespace: dirty, Name: dirty}}}
	res, err := Decode(mustJSON(t, Result{SchemaVersion: SchemaVersion, Cluster: dirty, Namespaces: []string{dirty}, Entries: []Entry{e}}))
	if err != nil {
		t.Fatal(err)
	}
	g := res.Entries[0]
	for _, s := range []string{res.Cluster, res.Namespaces[0], g.Kind, g.Value, g.Evidence, g.Source, g.Completeness, g.Reason, g.Hint, g.Scope,
		g.Details[0], g.Namespaces[0], g.Objects[0].Kind, g.Objects[0].Namespace, g.Objects[0].Name} {
		if s != "a[2Jbcdef" {
			t.Errorf("%q kept a control or bidi character", s)
		}
	}
}

func row(t *testing.T, rows []fleet.Provider, id string) fleet.Provider {
	t.Helper()
	i := slices.IndexFunc(rows, func(p fleet.Provider) bool { return p.ID == id })
	if i < 0 {
		t.Fatalf("no row %q in %+v", id, rows)
	}
	return rows[i]
}

func TestProviderRowsAreInferred(t *testing.T) {
	res := Result{AsOf: asOf, Entries: []Entry{
		{Kind: "registry", Value: "ghcr.io/acme", Evidence: "in 9 of 11 namespaces", Source: "running pods", Completeness: Complete, Hint: "oci://ghcr.io/acme/<service>:<version>", Details: []string{"a", "b"}},
		{Kind: "metrics", Completeness: Complete},
	}}
	rows := ProviderRows(res, "detection", asOf.Add(time.Minute))
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	for _, p := range rows {
		if p.Provenance != fleet.ProvenanceInferred || p.AsOf == nil || !p.AsOf.Equal(asOf) || p.Status != fleet.SourceAvailable || len(p.UsedFor) != 1 {
			t.Errorf("row %+v is not an available inferred row with asOf", p)
		}
	}
	reg := row(t, rows, "registry")
	if reg.Name != "Registry" || reg.Evidence != "ghcr.io/acme in 9 of 11 namespaces (from running pods)" || reg.Hint == "" || len(reg.Details) != 2 ||
		reg.UsedFor[0] != "proposal for delivery; set it yourself" {
		t.Errorf("registry row = %+v", reg)
	}
	if m := row(t, rows, "metrics"); m.Evidence != "(from detection)" {
		t.Errorf("metrics evidence = %q", m.Evidence)
	}
}

// TestProviderRowsNameOtherKinds: a kind Pacto does not know yet gets a
// title-cased name and the generic proposal.
func TestProviderRowsNameOtherKinds(t *testing.T) {
	rows := ProviderRows(Result{AsOf: asOf, Entries: []Entry{{Kind: "custom-thing", Completeness: Complete}, {Kind: "é-kind", Completeness: Complete}, {Completeness: Complete}}}, "d", asOf)
	for id, want := range map[string]string{"custom-thing": "Custom-thing", "é-kind": "É-kind", "": "Detected"} {
		if p := row(t, rows, id); p.Name != want || p.UsedFor[0] != "proposal; nothing is turned on" {
			t.Errorf("row %q = %+v, want name %q", id, p, want)
		}
	}
}

func TestProviderRowsUnknownEntryIsUnavailable(t *testing.T) {
	rows := ProviderRows(Result{AsOf: asOf, Entries: []Entry{
		{Kind: "cloud", Completeness: Unknown, Reason: "with controller.watchNamespaces the operator may not list nodes"},
		{Kind: "nodes", Completeness: "bogus"},
	}}, "detection", asOf.Add(time.Hour))
	c := row(t, rows, "cloud")
	if c.Status != fleet.SourceUnavailable || len(c.Limitations) != 1 || c.Limitations[0].Code != "DETECTION_CLOUD_UNKNOWN" || c.Limitations[0].Source != "detection" {
		t.Errorf("unknown cloud row = %+v", c)
	}
	if n := row(t, rows, "nodes"); n.Status != fleet.SourceUnavailable {
		t.Errorf("an unrecognised completeness must not read positive: %+v", n)
	}
}

func TestProviderRowsPartialEntryNamesReason(t *testing.T) {
	rows := ProviderRows(Result{AsOf: asOf, Entries: []Entry{
		{Kind: "sign-in", Completeness: Partial, Reason: "an identity provider outside the cluster is not visible"},
		{Kind: "exposure", Completeness: Complete, Reason: "ignored on a complete entry"},
	}}, "detection", asOf)
	s := row(t, rows, "sign-in")
	if s.Status != fleet.SourcePartial || len(s.Limitations) != 1 || s.Limitations[0].Code != "DETECTION_SIGN_IN_PARTIAL" {
		t.Errorf("partial row = %+v", s)
	}
	if e := row(t, rows, "exposure"); e.Limitations != nil {
		t.Errorf("complete row gained a limitation: %+v", e)
	}
}

func TestProviderRowsStaleAfter(t *testing.T) {
	res := Result{AsOf: asOf, Entries: []Entry{{Kind: "cloud", Completeness: Complete}, {Kind: "nodes", Completeness: Partial}, {Kind: "metrics", Completeness: Unknown}}}
	for _, p := range ProviderRows(res, "d", asOf.Add(StaleAfter)) {
		if p.Status == fleet.SourceStale {
			t.Errorf("%s stale at exactly StaleAfter", p.ID)
		}
	}
	rows := ProviderRows(res, "d", asOf.Add(StaleAfter+time.Second))
	if row(t, rows, "cloud").Status != fleet.SourceStale || row(t, rows, "nodes").Status != fleet.SourceStale || row(t, rows, "metrics").Status != fleet.SourceUnavailable {
		t.Errorf("rows = %+v", rows)
	}
}

func TestProviderRowsCarryScopeAndCluster(t *testing.T) {
	res := Result{AsOf: asOf, Cluster: "uid-1", Entries: []Entry{
		{Kind: "nodes", Completeness: Partial, Scope: fleet.ScopeCluster},
		{Kind: "exposure", Value: "Gateway API", Evidence: "on a Gateway", Completeness: Complete, Namespaces: []string{"infra"}, Objects: []Object{{Kind: "Gateway", Namespace: "infra", Name: "public"}}},
	}}
	rows := ProviderRows(res, "detection", asOf)
	n := row(t, rows, "nodes")
	if n.Scope != fleet.ScopeCluster || n.Cluster != "uid-1" || n.ClusterID != "uid-1" || !strings.HasSuffix(n.Evidence, "(from detection, cluster uid-1)") {
		t.Errorf("nodes row = %+v", n)
	}
	// The operator's display name for the cluster reads instead of its id.
	res.ClusterName = "kind-pacto"
	if n := row(t, ProviderRows(res, "detection", asOf), "nodes"); n.Cluster != "kind-pacto" || n.ClusterID != "uid-1" || !strings.HasSuffix(n.Evidence, "(from detection, cluster kind-pacto)") {
		t.Errorf("named nodes row = %+v", n)
	}
	e := row(t, rows, "exposure")
	want := []fleet.ProviderObject{{Kind: "Namespace", Namespace: "infra", Name: "infra"}, {Kind: "Gateway", Namespace: "infra", Name: "public"}}
	if e.Scope != "" || !slices.Equal(e.Objects, want) || strings.Contains(e.Evidence, "infra") {
		t.Errorf("exposure row = %+v", e)
	}
}
