package ignore

import (
	"testing"
	"testing/fstest"
)

func TestIgnoredByReportsLine(t *testing.T) {
	m, err := Load(fstest.MapFS{IgnoreFileName: {Data: []byte("# legacy\n\nservices/legacy/\n")}})
	if err != nil {
		t.Fatal(err)
	}
	if line, ok := m.IgnoredBy("services/legacy", true); line != 3 || !ok {
		t.Errorf("services/legacy = (%d, %v), want (3, true)", line, ok)
	}
	if line, ok := m.IgnoredBy(".git", true); line != 0 || !ok {
		t.Errorf(".git = (%d, %v), want (0, true)", line, ok)
	}
	neg, err := Load(fstest.MapFS{IgnoreFileName: {Data: []byte("# legacy\n\nservices/legacy/\n!services/legacy/\npacto.yaml\n")}})
	if err != nil {
		t.Fatal(err)
	}
	if line, ok := neg.IgnoredBy("services/legacy", true); line != 4 || ok {
		t.Errorf("negated services/legacy = (%d, %v), want (4, false)", line, ok)
	}
	if line, ok := neg.IgnoredBy("pacto.yaml", false); line != 0 || ok {
		t.Errorf("pacto.yaml = (%d, %v), want (0, false)", line, ok)
	}
}

func TestDefaultPatternsExcludePlanFile(t *testing.T) {
	if !New(nil).Ignored("pacto-plan.md", false) {
		t.Error("pacto-plan.md is not ignored by default")
	}
}
