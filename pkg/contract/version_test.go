package contract

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// prev and cur are the last two supported versions: the tests never name the
// current or target version, so they keep working when a version is added.
var (
	prev = SupportedVersions[len(SupportedVersions)-2]
	cur  = Current()
)

// fixture is doc with every {v} replaced by version.
func fixture(doc, version string) []byte { return []byte(strings.ReplaceAll(doc, "{v}", version)) }

// fakeCheck returns the same findings for every document, counting calls.
type fakeCheck struct {
	calls         int
	before, after struct {
		e   []ValidationError
		w   []ValidationWarning
		err error
	}
}

func (f *fakeCheck) check(raw []byte) ([]ValidationError, []ValidationWarning, error) {
	f.calls++
	r := f.before
	if bytes.Contains(raw, []byte(cur)) {
		r = f.after
	}
	return r.e, r.w, r.err
}

func clean([]byte) ([]ValidationError, []ValidationWarning, error) { return nil, nil, nil }

func TestKnown(t *testing.T) {
	for _, v := range SupportedVersions {
		if !Known(v) {
			t.Errorf("Known(%q) = false", v)
		}
	}
	if Known("") || Known("1.0") || Known("9.9") {
		t.Fatal("Known accepts an unsupported version")
	}
}

func TestBehind(t *testing.T) {
	for v, want := range map[string]bool{SupportedVersions[0]: true, prev: true, cur: false, "9.9": false, "": false} {
		c, got := Behind(v)
		if got != want || c != cur {
			t.Errorf("Behind(%q) = %q, %v; want %q, %v", v, c, got, cur, want)
		}
	}
}

// TestBehind25: 2.5 is behind 2.6.
func TestBehind25(t *testing.T) {
	if c, behind := Behind("2.5"); c != "2.6" || !behind {
		t.Fatalf("Behind(2.5) = %q, %v", c, behind)
	}
}

// TestUpgradeRaises25To26: a 2.5 contract whose environments all carry a
// file raises to 2.6 with no Kept finding.
func TestUpgradeRaises25To26(t *testing.T) {
	const doc = "pactoVersion: \"{v}\"\nservice: {name: s, version: 1.0.0}\nenvironments: [{name: staging, file: environments/staging.yaml}]\n"
	up, err := Upgrade(fixture(doc, "2.5"), "2.6", clean)
	if err != nil || up.Kept != nil || up.From != "2.5" || up.To != "2.6" || !bytes.Equal(up.Raw, fixture(doc, "2.6")) {
		t.Fatalf("Upgrade: %+v %v", up, err)
	}
}

func TestOlder(t *testing.T) {
	prev := SupportedVersions[len(SupportedVersions)-2]
	if !Older(prev, Current()) || Older(Current(), prev) || Older(Current(), Current()) || !Older("9.9", prev) {
		t.Fatal("Older does not follow SupportedVersions order")
	}
}

func TestUpgradeSplicesOnlyTheToken(t *testing.T) {
	const doc = "# a contract\nservice: {name: s, version: 1.0.0} # flow\n\npactoVersion: {v}   # the version\nconfigurations: [{name: a, schema: a.json, required: true}]\n# end, no newline"
	up, err := Upgrade(fixture(doc, prev), cur, clean)
	if err != nil || up.Kept != nil {
		t.Fatalf("Upgrade: %+v %v", up, err)
	}
	if !bytes.Equal(up.Raw, fixture(doc, cur)) || up.From != prev || up.To != cur {
		t.Fatalf("got %q", up.Raw)
	}
}

func TestUpgradeKeepsQuoteStyle(t *testing.T) {
	for _, doc := range []string{"pactoVersion: {v}\n", "pactoVersion: '{v}'\n", "pactoVersion: \"{v}\"\n", "a: 1\npactoVersion:   '{v}' \n"} {
		up, err := Upgrade(fixture(doc, prev), cur, clean)
		if err != nil || !bytes.Equal(up.Raw, fixture(doc, cur)) {
			t.Errorf("%q: got %q %v", doc, up.Raw, err)
		}
	}
}

func TestUpgradeKeepsBOM(t *testing.T) {
	const doc = "\xef\xbb\xbfpactoVersion: '{v}'\nservice: {name: a, version: 1.0.0}\n"
	if up, err := Upgrade(fixture(doc, prev), cur, clean); err != nil || !bytes.Equal(up.Raw, fixture(doc, cur)) {
		t.Fatalf("got %q %v", up.Raw, err)
	}
}

func TestUpgradeCRLF(t *testing.T) {
	const doc = "service:\r\n  name: s\r\npactoVersion: '{v}'\r\nx: y\r\n"
	if up, err := Upgrade(fixture(doc, prev), cur, clean); err != nil || !bytes.Equal(up.Raw, fixture(doc, cur)) {
		t.Fatalf("got %q %v", up.Raw, err)
	}
}

