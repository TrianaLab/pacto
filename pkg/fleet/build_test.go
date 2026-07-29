package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/lock"
)

// -------------------- sanitizeError --------------------

func TestSanitizeError(t *testing.T) {
	if sanitizeError(nil) != nil {
		t.Fatal("nil error must sanitize to nil")
	}
	tests := []struct {
		name string
		raw  string
		code string
	}{
		{"cancelled", "context canceled while dialing", "CANCELLED"},
		{"deadline", "context deadline exceeded", "CANCELLED"},
		{"timeout", "i/o timeout to registry", "CANCELLED"},
		{"unauthorized", "unauthorized: bearer token SECRET-abc123", "AUTH_FAILED"},
		{"forbidden 403", "403 forbidden for user", "AUTH_FAILED"},
		{"not found", "manifest not found: repo/x", "NOT_FOUND"},
		{"404", "registry returned 404", "NOT_FOUND"},
		{"generic", "some totally unexpected failure at host 10.0.0.1:5000", "UNAVAILABLE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			se := sanitizeError(errors.New(tt.raw))
			if se.Code != tt.code {
				t.Errorf("code = %q, want %q", se.Code, tt.code)
			}
			// The sanitized message must never echo raw secret text.
			for _, secret := range []string{"SECRET-abc123", "10.0.0.1", "repo/x", "bearer"} {
				if strings.Contains(se.Message, secret) {
					t.Errorf("sanitized message leaked %q: %q", secret, se.Message)
				}
			}
		})
	}
}

func TestSanitizeError_AuthKeywords(t *testing.T) {
	// Exercise each keyword arm of the AUTH_FAILED case.
	for _, raw := range []string{"credential expired", "access denied", "401 nope", "generic auth error"} {
		if se := sanitizeError(errors.New(raw)); se.Code != "AUTH_FAILED" {
			t.Errorf("%q → %q, want AUTH_FAILED", raw, se.Code)
		}
	}
}

// -------------------- sourceStateFor / unavailableState --------------------

func TestSourceStateFor_DerivedAvailable(t *testing.T) {
	src := NewMemorySource("oci", "registry", nil)
	st := sourceStateFor(src, &Collection{}, fixedNow(), 3, 4)
	if st.Status != SourceAvailable || st.ID != "oci" || st.Kind != "registry" {
		t.Fatalf("unexpected derived state: %+v", st)
	}
	if st.RevisionCount != 3 || st.TargetCount != 4 {
		t.Errorf("counts = %d/%d", st.RevisionCount, st.TargetCount)
	}
	if st.LastSuccessfulSync == nil || st.ObservedAt == nil {
		t.Error("derived available state must carry timestamps")
	}
}

func TestSourceStateFor_SuppliedState_Backfilled(t *testing.T) {
	src := NewMemorySource("k8s", "kubernetes", nil)
	col := &Collection{State: &SourceState{Status: SourcePartial}} // blank ID/Kind
	st := sourceStateFor(src, col, fixedNow(), 1, 2)
	if st.ID != "k8s" || st.Kind != "kubernetes" {
		t.Errorf("ID/Kind not backfilled: %+v", st)
	}
	if st.Status != SourcePartial || st.RevisionCount != 1 || st.TargetCount != 2 {
		t.Errorf("unexpected: %+v", st)
	}
}

func TestSourceStateFor_SuppliedState_Preserved(t *testing.T) {
	src := NewMemorySource("k8s", "kubernetes", nil)
	col := &Collection{State: &SourceState{ID: "explicit", Kind: "custom", Status: SourceStale}}
	st := sourceStateFor(src, col, fixedNow(), 0, 0)
	if st.ID != "explicit" || st.Kind != "custom" {
		t.Errorf("explicit ID/Kind must be preserved: %+v", st)
	}
}

func TestUnavailableState(t *testing.T) {
	src := NewMemorySource("oci", "registry", nil)
	st := unavailableState(src, errors.New("401 unauthorized token=abc"))
	if st.Status != SourceUnavailable || st.Error == nil || st.Error.Code != "AUTH_FAILED" {
		t.Fatalf("unexpected: %+v", st)
	}
	if strings.Contains(st.Error.Message, "abc") {
		t.Error("unavailable state leaked secret")
	}
}

// -------------------- revisionFrom --------------------

func TestRevisionFrom_NilBundle(t *testing.T) {
	if revisionFrom(RawRevision{}, "src", fixedNow()) != nil {
		t.Error("nil bundle must project to nil")
	}
	if revisionFrom(RawRevision{Bundle: &contract.Bundle{}}, "src", fixedNow()) != nil {
		t.Error("bundle with nil contract must project to nil")
	}
}

func TestRevisionFrom_ValidAndInvalidYAML(t *testing.T) {
	valid := revisionFrom(RawRevision{Bundle: validLeafBundle(t), Digest: "sha256:leaf"}, "local", fixedNow())
	if !valid.Valid {
		t.Errorf("valid bundle should be Valid; findings=%+v", valid.Validation)
	}
	if valid.Key != NewRevisionKey("leaf-svc", "sha256:leaf", "", "1.2.3") {
		t.Errorf("unexpected key %q", valid.Key)
	}

	badBundle := &contract.Bundle{Contract: mustParse(t, invalidYAML), RawYAML: []byte(invalidYAML), FS: fstest.MapFS{}}
	bad := revisionFrom(RawRevision{Bundle: badBundle}, "local", fixedNow())
	if bad.Valid {
		t.Error("invalid bundle should be !Valid")
	}
	if len(bad.Validation) == 0 {
		t.Error("invalid bundle should carry findings")
	}
}

