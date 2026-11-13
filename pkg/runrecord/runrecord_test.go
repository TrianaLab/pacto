package runrecord

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIDIsDistinctChangeKey(t *testing.T) {
	base := ID(KindDiff, "payments", "a", "b")
	if base != ID(KindDiff, "payments", "a", "b") {
		t.Fatal("same inputs gave different ids")
	}
	if !idPattern.MatchString(base) {
		t.Fatalf("id %q is malformed", base)
	}
	for _, other := range []string{
		ID(KindImpact, "payments", "a", "b"),
		ID(KindDiff, "orders", "a", "b"),
		ID(KindDiff, "payments", "x", "b"),
		ID(KindDiff, "payments", "a", "x"),
	} {
		if other == base {
			t.Fatal("changing one part kept the id")
		}
	}
	if ID(KindDiff, "s", "a", "bc") == ID(KindDiff, "s", "ab", "c") {
		t.Fatal("the separator does not keep a|bc apart from ab|c")
	}
}

func TestParseGolden(t *testing.T) {
	for _, name := range []string{"impact-blocked.json", "publish-refused.json"} {
		data, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		r, err := Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if r.ID != ID(r.Kind, r.Service, r.OldRevision, r.NewRevision) {
			t.Fatalf("%s: the fixture's id is not its change id", name)
		}
		out, err := Encode(r)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out, bytes.TrimSuffix(data, []byte("\n"))) {
			t.Fatalf("%s: Encode does not reproduce the file:\n%s", name, out)
		}
	}
}

func golden(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/impact-blocked.json")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestParseRejects(t *testing.T) {
	g := golden(t)
	cases := map[string]string{
		"unknown field":    strings.Replace(g, `"kind"`, `"actor":"me","kind"`, 1),
		"trailing data":    g + "}",
		"version":          strings.Replace(g, "run-record/v1", "run-record/v2", 1),
		"kind":             strings.Replace(g, `"kind":"impact"`, `"kind":"validate"`, 1),
		"outcome":          strings.Replace(g, `"outcome":"failed"`, `"outcome":"ok"`, 1),
		"id":               strings.Replace(g, `"id":"sha256:c1714d93c4049e5b8aad22a64ab1682e207731ffac9adef7711123b204194894"`, `"id":"sha256:XYZ"`, 1),
		"service":          strings.Replace(g, `"service":"payments"`, `"service":""`, 1),
		"recordedAt":       strings.Replace(g, `"recordedAt":"2026-11-13T09:00:00Z"`, `"recordedAt":"0001-01-01T00:00:00Z"`, 1),
		"gate verdict":     strings.Replace(g, `"verdict":"blocked"`, `"verdict":"maybe"`, 1),
		"negative count":   strings.Replace(g, `"unknown":1`, `"unknown":-1`, 1),
		"completeness":     strings.Replace(g, `"completeness":"complete"`, `"completeness":"most"`, 1),
		"no completeness":  strings.Replace(g, `"completeness":"complete",`, ``, 1),
		"no consumers":     strings.Replace(g, `"consumers":{"incompatible":2,"unknown":1,"compatible":0,"unclassified":0,"blocking":2},`, ``, 1),
		"oversized":        g + strings.Repeat(" ", MaxRecordBytes+1-len(g)),
		"not json":         "{",
		"bound is checked": strings.Repeat(" ", MaxRecordBytes+1),
	}
	for name, data := range cases {
		if data == g {
			t.Fatalf("%s: the case did not change the fixture", name)
		}
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	if _, err := Parse([]byte(cases["oversized"])); err == nil || !strings.Contains(err.Error(), "65536") {
		t.Errorf("the size error does not name the bound: %v", err)
	}
}

func TestAnchorTag(t *testing.T) {
	tag := AnchorTag("payments")
	if !strings.HasPrefix(tag, "pacto-stats-") || len(tag) != len("pacto-stats-")+64 {
		t.Fatalf("tag %q", tag)
	}
	if tag != AnchorTag("payments") || tag == AnchorTag("orders") {
		t.Fatal("the tag is not a function of the service")
	}
}

func TestEncodeRefusesInvalid(t *testing.T) {
	r := Record{Version: Version, ID: ID(KindDiff, "s", "", ""), Kind: KindDiff, Service: "s", Outcome: OutcomePassed, RecordedAt: time.Now()}
	if _, err := Encode(r); err != nil {
		t.Fatalf("a valid record: %v", err)
	}
	r.Kind = "coverage"
	if _, err := Encode(r); err == nil {
		t.Fatal("encoded an unknown kind")
	}
}
