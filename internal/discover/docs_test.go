package discover

import (
	"os"
	"strings"
	"testing"
)

// TestUnsupportedTableDocumented pins docs/pactify.md to what discovery
// reads: every finding code and every file or part of a file it recognizes
// but does not read is named there.
func TestUnsupportedTableDocumented(t *testing.T) {
	data, err := os.ReadFile("../../docs/pactify.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	want := []string{
		CodeUnsupportedFile, CodeAmbiguous, CodeChartDependencyUnlocked,
		CodeUnreadable, CodeUnresolved, CodeTruncated,
		// The parts of a file that are not read, beside the file table.
		"helmCharts", "include", "extends", "env_file", "ApplicationSet", "remote",
	}
	for pattern := range unsupportedFiles {
		want = append(want, "`"+pattern+"`")
	}
	for _, w := range want {
		if !strings.Contains(doc, w) {
			t.Errorf("docs/pactify.md does not name %s", w)
		}
	}
}