func TestRevisionFrom_ProjectionsMatrix(t *testing.T) {
	// Contract with readiness, and a bundle FS carrying skills + docs + openapi.
	c := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "proj-svc", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Interfaces:   []contract.Interface{{Name: "http", Type: contract.InterfaceTypeOpenAPI, Ref: "interfaces/openapi.json"}},
		Readiness: &contract.Readiness{
			Expires: "2099-12-31",
			Claims:  []contract.ReadinessClaim{{ID: "dash", Type: "url", Status: contract.StatusDone, Evidence: "https://x", Weight: 10}},
		},
	}
	fsys := fstest.MapFS{
		"interfaces/openapi.json": {Data: []byte(smallOpenAPI)},
		"skills/workflow.md":      {Data: []byte("# skill")},
		"docs/overview.md":        {Data: []byte("# doc")},
		lock.FileName:             {Data: []byte(validLockYAML)},
	}
	rev := revisionFrom(RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fsys}}, "local", fixedNow())
	if rev.Readiness == nil {
		t.Error("readiness should be evaluated")
	}
	if len(rev.Tools) == 0 {
		t.Error("tools should be derived from openapi")
	}
	if len(rev.Skills) != 1 || rev.Skills[0] != "workflow.md" {
		t.Errorf("skills = %v", rev.Skills)
	}
	if len(rev.Docs) != 1 {
		t.Errorf("docs = %v", rev.Docs)
	}
	if rev.Lock == nil {
		t.Error("lock should be parsed from FS pacto.lock")
	}
	// RawYAML nil → validation skipped → Valid stays false and no findings.
	if rev.Valid || len(rev.Validation) != 0 {
		t.Error("validation must be skipped when RawYAML is nil")
	}
}

func TestRevisionFrom_NoReadinessEmptyFS(t *testing.T) {
	// NOTE: an empty (non-nil) FS is used deliberately. revisionFrom calls
	// skills.List(b.FS) with no nil guard (unlike toolsFrom/docsFrom/lockFrom),
	// so a bundle with a nil FS panics — see the production-bug note in the report.
	c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "bare", Version: "1.0.0"}}
	rev := revisionFrom(RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}}, "local", fixedNow())
	if rev.Readiness != nil {
		t.Error("no readiness declared → nil result")
	}
	if rev.Tools != nil || rev.Docs != nil || rev.Skills != nil {
		t.Error("empty FS → no tools/docs/skills")
	}
	if rev.Lock != nil {
		t.Error("empty FS → no lock")
	}
}

// -------------------- toolsFrom --------------------

func TestToolsFrom_NilFS(t *testing.T) {
	if toolsFrom(&contract.Contract{}, nil) != nil {
		t.Error("nil fs → nil tools")
	}
}

func TestToolsFrom_SingleInterface_SummaryFallback(t *testing.T) {
	c := &contract.Contract{Interfaces: []contract.Interface{
		{Name: "http", Type: contract.InterfaceTypeOpenAPI, Ref: "interfaces/openapi.json"},
	}}
	fsys := fstest.MapFS{"interfaces/openapi.json": {Data: []byte(smallOpenAPI)}}
	tools := toolsFrom(c, fsys)
	if len(tools) != 2 {
		t.Fatalf("want 2 tools, got %d: %+v", len(tools), tools)
	}
	var getB ToolSummary
	for _, tl := range tools {
		if tl.Name == "getB" {
			getB = tl
		}
		if strings.Contains(tl.Name, "_") {
			t.Errorf("single-iface tools should not be prefixed: %q", tl.Name)
		}
	}
	if getB.Summary != "only a description" {
		t.Errorf("summary fallback to description failed: %q", getB.Summary)
	}
}

func TestToolsFrom_MultiInterface_Prefixed(t *testing.T) {
	c := &contract.Contract{Interfaces: []contract.Interface{
		{Name: "public", Type: contract.InterfaceTypeOpenAPI, Ref: "interfaces/a.json"},
		{Name: "admin", Type: contract.InterfaceTypeOpenAPI, Ref: "interfaces/b.json"},
		{Name: "events", Type: contract.InterfaceTypeAsyncAPI, Ref: "interfaces/events.json"}, // skipped
		{Name: "empty", Type: contract.InterfaceTypeOpenAPI, Ref: ""},                         // skipped
	}}
	fsys := fstest.MapFS{
		"interfaces/a.json": {Data: []byte(smallOpenAPI)},
		"interfaces/b.json": {Data: []byte(smallOpenAPI)},
	}
	tools := toolsFrom(c, fsys)
	if len(tools) != 4 {
		t.Fatalf("want 4 tools across 2 openapi ifaces, got %d", len(tools))
	}
	for _, tl := range tools {
		if !strings.HasPrefix(tl.Name, "public_") && !strings.HasPrefix(tl.Name, "admin_") {
			t.Errorf("multi-iface tool must be prefixed: %q", tl.Name)
		}
	}
}

func TestToolsFrom_ReadDocError_Skipped(t *testing.T) {
	c := &contract.Contract{Interfaces: []contract.Interface{
		{Name: "http", Type: contract.InterfaceTypeOpenAPI, Ref: "interfaces/bad.json"},
	}}
	fsys := fstest.MapFS{"interfaces/bad.json": {Data: []byte("{ this is not json")}}
	if tools := toolsFrom(c, fsys); tools != nil {
		t.Errorf("unreadable openapi should yield no tools, got %+v", tools)
	}
}

func TestToolsFrom_CapAtMax(t *testing.T) {
	c := &contract.Contract{Interfaces: []contract.Interface{
		{Name: "http", Type: contract.InterfaceTypeOpenAPI, Ref: "interfaces/openapi.json"},
	}}
	fsys := fstest.MapFS{"interfaces/openapi.json": {Data: []byte(bigOpenAPI(maxToolsPerRevision + 5))}}
	tools := toolsFrom(c, fsys)
	if len(tools) != maxToolsPerRevision {
		t.Fatalf("tools should be capped at %d, got %d", maxToolsPerRevision, len(tools))
	}
}

