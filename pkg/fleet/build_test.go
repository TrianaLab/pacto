package fleet

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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
	st, _ := sourceStateFor(src, &Collection{}, fixedNow(), 3, 4, false)
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
	st, _ := sourceStateFor(src, col, fixedNow(), 1, 2, false)
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
	st, _ := sourceStateFor(src, col, fixedNow(), 0, 0, false)
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
	if rev, _ := revisionFrom(RawRevision{}, "src", fixedNow()); rev != nil {
		t.Error("nil bundle must project to nil")
	}
	if rev, _ := revisionFrom(RawRevision{Bundle: &contract.Bundle{}}, "src", fixedNow()); rev != nil {
		t.Error("bundle with nil contract must project to nil")
	}
}

func TestRevisionFrom_ValidAndInvalidYAML(t *testing.T) {
	valid, validLims := revisionFrom(RawRevision{Bundle: validLeafBundle(t), Digest: "sha256:leaf"}, "local", fixedNow())
	if !valid.Valid {
		t.Errorf("valid bundle should be Valid; findings=%+v", valid.Validation)
	}
	if valid.Key != NewRevisionKey(NewServiceKey("leaf-svc"), "sha256:leaf") {
		t.Errorf("unexpected key %q", valid.Key)
	}
	// A source-pinned digest is an immutable identity, so no unresolved limitation.
	if len(validLims) != 0 {
		t.Errorf("digest-pinned revision should have no identity limitation, got %+v", validLims)
	}

	badBundle := &contract.Bundle{Contract: mustParse(t, invalidYAML), RawYAML: []byte(invalidYAML), FS: fstest.MapFS{}}
	bad, badLims := revisionFrom(RawRevision{Bundle: badBundle}, "local", fixedNow())
	if bad.Valid {
		t.Error("invalid bundle should be !Valid")
	}
	if len(bad.Validation) == 0 {
		t.Error("invalid bundle should carry findings")
	}
	// No digest was pinned, so identity is a derived content digest and the
	// revision is flagged REVISION_IDENTITY_UNRESOLVED.
	if len(badLims) != 1 || badLims[0].Code != LimitationRevisionUnresolved {
		t.Errorf("no-digest revision must return REVISION_IDENTITY_UNRESOLVED, got %+v", badLims)
	}
	if !strings.Contains(string(bad.Key), "@sha256:") {
		t.Errorf("no-digest revision key should be content-addressed, got %q", bad.Key)
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
	rev, _ := revisionFrom(RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fsys}}, "local", fixedNow())
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
	c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "bare", Version: "1.0.0"}}
	rev, _ := revisionFrom(RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}}, "local", fixedNow())
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

func TestRevisionFrom_NilFS(t *testing.T) {
	// A bundle with a contract but a nil FS must not panic: skills.List is guarded
	// by an `if b.FS != nil` check (like toolsFrom/docsFrom/lockFrom). This exercises
	// that false branch — no tools/docs/skills/lock are derived.
	c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "nofs", Version: "1.0.0"}}
	rev, _ := revisionFrom(RawRevision{Bundle: &contract.Bundle{Contract: c}}, "local", fixedNow())
	if rev == nil {
		t.Fatal("nil-FS bundle should still project a revision")
	}
	if rev.Tools != nil || rev.Docs != nil || rev.Skills != nil || rev.Lock != nil {
		t.Errorf("nil FS → no derived projections, got %+v", rev)
	}
}

// -------------------- toolsFrom --------------------

func TestToolsFrom_NilFS(t *testing.T) {
	if tools, specs := toolsFrom(&contract.Contract{}, nil); tools != nil || specs != nil {
		t.Error("nil fs → nil tools and no specs read")
	}
}

func TestToolsFrom_SingleInterface_SummaryFallback(t *testing.T) {
	c := &contract.Contract{Interfaces: []contract.Interface{
		{Name: "http", Type: contract.InterfaceTypeOpenAPI, Ref: "interfaces/openapi.json"},
	}}
	fsys := fstest.MapFS{"interfaces/openapi.json": {Data: []byte(smallOpenAPI)}}
	tools, specsRead := toolsFrom(c, fsys)
	if len(specsRead) != 1 || specsRead[0] != "http" {
		t.Errorf("readable openapi spec should be recorded, got %v", specsRead)
	}
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
	tools, specsRead := toolsFrom(c, fsys)
	if len(specsRead) != 2 {
		t.Errorf("only the two readable openapi specs should be recorded, got %v", specsRead)
	}
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
	tools, specsRead := toolsFrom(c, fsys)
	if tools != nil {
		t.Errorf("unreadable openapi should yield no tools, got %+v", tools)
	}
	if specsRead != nil {
		t.Errorf("unreadable openapi must NOT be recorded as read, got %v", specsRead)
	}
}

func TestToolsFrom_CapAtMax(t *testing.T) {
	c := &contract.Contract{Interfaces: []contract.Interface{
		{Name: "http", Type: contract.InterfaceTypeOpenAPI, Ref: "interfaces/openapi.json"},
	}}
	fsys := fstest.MapFS{"interfaces/openapi.json": {Data: []byte(bigOpenAPI(maxToolsPerRevision + 5))}}
	tools, _ := toolsFrom(c, fsys)
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

// -------------------- sbomSummary --------------------

// spdxDoc renders a minimal SPDX 2.3 document from name -> license pairs. An empty
// license means the package declares none.
func spdxDoc(pkgs [][2]string) string {
	parts := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		parts = append(parts, fmt.Sprintf(`{"name":%q,"versionInfo":"1.0.0","licenseConcluded":%q}`, p[0], p[1]))
	}
	return `{"packages":[` + strings.Join(parts, ",") + `]}`
}

// The SBOM is deliberately not retained whole, so the summary carries the whole
// burden of the inventory: the exact package count and license buckets that
// PARTITION that count. A package declaring no license is a real answer ("we do
// not know") and gets its own bucket rather than disappearing from a distribution
// that claims to cover everything.
func TestSBOMSummary_BucketsPartitionThePackagePopulation(t *testing.T) {
	fsys := fstest.MapFS{"sbom/sbom.spdx.json": {Data: []byte(spdxDoc([][2]string{
		{"a", "MIT"}, {"b", "MIT"}, {"c", "Apache-2.0"}, {"d", ""}, {"e", "  "},
	}))}}
	s, err := sbomSummary(fsys)
	if err != nil {
		t.Fatalf("sbomSummary: %v", err)
	}
	if s.Format != "spdx" || s.Packages != 5 {
		t.Fatalf("summary = %+v, want spdx with 5 packages", s)
	}
	want := []LicenseCount{{License: "MIT", Count: 2}, {License: "unspecified", Count: 2}, {License: "Apache-2.0", Count: 1}}
	if len(s.Licenses) != len(want) {
		t.Fatalf("licenses = %+v, want %+v", s.Licenses, want)
	}
	sum := 0
	for i, b := range s.Licenses {
		if b != want[i] {
			t.Errorf("bucket %d = %+v, want %+v (most common first, ties by name)", i, b, want[i])
		}
		sum += b.Count
	}
	if sum+s.OtherLicensed != s.Packages {
		t.Errorf("buckets cover %d packages, want the whole %d", sum+s.OtherLicensed, s.Packages)
	}
}

