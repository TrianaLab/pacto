package discover

import "testing"

// The Declare text is persisted in pacto.inferred.yaml and printed by
// pacto ci --explain, so its bytes never move.
func TestExternalYAMLBytesUnchanged(t *testing.T) {
	want := "dependencies:\n  - name: legacy\n    external: true\n    identity: host/corp-legacy.example\n    required: true\n    version: <the API version you call>\n"
	if got := externalYAML("legacy", "host/corp-legacy.example"); got != want {
		t.Fatalf("got:\n%s", got)
	}
}