// bigOpenAPI generates a valid OpenAPI doc with n GET operations.
func bigOpenAPI(n int) string {
	var b strings.Builder
	b.WriteString(`{"openapi":"3.1.0","info":{"title":"big","version":"1.0.0"},"paths":{`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"/p%d":{"get":{"operationId":"op%d","summary":"s","responses":{"200":{"description":"ok"}}}}`, i, i)
	}
	b.WriteString(`}}`)
	return b.String()
}

// -------------------- docsFrom / humanizeTitle --------------------

func TestDocsFrom_NilFS(t *testing.T) {
	if docsFrom(nil) != nil {
		t.Error("nil fs → nil docs")
	}
}

func TestDocsFrom_AbsentDir(t *testing.T) {
	if docs := docsFrom(fstest.MapFS{"other/x.txt": {Data: []byte("x")}}); len(docs) != 0 {
		t.Errorf("absent docs/ → empty, got %+v", docs)
	}
}

func TestDocsFrom_FiltersAndHumanizes(t *testing.T) {
	fsys := fstest.MapFS{
		"docs/guide.md":              {Data: []byte("g")},
		"docs/notes.txt":             {Data: []byte("n")}, // non-md, skipped
		"docs/sub/deep-file_name.md": {Data: []byte("d")},
	}
	docs := docsFrom(fsys)
	if len(docs) != 2 {
		t.Fatalf("want 2 md docs, got %d: %+v", len(docs), docs)
	}
	titles := map[string]string{}
	for _, d := range docs {
		titles[d.Path] = d.Title
	}
	if titles["docs/guide.md"] != "guide" {
		t.Errorf("guide title = %q", titles["docs/guide.md"])
	}
	if titles["docs/sub/deep-file_name.md"] != "deep file name" {
		t.Errorf("humanized title = %q", titles["docs/sub/deep-file_name.md"])
	}
}

func TestDocsFrom_CapAtMax(t *testing.T) {
	fsys := fstest.MapFS{}
	for i := 0; i < maxDocRefs+10; i++ {
		fsys[fmt.Sprintf("docs/d%04d.md", i)] = &fstest.MapFile{Data: []byte("x")}
	}
	if docs := docsFrom(fsys); len(docs) != maxDocRefs {
		t.Fatalf("docs should cap at %d, got %d", maxDocRefs, len(docs))
	}
}

func TestHumanizeTitle(t *testing.T) {
	tests := map[string]string{
		"plain":              "plain",
		"docs/a/b/final.md":  "final",
		"my-doc_file":        "my doc file",
		"docs/one-two.three": "one two", // last "." strips ".three"
	}
	for in, want := range tests {
		if got := humanizeTitle(in); got != want {
			t.Errorf("humanizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// -------------------- lockFrom --------------------

func TestLockFrom_SuppliedLockWins(t *testing.T) {
	l := mustLock(t)
	got := lockFrom(RawRevision{Lock: l, Bundle: &contract.Bundle{FS: fstest.MapFS{}}})
	if got != l {
		t.Error("supplied lock must win")
	}
}

func TestLockFrom_NilFS(t *testing.T) {
	if lockFrom(RawRevision{Bundle: &contract.Bundle{}}) != nil {
		t.Error("nil FS → nil lock")
	}
}

func TestLockFrom_NoLockFile(t *testing.T) {
	if lockFrom(RawRevision{Bundle: &contract.Bundle{FS: fstest.MapFS{}}}) != nil {
		t.Error("no pacto.lock → nil")
	}
}

func TestLockFrom_MalformedLock(t *testing.T) {
	fsys := fstest.MapFS{lock.FileName: {Data: []byte("lockVersion: 999\n")}}
	if lockFrom(RawRevision{Bundle: &contract.Bundle{FS: fsys}}) != nil {
		t.Error("unsupported lockVersion → nil")
	}
}

func TestLockFrom_ValidLockFile(t *testing.T) {
	fsys := fstest.MapFS{lock.FileName: {Data: []byte(validLockYAML)}}
	got := lockFrom(RawRevision{Bundle: &contract.Bundle{FS: fsys}})
	if got == nil {
		t.Fatal("valid pacto.lock should parse")
	}
	if _, ok := got.Dependency("dep-svc"); !ok {
		t.Error("parsed lock missing dep-svc")
	}
}

// -------------------- targetFrom --------------------

func TestTargetFrom_DefaultComplianceUnknown_MissingEvidence(t *testing.T) {
	tgt := targetFrom(RawTarget{Scope: "prod", Kind: "k8s", Name: "web", Service: "web-svc"}, "k8s", fixedNow(), time.Hour)
	if tgt.Compliance != StatusUnknown {
		t.Errorf("empty compliance → Unknown, got %q", tgt.Compliance)
	}
	if !hasLimitation(tgt.Limitations, LimitationEvidenceMissing) {
		t.Errorf("missing evidence limitation expected: %+v", tgt.Limitations)
	}
	if tgt.Stale {
		t.Error("no evidence → not stale")
	}
}

func TestTargetFrom_Stale(t *testing.T) {
	old := fixedNow().Add(-2 * time.Hour)
	tgt := targetFrom(RawTarget{Name: "w", Service: "s", Compliance: StatusCompliant, EvidenceAt: &old,
		Limitations: []Limitation{{Code: "PRE", Message: "supplied"}}}, "k8s", fixedNow(), time.Hour)
	if !tgt.Stale {
		t.Error("evidence older than window → stale")
	}
	if !hasLimitation(tgt.Limitations, LimitationSourceStale) {
		t.Error("stale limitation expected")
	}
	if !hasLimitation(tgt.Limitations, "PRE") {
		t.Error("source-supplied limitation must be preserved")
	}
}

func TestTargetFrom_FreshCompliant(t *testing.T) {
	recent := fixedNow().Add(-1 * time.Minute)
	tgt := targetFrom(RawTarget{Name: "w", Service: "s", Compliance: StatusCompliant, EvidenceAt: &recent}, "k8s", fixedNow(), time.Hour)
	if tgt.Stale {
		t.Error("fresh evidence → not stale")
	}
	if len(tgt.Limitations) != 0 {
		t.Errorf("fresh compliant target should have no limitations: %+v", tgt.Limitations)
	}
}

func TestTargetFrom_WindowDisabled(t *testing.T) {
	old := fixedNow().Add(-1000 * time.Hour)
	tgt := targetFrom(RawTarget{Name: "w", Service: "s", Compliance: StatusCompliant, EvidenceAt: &old}, "k8s", fixedNow(), 0)
	if tgt.Stale {
		t.Error("window=0 disables staleness")
	}
}

func hasLimitation(ls []Limitation, code string) bool {
	for _, l := range ls {
		if l.Code == code {
			return true
		}
	}
	return false
}

// -------------------- matchRevision / linkTargets --------------------

func TestMatchRevision(t *testing.T) {
	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{
		"svc@d1":  {Key: "svc@d1", Service: "svc", Digest: "sha256:d1", ResolvedRef: "reg/svc@sha256:d1"},
		"svc@r2":  {Key: "svc@r2", Service: "svc", ResolvedRef: "reg/svc:2.0"},
		"other@x": {Key: "other@x", Service: "other", Digest: "sha256:zz"},
	}}
	byDigest := matchRevision(snap, &TargetRecord{Service: "svc", Digest: "sha256:d1"})
	if byDigest != "svc@d1" {
		t.Errorf("digest match = %q", byDigest)
	}
	byRef := matchRevision(snap, &TargetRecord{Service: "svc", ResolvedRef: "reg/svc:2.0"})
	if byRef != "svc@r2" {
		t.Errorf("resolvedRef match = %q", byRef)
	}
	unlinked := matchRevision(snap, &TargetRecord{Service: "svc", Digest: "sha256:none", ResolvedRef: "nope"})
	if unlinked != "" {
		t.Errorf("no match should be empty, got %q", unlinked)
	}
	// service with no revisions in fleet.
	if matchRevision(snap, &TargetRecord{Service: "ghost", Digest: "sha256:d1"}) != "" {
		t.Error("ghost service should not link")
	}
}

func TestMatchRevision_VersionFallback(t *testing.T) {
	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{
		"vsvc@2": {Key: "vsvc@2", Service: "vsvc", Version: "2.0.0"}, // no digest, no resolvedRef
	}}
	// Tag-pinned resolved ref links by version suffix (":version").
	byTag := matchRevision(snap, &TargetRecord{Service: "vsvc", ResolvedRef: "reg/vsvc:2.0.0"})
	if byTag != "vsvc@2" {
		t.Errorf("tag version fallback = %q", byTag)
	}
	// Digest-style "@version" suffix also links.
	byAt := matchRevision(snap, &TargetRecord{Service: "vsvc", ResolvedRef: "reg/vsvc@2.0.0"})
	if byAt != "vsvc@2" {
		t.Errorf("@ version fallback = %q", byAt)
	}
	// A ref that pins a different version does not link.
	if got := matchRevision(snap, &TargetRecord{Service: "vsvc", ResolvedRef: "reg/vsvc:9.9.9"}); got != "" {
		t.Errorf("mismatched version should not link, got %q", got)
	}
}

// -------------------- revisionNewer / representativeRevision --------------------

func TestRevisionNewer(t *testing.T) {
	tests := []struct {
		name string
		a, b *ContractRevision
		want bool
	}{
		{"a greater semver", &ContractRevision{Version: "2.0.0", Key: "a"}, &ContractRevision{Version: "1.0.0", Key: "b"}, true},
		{"a lesser semver", &ContractRevision{Version: "1.0.0", Key: "a"}, &ContractRevision{Version: "2.0.0", Key: "b"}, false},
		{"equal semver, key tiebreak", &ContractRevision{Version: "1.0.0", Key: "z"}, &ContractRevision{Version: "1.0.0", Key: "a"}, true},
		{"a parses b not", &ContractRevision{Version: "1.0.0", Key: "a"}, &ContractRevision{Version: "not-semver", Key: "b"}, true},
		{"b parses a not", &ContractRevision{Version: "nope", Key: "a"}, &ContractRevision{Version: "1.0.0", Key: "b"}, false},
		{"neither parses, key tiebreak", &ContractRevision{Version: "x", Key: "z"}, &ContractRevision{Version: "y", Key: "a"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := revisionNewer(tt.a, tt.b); got != tt.want {
				t.Errorf("revisionNewer = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRepresentativeRevision(t *testing.T) {
	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{
		"svc@1": {Key: "svc@1", Service: "svc", Version: "1.0.0"},
		"svc@2": {Key: "svc@2", Service: "svc", Version: "2.0.0"},
	}}
	s := &ServiceRecord{Revisions: []RevisionKey{"svc@1", "svc@2"}}
	rep := representativeRevision(snap, s)
	if rep == nil || rep.Version != "2.0.0" {
		t.Errorf("representative should be highest semver, got %+v", rep)
	}
	// no revisions → nil.
	if representativeRevision(snap, &ServiceRecord{}) != nil {
		t.Error("no revisions → nil representative")
	}
}

// -------------------- resolveDepService / resolveRefService --------------------

func TestResolveDepService(t *testing.T) {
	snap := &FleetSnapshot{Services: map[ServiceKey]*ServiceRecord{
		NewServiceKey("payments"): {Name: "payments"},
		NewServiceKey("redis"):    {Name: "redis"},
	}}
	if r, ok := resolveDepService(snap, contract.Dependency{Name: "payments"}); !ok || r != "payments" {
		t.Errorf("direct resolve = %q,%v", r, ok)
	}
	if r, ok := resolveDepService(snap, contract.Dependency{Name: "redis-pacto"}); !ok || r != "redis" {
		t.Errorf("-pacto strip resolve = %q,%v", r, ok)
	}
	if _, ok := resolveDepService(snap, contract.Dependency{Name: "ghost"}); ok {
		t.Error("ghost should not resolve")
	}
	// "-pacto" strip that still does not resolve.
	if _, ok := resolveDepService(snap, contract.Dependency{Name: "missing-pacto"}); ok {
		t.Error("stripped name absent → unresolved")
	}
}

func TestResolveRefService(t *testing.T) {
	snap := &FleetSnapshot{Services: map[ServiceKey]*ServiceRecord{NewServiceKey("cfg"): {Name: "cfg"}}}
	if r, ok := resolveRefService(snap, contract.ReferenceRef{Name: "cfg"}); !ok || r != "cfg" {
		t.Errorf("ref resolve = %q,%v", r, ok)
	}
	if _, ok := resolveRefService(snap, contract.ReferenceRef{Name: "nope"}); ok {
		t.Error("unknown ref should not resolve")
	}
}

// -------------------- classifyCompleteness / degradedLimitations --------------------

func TestClassifyCompleteness(t *testing.T) {
	partial := &FleetSnapshot{Sources: []SourceState{{Status: SourceUnavailable}}}
	if classifyCompleteness(partial) != CompletenessPartial {
		t.Error("unavailable source → partial")
	}
	empty := &FleetSnapshot{Sources: []SourceState{{Status: SourceAvailable}}, Services: map[ServiceKey]*ServiceRecord{}, Targets: map[TargetKey]*TargetRecord{}}
	if classifyCompleteness(empty) != CompletenessEmpty {
		t.Error("no records, all available → empty")
	}
	complete := &FleetSnapshot{Sources: []SourceState{{Status: SourceAvailable}}, Services: map[ServiceKey]*ServiceRecord{NewServiceKey("a"): {}}, Targets: map[TargetKey]*TargetRecord{}}
	if classifyCompleteness(complete) != CompletenessComplete {
		t.Error("records + available → complete")
	}
}

func TestDegradedLimitations(t *testing.T) {
	snap := &FleetSnapshot{Sources: []SourceState{
		{ID: "s1", Status: SourceStale},
		{ID: "s2", Status: SourcePartial},
		{ID: "s3", Status: SourceAvailable},
	}}
	ls := degradedLimitations(snap)
	if !hasLimitation(ls, LimitationSourceStale) || !hasLimitation(ls, LimitationSourcePartial) {
		t.Errorf("stale+partial limitations expected: %+v", ls)
	}
	if len(ls) != 2 {
		t.Errorf("available source must not add a limitation: %+v", ls)
	}
}

// -------------------- serviceStatus (direct, incl. defensive default) --------------------

func TestServiceStatus_Direct(t *testing.T) {
	// Empty service record (no targets, no revisions) — defensive Unknown default,
	// unreachable via Build but exercised directly here.
	empty := &FleetSnapshot{Targets: map[TargetKey]*TargetRecord{}, Revisions: map[RevisionKey]*ContractRevision{}}
	if got := serviceStatus(empty, &ServiceRecord{}); got != StatusUnknown {
		t.Errorf("empty service → Unknown, got %q", got)
	}
}

// -------------------- appendUnique --------------------

func TestAppendUnique(t *testing.T) {
	if got := appendUnique(nil, ""); got != nil {
		t.Errorf("empty value → unchanged nil, got %v", got)
	}
	base := []string{"a"}
	if got := appendUnique(base, "a"); len(got) != 1 {
		t.Errorf("duplicate → unchanged, got %v", got)
	}
	if got := appendUnique(base, "b"); len(got) != 2 {
		t.Errorf("new value → appended, got %v", got)
	}
}

// -------------------- Build integration --------------------

func TestBuild_EmptySources(t *testing.T) {
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Completeness != CompletenessEmpty {
		t.Errorf("no sources → empty, got %q", snap.Completeness)
	}
	if !snap.GeneratedAt.Equal(fixedNow()) {
		t.Error("GeneratedAt should use injected clock")
	}
	// Zero configured sources is explicit, not silent.
	if !hasLimitation(snap.Limitations, LimitationNoSourcesConfigured) {
		t.Errorf("empty-sources build should carry NO_SOURCES_CONFIGURED: %+v", snap.Limitations)
	}
	if snap.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", snap.SchemaVersion, SchemaVersion)
	}
}

func TestBuild_DuplicateSourceID(t *testing.T) {
	// Two sources declaring the same id → a DUPLICATE_SOURCE_ID limitation.
	a := NewMemorySource("dup", "local", &Collection{Revisions: []RawRevision{{Bundle: validLeafBundle(t), Digest: "sha256:leaf"}}})
	b := NewMemorySource("dup", "oci", &Collection{Revisions: []RawRevision{{Bundle: bundleFor(t, "other"), Digest: "sha256:other"}}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLimitation(snap.Limitations, LimitationDuplicateSourceID) {
		t.Errorf("duplicate source id should be surfaced: %+v", snap.Limitations)
	}
}

func TestBuild_CollectionLimitations_MarkSourcePartial(t *testing.T) {
	// A source that reports record-level problems keeps its usable records AND is
	// marked partial, so completeness degrades and the problems reach the snapshot.
	col := &Collection{
		Revisions:   []RawRevision{{Bundle: validLeafBundle(t), Digest: "sha256:leaf"}},
		Limitations: []Limitation{{Code: LimitationSourceRecordInvalid, Source: "local", Message: "a record was skipped"}},
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Completeness != CompletenessPartial {
		t.Errorf("record-level problems → partial, got %q", snap.Completeness)
	}
	if !hasLimitation(snap.Limitations, LimitationSourceRecordInvalid) {
		t.Errorf("source-supplied limitation should reach the snapshot: %+v", snap.Limitations)
	}
	var found bool
	for _, s := range snap.Sources {
		if s.ID == "local" {
			found = true
			if s.Status != SourcePartial {
				t.Errorf("source with limitations → partial, got %q", s.Status)
			}
		}
	}
	if !found {
		t.Error("local source state missing")
	}
	// The usable revision survives.
	if snap.Service("leaf-svc") == nil {
		t.Error("partial source should keep its usable records")
	}
}

func TestBuild_SnapshotIDDeterministic(t *testing.T) {
	// Same inputs + fixed clock → identical SnapshotID across builds.
	mk := func() (*FleetSnapshot, error) {
		col := &Collection{
			Revisions: []RawRevision{{Bundle: validLeafBundle(t), Digest: "sha256:leaf"}},
			Targets:   []RawTarget{{Scope: "prod", Kind: "k8s", Name: "web", Service: "leaf-svc", Digest: "sha256:leaf", Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow())}},
		}
		return Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", col))
	}
	first, err := mk()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.SnapshotID, "sha256:") || first.SnapshotID == "sha256:" {
		t.Errorf("SnapshotID should be a sha256 digest, got %q", first.SnapshotID)
	}
	second, err := mk()
	if err != nil {
		t.Fatal(err)
	}
	if first.SnapshotID != second.SnapshotID {
		t.Errorf("SnapshotID not deterministic: %q != %q", first.SnapshotID, second.SnapshotID)
	}
	// A different fleet yields a different id.
	other, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{{Bundle: bundleFor(t, "diff"), Digest: "sha256:diff"}}}))
	if err != nil {
		t.Fatal(err)
	}
	if other.SnapshotID == first.SnapshotID {
		t.Error("distinct fleets should have distinct snapshot ids")
	}
}

func TestComputeSnapshotID_MarshalFailure(t *testing.T) {
	// An un-marshalable value (a channel in a target's observed runtime) forces the
	// defensive marshal-error path to a stable marker rather than a partial hash.
	snap := &FleetSnapshot{
		Targets: map[TargetKey]*TargetRecord{
			"prod/k8s/x": {Key: "prod/k8s/x", Name: "x", ObservedRuntime: map[string]any{"bad": make(chan int)}},
		},
	}
	if got := computeSnapshotID(snap); got != "sha256:unavailable" {
		t.Errorf("marshal failure → sha256:unavailable, got %q", got)
	}
}

func TestBuild_SingleMemorySource(t *testing.T) {
	col := &Collection{Revisions: []RawRevision{{Bundle: validLeafBundle(t), Digest: "sha256:leaf"}}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Completeness != CompletenessComplete {
		t.Errorf("records + available → complete, got %q", snap.Completeness)
	}
	if snap.Service("leaf-svc") == nil {
		t.Error("leaf-svc should be aggregated")
	}
}

func TestBuild_RevisionAndTargetDedup(t *testing.T) {
	rev := RawRevision{Bundle: validLeafBundle(t), Digest: "sha256:leaf"}
	tgt := RawTarget{Scope: "prod", Kind: "k8s", Name: "web", Service: "leaf-svc", Digest: "sha256:leaf", Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow())}
	src1 := NewMemorySource("a", "local", &Collection{Revisions: []RawRevision{rev}, Targets: []RawTarget{tgt}})
	// Second source repeats the same revision key and target key: first wins.
	rev2 := RawRevision{Bundle: validLeafBundle(t), Digest: "sha256:leaf"}
	src2 := NewMemorySource("b", "oci", &Collection{Revisions: []RawRevision{rev2}, Targets: []RawTarget{tgt}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src1, src2)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Revisions) != 1 {
		t.Errorf("revision should be deduped to 1, got %d", len(snap.Revisions))
	}
	if len(snap.Targets) != 1 {
		t.Errorf("target should be deduped to 1, got %d", len(snap.Targets))
	}
	// First source (a) wins the revision.
	for _, r := range snap.Revisions {
		if r.Source != "a" {
			t.Errorf("first source should win, got %q", r.Source)
		}
	}
	// Source b reported 0 net-new revisions.
	for _, s := range snap.Sources {
		if s.ID == "b" && s.RevisionCount != 0 {
			t.Errorf("dedup source b should report 0 revisions, got %d", s.RevisionCount)
		}
	}
}

func TestBuild_FailingSource_AllowPartialDefault(t *testing.T) {
	good := NewMemorySource("good", "local", &Collection{Revisions: []RawRevision{{Bundle: validLeafBundle(t), Digest: "sha256:leaf"}}})
	bad := NewFailingSource("bad", "oci", errors.New("401 unauthorized secret=xyz"))
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, good, bad)
	if err != nil {
		t.Fatalf("AllowPartial default should not fail: %v", err)
	}
	if snap.Completeness != CompletenessPartial {
		t.Errorf("failed source → partial, got %q", snap.Completeness)
	}
	if !hasLimitation(snap.Limitations, LimitationSourceUnavailable) {
		t.Error("unavailable limitation expected")
	}
	// The good source's records survive.
	if snap.Service("leaf-svc") == nil {
		t.Error("partial fleet should keep the healthy source's records")
	}
	// Unavailable source state is sanitized.
	for _, s := range snap.Sources {
		if s.ID == "bad" {
			if s.Status != SourceUnavailable || s.Error == nil || strings.Contains(s.Error.Message, "xyz") {
				t.Errorf("bad source state not sanitized: %+v", s)
			}
		}
	}
}

func TestBuild_FailingSource_DisallowPartial(t *testing.T) {
	bad := NewFailingSource("bad", "oci", errors.New("registry down"))
	_, err := Build(context.Background(), BuildOptions{Now: fixedNow, DisallowPartial: true}, bad)
	if err == nil {
		t.Fatal("DisallowPartial should make a source failure fatal")
	}
}

func TestBuild_PreCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Build(ctx, BuildOptions{Now: fixedNow}, NewMemorySource("s", "mem", &Collection{}))
	if err == nil {
		t.Fatal("pre-cancelled context must be fatal")
	}
}

func TestBuild_CancelDuringCollect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := NewMemorySource("s", "mem", nil).WithCollectFunc(func(c context.Context) (*Collection, error) {
		cancel() // cancel the parent context, but succeed
		return &Collection{}, nil
	})
	_, err := Build(ctx, BuildOptions{Now: fixedNow}, src)
	if err == nil {
		t.Fatal("cancellation observed after collect must be fatal")
	}
}

func TestBuild_CollectFuncReturnsCtxErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := NewMemorySource("s", "mem", nil).WithCollectFunc(func(c context.Context) (*Collection, error) {
		cancel()
		return nil, c.Err()
	})
	_, err := Build(ctx, BuildOptions{Now: fixedNow}, src)
	if err == nil {
		t.Fatal("collect returning ctx error under cancellation must be fatal")
	}
}

func TestBuild_ConcurrencyOptions(t *testing.T) {
	mk := func(id string) Source {
		return NewMemorySource(id, "local", &Collection{Revisions: []RawRevision{
			{Bundle: bundleFor(t, id), Digest: "sha256:" + id},
		}})
	}
	// Concurrency 1 (serialized).
	snap1, err := Build(context.Background(), BuildOptions{Now: fixedNow, Concurrency: 1}, mk("s1"), mk("s2"), mk("s3"))
	if err != nil {
		t.Fatal(err)
	}
	// Concurrency greater than source count (clamped to len).
	snapN, err := Build(context.Background(), BuildOptions{Now: fixedNow, Concurrency: 99}, mk("s1"), mk("s2"))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap1.Services) != 3 || len(snapN.Services) != 2 {
		t.Errorf("service counts = %d / %d", len(snap1.Services), len(snapN.Services))
	}
}

func TestBuild_NilCollection(t *testing.T) {
	// A source returning (nil, nil) must be treated as an empty collection.
	src := NewMemorySource("s", "mem", nil).WithCollectFunc(func(c context.Context) (*Collection, error) {
		return nil, nil
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Services) != 0 {
		t.Error("nil collection → no services")
	}
}

func TestBuild_NilRevisionSkipped(t *testing.T) {
	// A RawRevision with no bundle projects to nil and is skipped.
	col := &Collection{Revisions: []RawRevision{
		{}, // nil bundle → skipped
		{Bundle: validLeafBundle(t), Digest: "sha256:leaf"},
	}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Revisions) != 1 {
		t.Errorf("nil revision must be skipped, got %d revisions", len(snap.Revisions))
	}
}

func TestBuild_MultipleLimitationsSorted(t *testing.T) {
	// Two failing sources (same SOURCE_UNAVAILABLE code, different source id) plus a
	// stale source (different code) exercise the limitation sort comparator fully.
	f1 := NewFailingSource("f1", "oci", errors.New("boom"))
	f2 := NewFailingSource("f2", "oci", errors.New("boom"))
	stale := NewMemorySource("cache", "cache", &Collection{
		Revisions: []RawRevision{{Bundle: validLeafBundle(t), Digest: "sha256:leaf"}},
		State:     &SourceState{Status: SourceStale},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, f1, f2, stale)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Limitations) < 3 {
		t.Fatalf("expected >=3 limitations, got %d: %+v", len(snap.Limitations), snap.Limitations)
	}
	// Deterministically ordered by Code then Source.
	for i := 1; i < len(snap.Limitations); i++ {
		a, b := snap.Limitations[i-1], snap.Limitations[i]
		if a.Code > b.Code || (a.Code == b.Code && a.Source > b.Source) {
			t.Errorf("limitations not sorted at %d: %+v then %+v", i, a, b)
		}
	}
}

func TestBuild_DuplicateDepEdgeSort(t *testing.T) {
	// Two dependencies sharing a name but differing in ref produce two edges with
	// identical From/Type/To, forcing the RequestedRef tiebreak in the sort.
	c := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "dupdep", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "z", Ref: "oci://b/z", Required: true, Compatibility: "^1.0.0"},
			{Name: "z", Ref: "oci://a/z", Required: true, Compatibility: "^1.0.0"},
		},
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local",
		&Collection{Revisions: []RawRevision{{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:dd"}}}))
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, rel := range snap.Relationships {
		if rel.From == "dupdep" && rel.To == "z" {
			refs = append(refs, rel.RequestedRef)
		}
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 dupdep→z edges, got %d", len(refs))
	}
	if refs[0] > refs[1] {
		t.Errorf("tied edges should be ordered by RequestedRef: %v", refs)
	}
}

func TestBuild_SuppliedSourceState_Stale(t *testing.T) {
	// A source that succeeded but reports itself stale must degrade completeness.
	col := &Collection{
		Revisions: []RawRevision{{Bundle: validLeafBundle(t), Digest: "sha256:leaf"}},
		State:     &SourceState{Status: SourceStale},
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("cache", "cache", col))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Completeness != CompletenessPartial {
		t.Errorf("stale source → partial, got %q", snap.Completeness)
	}
	if !hasLimitation(snap.Limitations, LimitationSourceStale) {
		t.Error("stale limitation expected")
	}
}

// bundleFor builds a distinct leaf-style bundle for service name id. It carries a
// non-nil (empty) FS because revisionFrom → skills.List panics on a nil FS.
func bundleFor(t *testing.T, id string) *contract.Bundle {
	t.Helper()
	c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: id, Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}
	return &contract.Bundle{Contract: c, FS: fstest.MapFS{}}
}

// -------------------- relationships via Build --------------------

// relFrom finds the first relationship with the given from/to endpoints.
func relFrom(rels []Relationship, from, to string) *Relationship {
	for i := range rels {
		if rels[i].From == from && rels[i].To == to {
			return &rels[i]
		}
	}
	return nil
}

// buildRelSnapshot builds a snapshot exercising resolved, unresolved, reference
// and lock-pinned edges from a single "web" service.
func buildRelSnapshot(t *testing.T) *FleetSnapshot {
	t.Helper()
	web := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "web", Version: "1.0.0", Owner: contract.Owner{Team: "web-team"}},
		Dependencies: []contract.Dependency{
			{Name: "leaf-svc", Ref: "oci://ex/leaf", Required: true, Compatibility: "^1.0.0"},
			{Name: "ghost", Ref: "oci://ex/ghost", Required: false, Compatibility: "^1.0.0"},
		},
		// resolveRefService matches the reference's declared Name against service
		// keys, so name the config after the service it should resolve to.
		Configurations: []contract.Configuration{{Name: "cfg-svc", Ref: "oci://ex/cfg-svc"}},
	}
	webRev := RawRevision{Bundle: &contract.Bundle{Contract: web, FS: fstest.MapFS{}}, Digest: "sha256:web", Lock: mustLock(t)}
	leafRev := RawRevision{Bundle: validLeafBundle(t), Digest: "sha256:leaf"}
	cfgRev := RawRevision{Bundle: bundleFor(t, "cfg-svc"), Digest: "sha256:cfg"}
	// mustLock names dep "dep-svc"; add a dependency+lock alignment case too:
	web.Dependencies = append(web.Dependencies, contract.Dependency{Name: "dep-svc", Ref: "oci://ex/dep", Required: true, Compatibility: "^2.0.0"})
	depRev := RawRevision{Bundle: bundleFor(t, "dep-svc"), Digest: "sha256:dep"}

	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{webRev, leafRev, cfgRev, depRev}}))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestBuild_RelationshipsResolution(t *testing.T) {
	snap := buildRelSnapshot(t)
	resolvedDep := relFrom(snap.Relationships, "web", "leaf-svc")
	if resolvedDep == nil || !resolvedDep.Resolved || resolvedDep.ResolvedService != "leaf-svc" {
		t.Errorf("resolved dependency edge wrong: %+v", resolvedDep)
	}
	unresolvedDep := relFrom(snap.Relationships, "web", "ghost")
	if unresolvedDep == nil || unresolvedDep.Resolved || unresolvedDep.Reason == "" {
		t.Errorf("unresolved dependency edge should carry a reason: %+v", unresolvedDep)
	}
	// Forward/reverse indexes populated for resolved edges.
	if len(snap.forwardDeps["web"]) == 0 {
		t.Error("forwardDeps[web] should be populated")
	}
	if len(snap.reverseDeps["leaf-svc"]) == 0 {
		t.Error("reverseDeps[leaf-svc] should include web")
	}
}

func TestBuild_RelationshipsRefAndLock(t *testing.T) {
	snap := buildRelSnapshot(t)
	refEdge := relFrom(snap.Relationships, "web", "cfg-svc")
	if refEdge == nil || refEdge.Type != "reference" || !refEdge.Resolved {
		t.Errorf("config reference edge wrong: %+v", refEdge)
	}
	lockedDep := relFrom(snap.Relationships, "web", "dep-svc")
	if lockedDep == nil || lockedDep.LockedDigest != "sha256:deadbeef" || lockedDep.LockedVersion != "2.0.0" {
		t.Errorf("locked dependency should carry lock digest/version: %+v", lockedDep)
	}
}

func TestBuild_TwoRevisionsRepresentative(t *testing.T) {
	c1 := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "svc", Version: "1.0.0"},
		Dependencies: []contract.Dependency{{Name: "old-dep", Ref: "oci://x/o", Required: true, Compatibility: "^1.0.0"}}}
	c2 := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "svc", Version: "2.0.0"},
		Dependencies: []contract.Dependency{{Name: "new-dep", Ref: "oci://x/n", Required: true, Compatibility: "^1.0.0"}}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local",
		&Collection{Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: c1, FS: fstest.MapFS{}}, Digest: "sha256:1"},
			{Bundle: &contract.Bundle{Contract: c2, FS: fstest.MapFS{}}, Digest: "sha256:2"},
		}}))
	if err != nil {
		t.Fatal(err)
	}
	// Representative (v2.0.0) sources edges → new-dep present, old-dep absent.
	var sawNew, sawOld bool
	for _, rel := range snap.Relationships {
		if rel.To == "new-dep" {
			sawNew = true
		}
		if rel.To == "old-dep" {
			sawOld = true
		}
	}
	if !sawNew || sawOld {
		t.Errorf("representative revision should source edges from v2.0.0 only (new=%v old=%v)", sawNew, sawOld)
	}
}

// -------------------- service status via Build --------------------

func TestBuild_ServiceStatuses(t *testing.T) {
	now := ptrTime(fixedNow())
	cols := &Collection{
		Revisions: []RawRevision{
			// invalid revision → Invalid service (no targets).
			{Bundle: &contract.Bundle{Contract: mustParse(t, invalidYAML), RawYAML: []byte(invalidYAML), FS: fstest.MapFS{}}},
			// valid revision, no targets → NotEvaluated.
			{Bundle: validLeafBundle(t), Digest: "sha256:leaf"},
			// revision for a compliant service.
			{Bundle: bundleFor(t, "ok-svc"), Digest: "sha256:ok"},
			// revision for a noncompliant service.
			{Bundle: bundleFor(t, "bad-tgt-svc"), Digest: "sha256:bt"},
			// revision for an unknown service.
			{Bundle: bundleFor(t, "unk-svc"), Digest: "sha256:uk"},
		},
		Targets: []RawTarget{
			{Name: "ok", Service: "ok-svc", Digest: "sha256:ok", Compliance: StatusCompliant, EvidenceAt: now},
			{Name: "bad", Service: "bad-tgt-svc", Digest: "sha256:bt", Compliance: StatusNonCompliant, EvidenceAt: now},
			{Name: "unk", Service: "unk-svc", Digest: "sha256:uk", Compliance: StatusUnknown, EvidenceAt: now},
		},
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", cols))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"bad-svc":     StatusInvalid,
		"leaf-svc":    StatusNotEvaluated,
		"ok-svc":      StatusCompliant,
		"bad-tgt-svc": StatusNonCompliant,
		"unk-svc":     StatusUnknown,
	}
	for name, status := range want {
		if got := snap.Service(name).Status; got != status {
			t.Errorf("service %q status = %q, want %q", name, got, status)
		}
	}
}

func TestBuild_OwnerAndLabelBackfill(t *testing.T) {
	c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "svc", Version: "1.0.0", Owner: contract.Owner{Team: "owners"}}}
	col := &Collection{
		Revisions: []RawRevision{{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:1"}},
		Targets:   []RawTarget{{Name: "t", Service: "svc", Digest: "sha256:1", Labels: map[string]string{"env": "prod"}, Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow())}},
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	s := snap.Service("svc")
	if s.Owner.Team != "owners" {
		t.Errorf("owner should backfill from revision, got %+v", s.Owner)
	}
	if s.Labels["env"] != "prod" {
		t.Errorf("labels should union from targets, got %+v", s.Labels)
	}
}

// -------------------- immutability --------------------

func TestBuild_DoesNotMutateInputContract(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "svc", Version: "1.0.0", Owner: contract.Owner{Team: "orig"}},
		Dependencies: []contract.Dependency{{Name: "d", Ref: "oci://x/d", Required: true, Compatibility: "^1.0.0"}},
	}
	raw := RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:1"}
	origOwner := c.Service.Owner
	origDepCount := len(c.Dependencies)

	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{raw}}))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Service.Owner.Equal(origOwner) || len(c.Dependencies) != origDepCount {
		t.Error("Build must not mutate the input contract's fields")
	}
	// The snapshot references the same contract pointer (no defensive copy expected).
	if snap.Service("svc") == nil {
		t.Fatal("service missing")
	}
	for _, r := range snap.Revisions {
		if r.Contract != c {
			t.Error("revision should reference the input contract pointer")
		}
		if r.bundle != raw.Bundle {
			t.Error("revision should reference the input bundle pointer")
		}
	}
}

// ensure finding import is used (targets carry findings in query tests, but keep
// this package-level reference explicit and cheap).
var _ = finding.SeverityError