// A pathological inventory must not put an unbounded license list in the snapshot,
// and truncating it must not quietly shrink the population: the tail is folded into
// OtherLicensed so buckets + other still equal Packages.
func TestSBOMSummary_FoldsTheLongTailInsteadOfDroppingIt(t *testing.T) {
	var pkgs [][2]string
	for i := 0; i < maxSBOMLicenses+7; i++ {
		pkgs = append(pkgs, [2]string{fmt.Sprintf("p%02d", i), fmt.Sprintf("lic-%02d", i)})
	}
	s, err := sbomSummary(fstest.MapFS{"sbom/sbom.spdx.json": {Data: []byte(spdxDoc(pkgs))}})
	if err != nil {
		t.Fatalf("sbomSummary: %v", err)
	}
	if len(s.Licenses) != maxSBOMLicenses {
		t.Fatalf("licenses = %d, want the bound %d", len(s.Licenses), maxSBOMLicenses)
	}
	if s.OtherLicensed != 7 {
		t.Errorf("OtherLicensed = %d, want the 7 packages past the bound", s.OtherLicensed)
	}
	sum := s.OtherLicensed
	for _, b := range s.Licenses {
		sum += b.Count
	}
	if sum != s.Packages {
		t.Errorf("buckets + other = %d, want Packages %d", sum, s.Packages)
	}
}

func TestSBOMSummary_AbsentBundleInventoryIsNotAnError(t *testing.T) {
	for name, fsys := range map[string]fs.FS{
		"nil FS":     nil,
		"no sbom/":   fstest.MapFS{"docs/x.md": {Data: []byte("x")}},
		"no sbom in": fstest.MapFS{"sbom/README.txt": {Data: []byte("x")}},
	} {
		s, err := sbomSummary(fsys)
		if s != nil || err != nil {
			t.Errorf("%s: got (%+v, %v), want (nil, nil)", name, s, err)
		}
	}
}

// "We could not read the inventory" is not "there is no inventory". The revision
// carries no summary either way, so the difference has to live in a limitation.
func TestRevisionFrom_UnreadableSBOMBecomesALimitationNotSilence(t *testing.T) {
	c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "broken-sbom", Version: "1.0.0"}}
	fsys := fstest.MapFS{"sbom/sbom.spdx.json": {Data: []byte("{not json")}}
	rev, lims := revisionFrom(RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fsys}, Digest: "sha256:x"}, "local", fixedNow())
	if rev.SBOM != nil {
		t.Errorf("an unparseable SBOM must not produce a summary: %+v", rev.SBOM)
	}
	var found *Limitation
	for i := range lims {
		if lims[i].Code == LimitationSBOMUnreadable {
			found = &lims[i]
		}
	}
	if found == nil {
		t.Fatalf("want a %s limitation, got %+v", LimitationSBOMUnreadable, lims)
	}
	if !strings.Contains(found.Message, "unknown, not empty") {
		t.Errorf("the limitation must say the inventory is unknown rather than empty: %q", found.Message)
	}
}

func TestRevisionFrom_ProjectsTheBundleSBOM(t *testing.T) {
	c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "with-sbom", Version: "1.0.0"}}
	fsys := fstest.MapFS{"sbom/sbom.spdx.json": {Data: []byte(spdxDoc([][2]string{{"a", "MIT"}}))}}
	rev, _ := revisionFrom(RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fsys}, Digest: "sha256:x"}, "local", fixedNow())
	if rev.SBOM == nil || rev.SBOM.Packages != 1 || rev.SBOM.Format != "spdx" {
		t.Fatalf("SBOM = %+v, want the projected spdx summary", rev.SBOM)
	}
}

// The contract's metadata map is author-controlled and arbitrarily wide, so it is
// flattened and bounded ONCE at Build, exactly like an observed-runtime map — never
// carried raw into a per-request projection.
func TestRevisionFrom_BoundsContractMetadataAtBuildTime(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "2.0", Service: contract.Service{Name: "meta-svc", Version: "1.0.0"},
		Metadata: map[string]any{"tier": "gold", "tags": []any{"a", "b"}},
	}
	rev, _ := revisionFrom(RawRevision{Bundle: &contract.Bundle{Contract: c}, Digest: "sha256:x"}, "local", fixedNow())
	got := map[string]string{}
	for _, f := range rev.Metadata.Items {
		got[f.Key] = f.Value
	}
	if got["tier"] != "gold" || got["tags[0]"] != "a" || got["tags[1]"] != "b" {
		t.Errorf("flattened metadata = %+v", got)
	}
	if rev.Metadata.Truncated {
		t.Error("a two-key metadata map is not truncated")
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
	tgt, _ := targetFrom(RawTarget{Scope: "prod", Kind: "k8s", Name: "web", Service: "web-svc"}, "k8s", fixedNow(), time.Hour)
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
	tgt, _ := targetFrom(RawTarget{Name: "w", Service: "s", Compliance: StatusCompliant, EvidenceAt: &old,
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
	tgt, _ := targetFrom(RawTarget{Name: "w", Service: "s", Compliance: StatusCompliant, EvidenceAt: &recent}, "k8s", fixedNow(), time.Hour)
	if tgt.Stale {
		t.Error("fresh evidence → not stale")
	}
	if len(tgt.Limitations) != 0 {
		t.Errorf("fresh compliant target should have no limitations: %+v", tgt.Limitations)
	}
}

func TestTargetFrom_WindowDisabled(t *testing.T) {
	old := fixedNow().Add(-1000 * time.Hour)
	tgt, _ := targetFrom(RawTarget{Name: "w", Service: "s", Compliance: StatusCompliant, EvidenceAt: &old}, "k8s", fixedNow(), 0)
	if tgt.Stale {
		t.Error("window=0 disables staleness")
	}
}

// -------------------- matchRevision / linkTargets --------------------

func TestMatchRevision(t *testing.T) {
	svcKey := NewServiceKey("svc")
	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{
		"svc@d1":  {Key: "svc@d1", Service: "svc", ServiceKey: svcKey, Digest: "sha256:d1", ResolvedRef: "reg/svc@sha256:d1"},
		"svc@r2":  {Key: "svc@r2", Service: "svc", ServiceKey: svcKey, ResolvedRef: "reg/svc:2.0"},
		"other@x": {Key: "other@x", Service: "other", ServiceKey: NewServiceKey("other"), Digest: "sha256:zz"},
	}}
	byDigest, kind := matchRevision(snap, &TargetRecord{Service: "svc", ServiceKey: svcKey, Digest: "sha256:d1"})
	if byDigest != "svc@d1" || kind != revisionMatchExact {
		t.Errorf("digest match = %q/%q, want svc@d1/exact", byDigest, kind)
	}
	byRef, kind := matchRevision(snap, &TargetRecord{Service: "svc", ServiceKey: svcKey, ResolvedRef: "reg/svc:2.0"})
	if byRef != "svc@r2" || kind != revisionMatchInferred {
		t.Errorf("resolvedRef match = %q/%q, want svc@r2/inferred (a mutable ref is not exact)", byRef, kind)
	}
	unlinked, kind := matchRevision(snap, &TargetRecord{Service: "svc", ServiceKey: svcKey, Digest: "sha256:none", ResolvedRef: "nope"})
	if unlinked != "" || kind != "" {
		t.Errorf("no match should be empty, got %q/%q", unlinked, kind)
	}
	// service with no revisions in fleet.
	if k, _ := matchRevision(snap, &TargetRecord{Service: "ghost", ServiceKey: NewServiceKey("ghost"), Digest: "sha256:d1"}); k != "" {
		t.Error("ghost service should not link")
	}
}

func TestMatchRevision_VersionFallback(t *testing.T) {
	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{
		"vsvc@2": {Key: "vsvc@2", Service: "vsvc", Version: "2.0.0"}, // no digest, no resolvedRef
	}}
	// Tag-pinned resolved ref links by version suffix (":version") — inferred.
	byTag, kind := matchRevision(snap, &TargetRecord{Service: "vsvc", ResolvedRef: "reg/vsvc:2.0.0"})
	if byTag != "vsvc@2" || kind != revisionMatchInferred {
		t.Errorf("tag version fallback = %q/%q, want vsvc@2/inferred", byTag, kind)
	}
	// Digest-style "@version" suffix also links.
	byAt, kind := matchRevision(snap, &TargetRecord{Service: "vsvc", ResolvedRef: "reg/vsvc@2.0.0"})
	if byAt != "vsvc@2" || kind != revisionMatchInferred {
		t.Errorf("@ version fallback = %q/%q, want vsvc@2/inferred", byAt, kind)
	}
	// A ref that pins a different version does not link.
	if got, _ := matchRevision(snap, &TargetRecord{Service: "vsvc", ResolvedRef: "reg/vsvc:9.9.9"}); got != "" {
		t.Errorf("mismatched version should not link, got %q", got)
	}
}