func TestUpgradeDeterministic(t *testing.T) {
	raw := fixture("é: ü\npactoVersion: {v} # ñ\n", prev)
	a, _ := Upgrade(raw, cur, clean)
	b, _ := Upgrade(raw, cur, clean)
	if !bytes.Equal(a.Raw, b.Raw) || bytes.Equal(a.Raw, raw) {
		t.Fatalf("got %q and %q", a.Raw, b.Raw)
	}
}

func TestUpgradeRefusesAlias(t *testing.T) {
	_, err := Upgrade(fixture("v: &v '{v}'\npactoVersion: *v\n", prev), cur, clean)
	var pe *ParseError
	if !errors.As(err, &pe) || !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("got %v", err)
	}
}

func TestUpgradeNonMappingRoot(t *testing.T) {
	if _, err := Upgrade([]byte("- a\n"), cur, clean); err == nil || !strings.Contains(err.Error(), "not a mapping") {
		t.Fatalf("got %v", err)
	}
	if _, err := Upgrade([]byte("a: [\n"), cur, clean); err == nil {
		t.Fatal("broken YAML upgraded")
	}
}

func TestUpgradeRefusesUnspliceableToken(t *testing.T) {
	for _, doc := range []string{`pactoVersion: "2\x2e3"` + "\n", "pactoVersion: |-\n  {v}\n", "pactoVersion: >-\n  {v}\n"} {
		up, err := Upgrade(fixture(doc, prev), cur, clean)
		if err == nil || !strings.Contains(err.Error(), "by hand") || up.To != up.From {
			t.Errorf("%q: got %+v %v", doc, up, err)
		}
	}
}

func TestOffsetOfOutOfRange(t *testing.T) {
	raw := []byte("ab\ncd")
	if offsetOf(raw, 3, 1) != -1 || offsetOf(raw, 1, 4) != -1 || offsetOf(raw, 2, 4) != -1 || offsetOf(raw, 2, 2) != 4 {
		t.Fatal("offsetOf")
	}
}

func TestUpgradeAbsentVersionUnchanged(t *testing.T) {
	for _, raw := range []string{"", "service: {name: s}\n"} {
		up, err := Upgrade([]byte(raw), cur, clean)
		if err != nil || string(up.Raw) != raw || up.From != "" || up.To != "" || up.Change() != "" {
			t.Errorf("%q: got %+v %v", raw, up, err)
		}
	}
}

func TestUpgradeManual1x(t *testing.T) {
	for _, v := range []string{"1.0", "1"} {
		_, err := Upgrade([]byte("pactoVersion: '"+v+"'\n"), cur, clean)
		var mm *ErrManualMigration
		if !errors.As(err, &mm) || mm.Version != v || !strings.Contains(err.Error(), "MIGRATION.md") {
			t.Errorf("%s: got %v", v, err)
		}
	}
}

func TestUpgradeUnsupportedFromAndTo(t *testing.T) {
	for _, tc := range []struct{ raw, to string }{{"pactoVersion: '9.9'\n", cur}, {string(fixture("pactoVersion: {v}\n", prev)), "9.9"}} {
		_, err := Upgrade([]byte(tc.raw), tc.to, clean)
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Path != "pactoVersion" {
			t.Errorf("%+v: got %v", tc, err)
		}
	}
}

func TestUpgradeAtOrPastTargetSkipsCheck(t *testing.T) {
	f := &fakeCheck{}
	for _, raw := range [][]byte{fixture("pactoVersion: {v}\n", cur), fixture("pactoVersion: {v}\n", prev)} {
		if up, err := Upgrade(raw, prev, f.check); err != nil || !bytes.Equal(up.Raw, raw) || up.Change() != "" {
			t.Fatalf("got %+v %v", up, err)
		}
	}
	if up, _ := Upgrade(fixture("pactoVersion: {v}\n", cur), cur, f.check); up.To != cur || f.calls != 0 {
		t.Fatalf("check ran %d times", f.calls)
	}
}

func upgradeWith(t *testing.T, f *fakeCheck) (Upgraded, error) {
	t.Helper()
	return Upgrade(fixture("pactoVersion: {v}\n", prev), cur, f.check)
}

func assertKept(t *testing.T, up Upgraded, code string) {
	t.Helper()
	if up.Kept == nil || up.Kept.Code != code || up.To != prev || !bytes.Equal(up.Raw, fixture("pactoVersion: {v}\n", prev)) {
		t.Fatalf("got %+v", up)
	}
}

func TestUpgradeCheckErrorKeeps(t *testing.T) {
	f := &fakeCheck{}
	f.before.err = errors.New("cannot tell")
	up, err := upgradeWith(t, f)
	if err != f.before.err || up.To != prev || up.Kept != nil {
		t.Fatalf("got %+v %v", up, err)
	}
}

func TestUpgradeRaisedCheckErrorKeeps(t *testing.T) {
	f := &fakeCheck{}
	f.after.err = errors.New("cannot tell")
	up, err := upgradeWith(t, f)
	if err != f.after.err || up.To != prev || up.Kept != nil {
		t.Fatalf("got %+v %v", up, err)
	}
}