// -------------------- resolveDepService / resolveRefService --------------------

func TestResolveDepService(t *testing.T) {
	snap := &FleetSnapshot{Services: map[ServiceKey]*ServiceRecord{
		NewServiceKey("payments"): {Key: NewServiceKey("payments"), Name: "payments"},
		NewServiceKey("redis"):    {Key: NewServiceKey("redis"), Name: "redis"},
	}}
	if r, ok := resolveDepService(snap, "", contract.Dependency{Name: "payments"}); !ok || r != NewServiceKey("payments") {
		t.Errorf("direct resolve = %q,%v", r, ok)
	}
	if r, ok := resolveDepService(snap, "", contract.Dependency{Name: "redis-pacto"}); !ok || r != NewServiceKey("redis") {
		t.Errorf("-pacto strip resolve = %q,%v", r, ok)
	}
	if _, ok := resolveDepService(snap, "", contract.Dependency{Name: "ghost"}); ok {
		t.Error("ghost should not resolve")
	}
	// "-pacto" strip that still does not resolve.
	if _, ok := resolveDepService(snap, "", contract.Dependency{Name: "missing-pacto"}); ok {
		t.Error("stripped name absent → unresolved")
	}
}

func TestResolveRefService(t *testing.T) {
	snap := &FleetSnapshot{Services: map[ServiceKey]*ServiceRecord{NewServiceKey("cfg"): {Key: NewServiceKey("cfg"), Name: "cfg"}}}
	if r, ok := resolveRefService(snap, "", contract.ReferenceRef{Name: "cfg"}); !ok || r != NewServiceKey("cfg") {
		t.Errorf("ref resolve = %q,%v", r, ok)
	}
	if _, ok := resolveRefService(snap, "", contract.ReferenceRef{Name: "nope"}); ok {
		t.Error("unknown ref should not resolve")
	}
}

func TestResolveDepService_Domain(t *testing.T) {
	snap := &FleetSnapshot{Services: map[ServiceKey]*ServiceRecord{
		NewServiceKeyDomain("east", "payments"): {Key: NewServiceKeyDomain("east", "payments"), Name: "payments", Domain: "east"},
		NewServiceKeyDomain("east", "redis"):    {Key: NewServiceKeyDomain("east", "redis"), Name: "redis", Domain: "east"},
		NewServiceKeyDomain("west", "billing"):  {Key: NewServiceKeyDomain("west", "billing"), Name: "billing", Domain: "west"},
	}}
	// A bare dependency ref resolves within the depending revision's own domain,
	// returning the DOMAIN-QUALIFIED key (never the bare name).
	if r, ok := resolveDepService(snap, "east", contract.Dependency{Name: "payments"}); !ok || r != NewServiceKeyDomain("east", "payments") {
		t.Errorf("same-domain resolve = %q,%v", r, ok)
	}
	// The "-pacto" bundle-name suffix is stripped, still within the domain.
	if r, ok := resolveDepService(snap, "east", contract.Dependency{Name: "redis-pacto"}); !ok || r != NewServiceKeyDomain("east", "redis") {
		t.Errorf("-pacto strip in domain = %q,%v", r, ok)
	}
	// A name present only in a different domain does not resolve.
	if _, ok := resolveDepService(snap, "east", contract.Dependency{Name: "billing"}); ok {
		t.Error("cross-domain dependency must not resolve")
	}
}

func TestResolveRefService_Domain(t *testing.T) {
	snap := &FleetSnapshot{Services: map[ServiceKey]*ServiceRecord{
		NewServiceKeyDomain("east", "cfg"):  {Key: NewServiceKeyDomain("east", "cfg"), Name: "cfg", Domain: "east"},
		NewServiceKeyDomain("west", "cfg2"): {Key: NewServiceKeyDomain("west", "cfg2"), Name: "cfg2", Domain: "west"},
	}}
	if r, ok := resolveRefService(snap, "east", contract.ReferenceRef{Name: "cfg"}); !ok || r != NewServiceKeyDomain("east", "cfg") {
		t.Errorf("same-domain ref resolve = %q,%v", r, ok)
	}
	// A reference whose name exists only in another domain does not resolve.
	if _, ok := resolveRefService(snap, "east", contract.ReferenceRef{Name: "cfg2"}); ok {
		t.Error("cross-domain reference must not resolve")
	}
}