func TestUpgradeNewErrorKeeps(t *testing.T) {
	f := &fakeCheck{}
	f.after.e = []ValidationError{{Code: "E", Path: "p", Message: "m"}}
	up, err := upgradeWith(t, f)
	if err != nil {
		t.Fatal(err)
	}
	assertKept(t, up, "E")
}

func TestUpgradeNewWarningKeeps(t *testing.T) {
	f := &fakeCheck{}
	f.before.w = []ValidationWarning{{Code: "W", Path: "p"}}
	f.after.w = []ValidationWarning{{Code: "W", Path: "p"}, {Code: "W", Path: "q", Message: "m"}}
	up, _ := upgradeWith(t, f)
	assertKept(t, up, "W")
	if up.Kept.Path != "q" || up.Kept.Message != "m" {
		t.Fatalf("got %+v", up.Kept)
	}
}

func TestUpgradeWarningBecomesErrorKeeps(t *testing.T) {
	f := &fakeCheck{}
	f.before.w = []ValidationWarning{{Code: "C", Path: "p"}}
	f.after.e = []ValidationError{{Code: "C", Path: "p"}}
	up, _ := upgradeWith(t, f)
	assertKept(t, up, "C")
}

func TestUpgradeExtraSameKeyFindingKeeps(t *testing.T) {
	f := &fakeCheck{}
	f.before.w = []ValidationWarning{{Code: "C", Path: "p"}}
	f.after.w = []ValidationWarning{{Code: "C", Path: "p"}, {Code: "C", Path: "p"}}
	up, _ := upgradeWith(t, f)
	assertKept(t, up, "C")
}

func TestUpgradeNewFindingBeatsRaisedError(t *testing.T) {
	f := &fakeCheck{}
	f.after.e = []ValidationError{{Code: "E", Path: "p"}}
	f.after.err = errors.New("cannot tell either")
	up, err := upgradeWith(t, f)
	if err != nil {
		t.Fatal(err)
	}
	assertKept(t, up, "E")
}

// An original that cannot tell (it failed or skipped a stage) never backs an
// "adds X" claim: a finding the raised run has may be one the original never
// ran the stage to see.
func TestUpgradeOriginalErrorBeatsNewFinding(t *testing.T) {
	f := &fakeCheck{}
	f.before.e = []ValidationError{{Code: "POLICY", Path: "pactoVersion"}}
	f.before.err = errors.New("cannot tell")
	f.after.e = []ValidationError{{Code: "ENVIRONMENT_VALUES_INVALID", Path: "environments[0].file"}}
	up, err := upgradeWith(t, f)
	if err != f.before.err || up.Kept != nil || up.To != prev || f.calls != 1 {
		t.Fatalf("got %+v %v after %d calls", up, err, f.calls)
	}
}

// An original that reached every stage and only could not verify one still
// backs a confirmed new finding.
func TestUpgradeComparableErrorLetsNewFindingWin(t *testing.T) {
	f := &fakeCheck{}
	f.before.err = fmt.Errorf("could not verify: %w", ErrComparable)
	f.after.e = []ValidationError{{Code: "CONFIG_REF_UNRESOLVED", Path: "configurations[0]"}}
	up, err := upgradeWith(t, f)
	if err != nil {
		t.Fatal(err)
	}
	assertKept(t, up, "CONFIG_REF_UNRESOLVED")
	f.after.e = nil
	if up, err := upgradeWith(t, f); err != f.before.err || up.Kept != nil || up.To != prev {
		t.Fatalf("got %+v %v", up, err)
	}
}

func TestUpgradeSameFindingsRaise(t *testing.T) {
	f := &fakeCheck{}
	f.before.e = []ValidationError{{Code: "E", Path: "p", Message: "at one version"}}
	f.before.w = []ValidationWarning{{Code: "W", Path: "p", Message: "at one version"}}
	f.after.e = []ValidationError{{Code: "E", Path: "p", Message: "at another"}}
	f.after.w = []ValidationWarning{{Code: "W", Path: "p", Message: "at another"}}
	up, err := upgradeWith(t, f)
	if err != nil || up.To != cur || up.Kept != nil || f.calls != 2 {
		t.Fatalf("got %+v %v", up, err)
	}
}

func TestUpgradedChange(t *testing.T) {
	raised := Upgraded{From: prev, To: cur}
	kept := Upgraded{From: prev, To: prev, Kept: &ValidationError{Code: "C", Path: "p", Message: "m"}}
	if got := raised.Change(); got != `set pactoVersion to "`+cur+`" (was "`+prev+`")` {
		t.Errorf("raised: %q", got)
	}
	if got := kept.Change(); got != "kept at "+prev+": C at p: m" {
		t.Errorf("kept: %q", got)
	}
	if got := (Upgraded{From: prev, To: prev}).Change(); got != "" {
		t.Errorf("unchanged: %q", got)
	}
}