// -------------------- domain-qualified service identity --------------------

func TestBuild_DistinctDomains_Revisions(t *testing.T) {
	east := NewMemorySource("east", "oci", &Collection{Revisions: []RawRevision{
		{Bundle: bundleFor(t, "shared"), Domain: "east", Digest: "sha256:east"},
	}})
	west := NewMemorySource("west", "oci", &Collection{Revisions: []RawRevision{
		{Bundle: bundleFor(t, "shared"), Domain: "west", Digest: "sha256:west"},
	}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, east, west)
	if err != nil {
		t.Fatal(err)
	}
	eastKey := NewServiceKeyDomain("east", "shared")
	es, ws := snap.Services[eastKey], snap.Services[NewServiceKeyDomain("west", "shared")]
	if es == nil || ws == nil {
		t.Fatalf("both domain services must exist: east=%v west=%v", es, ws)
	}
	if es == ws {
		t.Error("same-named services in different domains must not be merged")
	}
	if es.Domain != "east" || ws.Domain != "west" {
		t.Errorf("service domains not set: %q %q", es.Domain, ws.Domain)
	}
	// The revision key is domain-qualified (ServiceKey@digest), so two same-named
	// revisions in different domains never collide on one key.
	rev := snap.Revisions[NewRevisionKey(eastKey, "sha256:east")]
	if rev == nil || rev.Domain != "east" || rev.ServiceKey != eastKey {
		t.Errorf("revision domain/key not derived from raw.Domain: %+v", rev)
	}
	if _, collides := snap.Revisions["shared@sha256:east"]; collides {
		t.Error("revision key must be domain-qualified, not the bare service name")
	}
}

func TestBuild_DistinctDomains_Targets(t *testing.T) {
	src := NewMemorySource("s", "oci", &Collection{Targets: []RawTarget{
		{Scope: "prod", Kind: "k8s", Name: "a", Service: "svc", Domain: "east"},
		{Scope: "prod", Kind: "k8s", Name: "b", Service: "svc", Domain: "west"},
	}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	es := snap.Services[NewServiceKeyDomain("east", "svc")]
	ws := snap.Services[NewServiceKeyDomain("west", "svc")]
	if es == nil || ws == nil || es == ws {
		t.Fatalf("targets in distinct domains must yield distinct services: east=%v west=%v", es, ws)
	}
	tgt := snap.Targets[NewTargetKey("prod", "k8s", "a")]
	if tgt.Domain != "east" || tgt.ServiceKey != NewServiceKeyDomain("east", "svc") {
		t.Errorf("target domain/key not derived from raw.Domain: %+v", tgt)
	}
}

// Two revisions of one service share a version but differ in content (no pinned
// digest). A target whose only correlation is that mutable version must NOT be
// linked to a coin-flip revision; it is ambiguous, links to nothing, and the
// result is identical regardless of source order (review section S15 + I4).
func TestMatchRevision_AmbiguousMutableMatch_Deterministic(t *testing.T) {
	mkRev := func(openapi string) RawRevision {
		return RawRevision{
			Bundle: &contract.Bundle{
				Contract: &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "orders", Version: "2.0.0"}},
				FS:       fstest.MapFS{"openapi.yaml": {Data: []byte(openapi)}},
			},
			Domain: "d", // no Digest -> distinct content-derived keys, same version
		}
	}
	tgt := RawTarget{Scope: "prod", Kind: "k8s", Name: "o", Service: "orders", Domain: "d",
		ResolvedRef: "reg/orders:2.0.0", Compliance: StatusCompliant}
	srcA := NewMemorySource("a", "oci", &Collection{Revisions: []RawRevision{mkRev("# variant A")}, Targets: []RawTarget{tgt}})
	srcB := NewMemorySource("b", "oci", &Collection{Revisions: []RawRevision{mkRev("# variant B")}})

	check := func(t *testing.T, snap *FleetSnapshot) string {
		t.Helper()
		var tr *TargetRecord
		for _, x := range snap.Targets {
			tr = x
		}
		if tr == nil {
			t.Fatal("expected the target")
		}
		if tr.ContractRevision != "" || tr.RevisionMatch != "" {
			t.Errorf("ambiguous mutable match must not link: got rev=%q match=%q", tr.ContractRevision, tr.RevisionMatch)
		}
		if !hasLim(snap.Limitations, LimitationRevisionAmbiguous) {
			t.Errorf("expected REVISION_LINK_AMBIGUOUS, got %+v", snap.Limitations)
		}
		return snap.SnapshotID
	}

	snapAB, err := Build(context.Background(), BuildOptions{Now: fixedNow}, srcA, srcB)
	if err != nil {
		t.Fatal(err)
	}
	snapBA, err := Build(context.Background(), BuildOptions{Now: fixedNow}, srcB, srcA)
	if err != nil {
		t.Fatal(err)
	}
	if idAB, idBA := check(t, snapAB), check(t, snapBA); idAB != idBA {
		t.Errorf("ambiguous link must be permutation-invariant: SnapshotID %q vs %q", idAB, idBA)
	}
}

// Build must record whether a target's revision link is exact (immutable digest)
// or inferred (a mutable version tag), so only the exact one is presented as the
// revision known to be running (review section S15).
func TestLinkTargets_ClassifiesExactVsInferred(t *testing.T) {
	rev := func(name, ver, digest, ref string) RawRevision {
		return RawRevision{Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0",
			Service: contract.Service{Name: name, Version: ver}}, FS: fstest.MapFS{}},
			Domain: "d", Digest: digest, ResolvedRef: ref}
	}
	src := NewMemorySource("s", "k8s", &Collection{
		Revisions: []RawRevision{
			rev("pay", "1.0.0", "sha256:pay1", "oci://r/pay@sha256:pay1"),
			rev("ship", "3.0.0", "", ""),
		},
		Targets: []RawTarget{
			{Scope: "prod", Kind: "k8s", Name: "p", Service: "pay", Domain: "d", Digest: "sha256:pay1", Compliance: StatusCompliant},
			{Scope: "prod", Kind: "k8s", Name: "s", Service: "ship", Domain: "d", ResolvedRef: "reg/ship:3.0.0", Compliance: StatusCompliant},
		},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, tr := range snap.Targets {
		got[tr.Service] = tr.RevisionMatch
	}
	if got["pay"] != revisionMatchExact {
		t.Errorf("pay target should link exact (digest), got %q", got["pay"])
	}
	if got["ship"] != revisionMatchInferred {
		t.Errorf("ship target should link inferred (version tag), got %q", got["ship"])
	}
}

func TestMatchRevision_DomainDiscriminates(t *testing.T) {
	eastKey := NewServiceKeyDomain("east", "shared")
	westKey := NewServiceKeyDomain("west", "shared")
	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{
		"east": {Key: "east", Service: "shared", ServiceKey: eastKey, Version: "1.0.0"},
		"west": {Key: "west", Service: "shared", ServiceKey: westKey, Version: "1.0.0"},
	}}
	// Same name and version in both domains; only the ServiceKey differs. A target
	// in the east domain must link to east's revision, never west's.
	tgt := &TargetRecord{Service: "shared", ServiceKey: eastKey, ResolvedRef: "reg/shared:1.0.0"}
	if got, _ := matchRevision(snap, tgt); got != "east" {
		t.Errorf("target linked to %q, want east (ServiceKey must discriminate)", got)
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
	// An un-marshalable value (a channel in a revision contract's metadata map) forces
	// the defensive marshal-error path to a stable marker rather than a partial hash.
	snap := &FleetSnapshot{
		Revisions: map[RevisionKey]*ContractRevision{
			"svc@x": {Key: "svc@x", Service: "svc", Contract: &contract.Contract{
				Metadata: map[string]any{"bad": make(chan int)},
			}},
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

func TestBuild_RevisionAndTargetMerge(t *testing.T) {
	rev := RawRevision{Bundle: validLeafBundle(t), Digest: "sha256:leaf"}
	tgt := RawTarget{Scope: "prod", Kind: "k8s", Name: "web", Service: "leaf-svc", Digest: "sha256:leaf", Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow())}
	src1 := NewMemorySource("a", "local", &Collection{Revisions: []RawRevision{rev}, Targets: []RawTarget{tgt}})
	// Second source contributes the SAME immutable revision and target: merge,
	// don't drop. Provenance from both sources is retained.
	rev2 := RawRevision{Bundle: validLeafBundle(t), Digest: "sha256:leaf"}
	src2 := NewMemorySource("b", "oci", &Collection{Revisions: []RawRevision{rev2}, Targets: []RawTarget{tgt}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src1, src2)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Revisions) != 1 {
		t.Errorf("same immutable revision should merge to 1, got %d", len(snap.Revisions))
	}
	if len(snap.Targets) != 1 {
		t.Errorf("same target should merge to 1, got %d", len(snap.Targets))
	}
	// The merged revision retains BOTH sources (no first-source-wins drop).
	for _, r := range snap.Revisions {
		if r.Source != "a" {
			t.Errorf("primary source should be a, got %q", r.Source)
		}
		if !containsStr(r.Sources, "a") || !containsStr(r.Sources, "b") {
			t.Errorf("merged revision should retain both sources, got %v", r.Sources)
		}
	}
	for _, tr := range snap.Targets {
		if !containsStr(tr.Sources, "a") || !containsStr(tr.Sources, "b") {
			t.Errorf("merged target should retain both sources, got %v", tr.Sources)
		}
	}
	// The logical service records both contributing sources.
	if s := snap.Service("leaf-svc"); !containsStr(s.Sources, "a") || !containsStr(s.Sources, "b") {
		t.Errorf("service should record both sources, got %v", s.Sources)
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
		if rel.FromService == "dupdep" && rel.To == "z" {
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

// bundleFor builds a distinct leaf-style bundle for service name id, carrying an
// empty (non-nil) FS so no skills/docs/tools are derived.
func bundleFor(t *testing.T, id string) *contract.Bundle {
	t.Helper()
	c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: id, Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}
	return &contract.Bundle{Contract: c, FS: fstest.MapFS{}}
}

// -------------------- relationships via Build --------------------

// relFrom finds the first relationship with the given from/to endpoints.
func relFrom(rels []Relationship, from, to string) *Relationship {
	for i := range rels {
		if string(rels[i].FromService) == from && rels[i].To == to {
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
	if resolvedDep == nil || !resolvedDep.Resolved || resolvedDep.ToService != "leaf-svc" {
		t.Errorf("resolved dependency edge wrong: %+v", resolvedDep)
	}
	if resolvedDep.Type != RelationshipDependency || resolvedDep.FromRevision == "" {
		t.Errorf("dependency edge must be typed and tagged with its FromRevision: %+v", resolvedDep)
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

// relObserved finds the first OBSERVED relationship by domain-qualified endpoints.
func relObserved(rels []Relationship, from, to string) *Relationship {
	for i := range rels {
		if rels[i].Provenance == ProvenanceObserved && string(rels[i].FromService) == from && string(rels[i].ToService) == to {
			return &rels[i]
		}
	}
	return nil
}

func TestBuild_ObservedRelationships(t *testing.T) {
	src := NewMemorySource("otel", "observation", &Collection{
		Revisions: []RawRevision{
			{Bundle: bundleFor(t, "web"), Digest: "sha256:web"},
			{Bundle: bundleFor(t, "api"), Digest: "sha256:api"},
			{Bundle: bundleFor(t, "cache"), Digest: "sha256:cache"},
			{Domain: "eu", Bundle: bundleFor(t, "payments"), Digest: "sha256:pe"},
			{Domain: "us", Bundle: bundleFor(t, "payments"), Digest: "sha256:pu"},
		},
		Observed: []ObservedEdge{
			{From: "web", To: "api", Count: 3},      // unique both -> observed edge
			{From: "web", To: "api", Count: 2},      // duplicate -> counts sum to 5
			{From: "web", To: "cache", Count: 1},    // same caller, different callee -> sort by 'to'
			{From: "api", To: "web", Count: 1},      // different caller -> sort by 'from'
			{From: "web", To: "payments", Count: 1}, // payments ambiguous across domains -> limitation
			{From: "web", To: "payments", Count: 4}, // same ambiguous name again -> limitation deduped
			{From: "ghost", To: "api", Count: 1},    // ghost unknown -> limitation
		},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}

	rel := relObserved(snap.Relationships, "web", "api")
	if rel == nil {
		t.Fatalf("expected an observed web->api relationship: %+v", snap.Relationships)
	}
	if rel.ObservedCount != 5 || rel.Source != "otel" || !rel.Resolved || rel.Type != RelationshipDependency {
		t.Errorf("observed relationship = %+v", rel)
	}
	// Observed adjacency is populated and SEPARATE from the declared indexes.
	if deps := snap.ObservedDependents(NewServiceKey("api")); len(deps) != 1 || deps[0] != NewServiceKey("web") {
		t.Errorf("observed dependents of api = %v, want [web]", deps)
	}
	if fwd := snap.ObservedDependencies(NewServiceKey("web")); len(fwd) != 2 {
		t.Errorf("observed dependencies of web = %v, want [api cache]", fwd)
	}
	if len(snap.reverseDeps[NewServiceKey("api")]) != 0 {
		t.Error("observed edges must NOT pollute the declared reverseDeps index")
	}
}

// When two sources witness the same edge, neither source's count is collapsed
// onto the other: the total is summed but each source keeps its own count and
// window, and the single Source field is empty (review section S5).
// Reconciliation is an explicit backend fact: a declared edge is matched only
// when an observed edge corroborates it, declared-not-observed when observation
// data exists but did not witness it, and insufficient when there is no
// observation data at all — never "reconciled" from deployment (review S3).
func TestQuery_HasObserved(t *testing.T) {
	declaredOnly := NewQuery(&FleetSnapshot{Relationships: []Relationship{
		{Type: RelationshipDependency, Provenance: ProvenanceDeclared, FromService: "a", ToService: "b"},
	}})
	if declaredOnly.HasObserved() {
		t.Error("a declared-only snapshot must not report observed")
	}
	withObserved := NewQuery(&FleetSnapshot{Relationships: []Relationship{
		{Type: RelationshipDependency, Provenance: ProvenanceObserved, FromService: "a", ToService: "b"},
	}})
	if !withObserved.HasObserved() {
		t.Error("a snapshot with an observed edge must report observed")
	}
}

func TestReconcileDeclared(t *testing.T) {
	snap := &FleetSnapshot{Relationships: []Relationship{
		{Type: RelationshipDependency, Provenance: ProvenanceDeclared, FromService: "d/web", ToService: "d/api"},
		{Type: RelationshipDependency, Provenance: ProvenanceDeclared, FromService: "d/web", ToService: "d/cache"},
		{Type: RelationshipDependency, Provenance: ProvenanceObserved, FromService: "d/web", ToService: "d/api"},
	}}
	reconcileDeclared(snap)
	got := map[ServiceKey]string{}
	for _, r := range snap.Relationships {
		if r.Provenance == ProvenanceDeclared {
			got[r.ToService] = r.Reconciliation
		}
	}
	if got["d/api"] != ReconciliationMatched {
		t.Errorf("web->api = %q, want matched", got["d/api"])
	}
	if got["d/cache"] != ReconciliationDeclaredNotObserved {
		t.Errorf("web->cache = %q, want declared-not-observed", got["d/cache"])
	}
}

func TestReconcileDeclared_InsufficientWithoutObservation(t *testing.T) {
	snap := &FleetSnapshot{Relationships: []Relationship{
		{Type: RelationshipDependency, Provenance: ProvenanceDeclared, FromService: "d/web", ToService: "d/api"},
	}}
	reconcileDeclared(snap)
	if snap.Relationships[0].Reconciliation != ReconciliationInsufficient {
		t.Errorf("no observation data must be insufficient, got %q", snap.Relationships[0].Reconciliation)
	}
}

func TestBuild_ObservedRelationships_MultiSource(t *testing.T) {
	base := fixedNow()
	revs := []RawRevision{
		{Bundle: bundleFor(t, "web"), Digest: "sha256:web"},
		{Bundle: bundleFor(t, "api"), Digest: "sha256:api"},
	}
	mesh := NewMemorySource("mesh", "observation", &Collection{
		Revisions: revs,
		Observed:  []ObservedEdge{{From: "web", To: "api", Count: 10, FirstSeen: base.Add(-3 * time.Hour), LastSeen: base.Add(-2 * time.Hour)}},
	})
	otel := NewMemorySource("otel", "observation", &Collection{
		Observed: []ObservedEdge{{From: "web", To: "api", Count: 5, FirstSeen: base.Add(-1 * time.Hour), LastSeen: base}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, mesh, otel)
	if err != nil {
		t.Fatal(err)
	}
	rel := relObserved(snap.Relationships, "web", "api")
	if rel == nil {
		t.Fatalf("expected observed web->api, got %+v", snap.Relationships)
	}
	if rel.ObservedCount != 15 {
		t.Errorf("total ObservedCount = %d, want 15", rel.ObservedCount)
	}
	if rel.Source != "" {
		t.Errorf("a multi-source edge must not attribute to one source, got %q", rel.Source)
	}
	bySrc := map[string]int{}
	for _, s := range rel.ObservedSources {
		bySrc[s.Source] = s.Count
	}
	if bySrc["mesh"] != 10 || bySrc["otel"] != 5 {
		t.Errorf("per-source counts lost: %+v", rel.ObservedSources)
	}
	if rel.FirstSeen == nil || !rel.FirstSeen.Equal(base.Add(-3*time.Hour)) {
		t.Errorf("window start = %v, want %v", rel.FirstSeen, base.Add(-3*time.Hour))
	}
	if rel.LastSeen == nil || !rel.LastSeen.Equal(base) {
		t.Errorf("window end = %v, want %v", rel.LastSeen, base)
	}
}

func TestBuild_ObservedRelationships_Unresolved(t *testing.T) {
	src := NewMemorySource("otel", "observation", &Collection{
		Revisions: []RawRevision{
			{Bundle: bundleFor(t, "web"), Digest: "sha256:web"},
			{Bundle: bundleFor(t, "api"), Digest: "sha256:api"},
			{Domain: "eu", Bundle: bundleFor(t, "payments"), Digest: "sha256:pe"},
			{Domain: "us", Bundle: bundleFor(t, "payments"), Digest: "sha256:pu"},
		},
		Observed: []ObservedEdge{
			{From: "web", To: "payments", Count: 1}, // payments ambiguous across domains -> limitation
			{From: "web", To: "payments", Count: 4}, // same ambiguous name again -> deduped
			{From: "ghost", To: "api", Count: 1},    // ghost unknown -> limitation
		},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	unresolved := 0
	for _, l := range snap.Limitations {
		if l.Code == LimitationObservedIdentityUnresolved {
			unresolved++
		}
	}
	if unresolved != 2 {
		t.Errorf("want 2 unresolved observed limitations (payments ambiguous, ghost unknown), got %d: %+v", unresolved, snap.Limitations)
	}
	if relObserved(snap.Relationships, "ghost", "api") != nil {
		t.Error("an unknown caller must not create an observed relationship")
	}
	// No observed edges at all is a no-op (guards the empty-input path).
	s2, _ := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("x", "y", &Collection{}))
	if len(s2.Relationships) != 0 {
		t.Error("no observed edges must add no relationships")
	}
}

func TestBuild_RelationshipsRefAndLock(t *testing.T) {
	snap := buildRelSnapshot(t)
	refEdge := relFrom(snap.Relationships, "web", "cfg-svc")
	if refEdge == nil || refEdge.Type != RelationshipConfigRef || !refEdge.Resolved {
		t.Errorf("config reference edge should be a distinct configuration_reference type: %+v", refEdge)
	}
	lockedDep := relFrom(snap.Relationships, "web", "dep-svc")
	if lockedDep == nil || lockedDep.LockedDigest != "sha256:deadbeef" || lockedDep.LockedVersion != "2.0.0" {
		t.Errorf("locked dependency should carry lock digest/version: %+v", lockedDep)
	}
}

// TestBuild_TypedReferenceEdges asserts config and policy references produce
// DISTINCT typed edges (never collapsed into one generic "reference"), and that an
// unresolved reference still emits an edge carrying a reason.
func TestBuild_TypedReferenceEdges(t *testing.T) {
	app := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "app", Version: "1.0.0"},
		Configurations: []contract.Configuration{
			{Name: "cfg-target", Ref: "oci://x/cfg"},
		},
		Policies: []contract.Policy{
			{Name: "pol-target", Ref: "oci://x/pol"},
			{Name: "ghost-pol", Ref: "oci://x/ghost"},
		},
	}
	col := &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: app, FS: fstest.MapFS{}}, Digest: "sha256:app"},
		{Bundle: bundleFor(t, "cfg-target"), Digest: "sha256:cfg"},
		{Bundle: bundleFor(t, "pol-target"), Digest: "sha256:pol"},
	}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	cfg := relFrom(snap.Relationships, "app", "cfg-target")
	if cfg == nil || cfg.Type != RelationshipConfigRef || !cfg.Resolved || cfg.ToService != "cfg-target" {
		t.Errorf("config ref edge wrong: %+v", cfg)
	}
	pol := relFrom(snap.Relationships, "app", "pol-target")
	if pol == nil || pol.Type != RelationshipPolicyRef || !pol.Resolved {
		t.Errorf("policy ref edge wrong: %+v", pol)
	}
	if RelationshipConfigRef == RelationshipPolicyRef {
		t.Fatal("config and policy reference types must be distinct")
	}
	ghost := relFrom(snap.Relationships, "app", "ghost-pol")
	if ghost == nil || ghost.Type != RelationshipPolicyRef || ghost.Resolved || ghost.Reason == "" {
		t.Errorf("unresolved policy ref should emit an edge with a reason: %+v", ghost)
	}
}

// TestBuild_ResolvedRevisionPinnedByLock covers resolveDepRevision's match branch:
// when a lock digest identifies an exact revision of the resolved service, the
// dependency edge pins it via ResolvedRevision.
func TestBuild_ResolvedRevisionPinnedByLock(t *testing.T) {
	web := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "web", Version: "1.0.0"},
		Dependencies: []contract.Dependency{{Name: "dep-svc", Ref: "oci://x/dep", Required: true, Compatibility: "^2.0.0"}},
	}
	col := &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: web, FS: fstest.MapFS{}}, Digest: "sha256:web", Lock: mustLock(t)},
		// digest matches the lock's dep-svc digest (sha256:deadbeef).
		{Bundle: bundleFor(t, "dep-svc"), Digest: "sha256:deadbeef"},
	}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	rel := relFrom(snap.Relationships, "web", "dep-svc")
	want := NewRevisionKey(NewServiceKey("dep-svc"), "sha256:deadbeef")
	if rel == nil || rel.ResolvedRevision != want {
		t.Errorf("locked dep should pin the resolved revision %q, got %+v", want, rel)
	}
}

// TestBuildRelationships_NilContractSkipped covers the defensive nil-Contract skip
// in buildRelationships (unreachable via Build, exercised directly).
func TestBuildRelationships_NilContractSkipped(t *testing.T) {
	snap := &FleetSnapshot{
		Revisions:             map[RevisionKey]*ContractRevision{"x@1": {Key: "x@1", Service: "x"}},
		forwardDeps:           map[ServiceKey][]ServiceKey{},
		reverseDeps:           map[ServiceKey][]ServiceKey{},
		forwardDepsByRevision: map[RevisionKey][]ServiceKey{},
	}
	buildRelationships(snap)
	if len(snap.Relationships) != 0 {
		t.Errorf("nil-contract revision must produce no edges, got %+v", snap.Relationships)
	}
}

// twoRevSnapshot builds a fleet with one service "svc" carrying TWO revisions that
// declare DIFFERENT dependencies, both deps present as services, plus a target
// pinned to the first revision. It is the shared fixture for per-revision edge and
// revision/target graph-scoping assertions.
func twoRevSnapshot(t *testing.T) *FleetSnapshot {
	t.Helper()
	mk := func(name, version, digest string, deps ...string) RawRevision {
		c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: version}}
		for _, d := range deps {
			c.Dependencies = append(c.Dependencies, contract.Dependency{Name: d, Ref: "oci://x/" + d, Required: true, Compatibility: "^1.0.0"})
		}
		return RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: digest}
	}
	col := &Collection{
		Revisions: []RawRevision{
			mk("svc", "1.0.0", "sha256:1", "old-dep"),
			mk("svc", "2.0.0", "sha256:2", "new-dep"),
			mk("old-dep", "1.0.0", "sha256:old"),
			mk("new-dep", "1.0.0", "sha256:new"),
		},
		Targets: []RawTarget{
			// pinned by digest to the v1.0.0 revision (sha256:1).
			{Scope: "prod", Kind: "k8s", Name: "svc-app", Service: "svc", Digest: "sha256:1", Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow())},
		},
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// TestBuild_PerRevisionEdges asserts edges are built from EVERY revision and tagged
// with the exact FromRevision they originate from — never a single "latest"/
// representative revision. A service with two revisions declaring different deps
// yields two distinct, revision-tagged edge sets.
func TestBuild_PerRevisionEdges(t *testing.T) {
	snap := twoRevSnapshot(t)
	rev1 := NewRevisionKey(NewServiceKey("svc"), "sha256:1")
	rev2 := NewRevisionKey(NewServiceKey("svc"), "sha256:2")

	var oldEdge, newEdge *Relationship
	for i := range snap.Relationships {
		switch snap.Relationships[i].To {
		case "old-dep":
			oldEdge = &snap.Relationships[i]
		case "new-dep":
			newEdge = &snap.Relationships[i]
		}
	}
	if oldEdge == nil || oldEdge.FromRevision != rev1 {
		t.Errorf("old-dep edge must originate from rev1 %q, got %+v", rev1, oldEdge)
	}
	if newEdge == nil || newEdge.FromRevision != rev2 {
		t.Errorf("new-dep edge must originate from rev2 %q, got %+v", rev2, newEdge)
	}
	// Revision-accurate index: each revision sees only ITS dependency.
	if got := snap.forwardDepsByRevision[rev1]; len(got) != 1 || got[0] != "old-dep" {
		t.Errorf("forwardDepsByRevision[rev1] = %v, want [old-dep]", got)
	}
	if got := snap.forwardDepsByRevision[rev2]; len(got) != 1 || got[0] != "new-dep" {
		t.Errorf("forwardDepsByRevision[rev2] = %v, want [new-dep]", got)
	}
	// Aggregated service index is the union across revisions.
	if got := snap.forwardDeps["svc"]; len(got) != 2 {
		t.Errorf("forwardDeps[svc] should union both deps, got %v", got)
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

func TestBuild_OwnerBackfill_LabelsNotMerged(t *testing.T) {
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
	// Target labels must NOT be merged into the logical service label map.
	if s.Labels != nil {
		t.Errorf("service Labels must stay nil (target labels live on the target), got %+v", s.Labels)
	}
	// The label lives on the target, which also carries its Sources.
	for _, tk := range s.Targets {
		tgt := snap.Targets[tk]
		if tgt.Labels["env"] != "prod" {
			t.Errorf("target should keep its own labels, got %+v", tgt.Labels)
		}
		if len(tgt.Sources) != 1 || tgt.Sources[0] != "local" {
			t.Errorf("target Sources = %v, want [local]", tgt.Sources)
		}
	}
}

// -------------------- serviceStatus severity order --------------------

func TestServiceStatus_WarningDominatesCompliant(t *testing.T) {
	// NonCompliant > Unknown > Warning > Compliant. With only Warning + Compliant
	// targets, Warning wins.
	snap := &FleetSnapshot{
		Revisions: map[RevisionKey]*ContractRevision{},
		Targets: map[TargetKey]*TargetRecord{
			"w": {Key: "w", Compliance: StatusWarning},
			"c": {Key: "c", Compliance: StatusCompliant},
		},
	}
	s := &ServiceRecord{Targets: []TargetKey{"c", "w"}}
	if got := serviceStatus(snap, s); got != StatusWarning {
		t.Errorf("Warning should dominate Compliant, got %q", got)
	}
}

func TestServiceStatus_TargetInvalidDominates(t *testing.T) {
	// A target reporting Invalid compliance dominates even NonCompliant.
	snap := &FleetSnapshot{
		Revisions: map[RevisionKey]*ContractRevision{},
		Targets: map[TargetKey]*TargetRecord{
			"n": {Key: "n", Compliance: StatusNonCompliant},
			"i": {Key: "i", Compliance: StatusInvalid},
		},
	}
	s := &ServiceRecord{Targets: []TargetKey{"n", "i"}}
	if got := serviceStatus(snap, s); got != StatusInvalid {
		t.Errorf("target Invalid must dominate, got %q", got)
	}
}

func TestServiceStatus_InvalidNotCollapsed(t *testing.T) {
	// An invalid contract revision is a distinct, worse state than a compliance
	// violation: it must NEVER be collapsed into NonCompliant.
	bad, _ := revisionFrom(RawRevision{Bundle: &contract.Bundle{Contract: mustParse(t, invalidYAML), RawYAML: []byte(invalidYAML), FS: fstest.MapFS{}}}, "s", fixedNow())
	snap := &FleetSnapshot{
		Revisions: map[RevisionKey]*ContractRevision{bad.Key: bad},
		Targets:   map[TargetKey]*TargetRecord{"t": {Key: "t", Compliance: StatusNonCompliant}},
	}
	s := &ServiceRecord{Revisions: []RevisionKey{bad.Key}, Targets: []TargetKey{"t"}}
	if got := serviceStatus(snap, s); got != StatusInvalid {
		t.Errorf("invalid revision must not collapse to NonCompliant, got %q", got)
	}
}

// -------------------- deriveOwner / ownerSeen --------------------

func TestDeriveOwner_DeterministicBackfill(t *testing.T) {
	// The lowest-keyed revision that declares an owner sets the summary; a revision
	// with an empty owner is skipped.
	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{
		"svc@a": {Key: "svc@a", Service: "svc"}, // empty owner → skipped
		"svc@b": {Key: "svc@b", Service: "svc", Owner: contract.Owner{Team: "b-team"}},
	}}
	s := &ServiceRecord{Name: "svc", Revisions: []RevisionKey{"svc@b", "svc@a"}}
	if lim := deriveOwner(snap, s); lim != nil {
		t.Errorf("single distinct owner → no limitation, got %+v", lim)
	}
	if s.Owner.Team != "b-team" {
		t.Errorf("owner should be derived from the declaring revision, got %+v", s.Owner)
	}
}

func TestDeriveOwner_SameOwnerNoConflict(t *testing.T) {
	// Two revisions declaring the SAME owner is not a conflict (ownerSeen==true).
	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{
		"svc@a": {Key: "svc@a", Service: "svc", Owner: contract.Owner{Team: "same"}},
		"svc@z": {Key: "svc@z", Service: "svc", Owner: contract.Owner{Team: "same"}},
	}}
	s := &ServiceRecord{Name: "svc", Revisions: []RevisionKey{"svc@z", "svc@a"}}
	if lim := deriveOwner(snap, s); lim != nil {
		t.Errorf("identical owners → no conflict, got %+v", lim)
	}
	if s.Owner.Team != "same" {
		t.Errorf("owner = %+v", s.Owner)
	}
}

func TestDeriveOwner_Conflict(t *testing.T) {
	// Differing owners across revisions → OWNER_CONFLICT; the summary owner is
	// deterministically the lowest revision key's owner.
	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{
		"svc@a": {Key: "svc@a", Service: "svc", Owner: contract.Owner{Team: "a-team"}},
		"svc@b": {Key: "svc@b", Service: "svc", Owner: contract.Owner{Team: "b-team"}},
	}}
	s := &ServiceRecord{Name: "svc", Revisions: []RevisionKey{"svc@b", "svc@a"}}
	lim := deriveOwner(snap, s)
	if len(lim) != 1 || lim[0].Code != LimitationOwnerConflict || lim[0].Source != "fleet" {
		t.Fatalf("differing owners → OWNER_CONFLICT from fleet, got %+v", lim)
	}
	if s.Owner.Team != "a-team" {
		t.Errorf("owner summary should be the lowest-key revision's owner (a-team), got %+v", s.Owner)
	}
}

func TestBuild_OwnerConflictSurfaced(t *testing.T) {
	c1 := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "svc", Version: "1.0.0", Owner: contract.Owner{Team: "t1"}}}
	c2 := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "svc", Version: "2.0.0", Owner: contract.Owner{Team: "t2"}}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: c1, FS: fstest.MapFS{}}, Digest: "sha256:1"},
		{Bundle: &contract.Bundle{Contract: c2, FS: fstest.MapFS{}}, Digest: "sha256:2"},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if !hasLimitation(snap.Limitations, LimitationOwnerConflict) {
		t.Errorf("conflicting per-revision owners should surface OWNER_CONFLICT: %+v", snap.Limitations)
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
	if snap.Service("svc") == nil {
		t.Fatal("service missing")
	}
	// The snapshot deep-copies the contract (immutability): equal value, distinct
	// pointer, so a later source mutation cannot reach the snapshot.
	for _, r := range snap.Revisions {
		if r.Contract == c {
			t.Error("revision must NOT alias the input contract pointer")
		}
		if r.Contract == nil || r.Contract.Service.Name != c.Service.Name {
			t.Error("revision contract should be an equal deep copy")
		}
	}
}

// ensure finding import is used (targets carry findings in query tests, but keep
// this package-level reference explicit and cheap).
var _ = finding.SeverityError
