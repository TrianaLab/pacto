package diff

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/schemax"
)

const inferredReason = "inferred by Pacto, not part of the declared contract"

func bareContract() *contract.Contract {
	return &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "svc", Version: "1.0.0"}}
}

// recordFS is a bundle FS holding only an inferred record with these fields.
func recordFS(commit string, fields ...contract.Inferred) fstest.MapFS {
	r := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Commit: commit, Pacto: "4.6.0", Discovery: "1"}, Fields: fields}
	return fstest.MapFS{contract.InferredRecordFile: {Data: r.Marshal()}}
}

func field(name, value, source string) contract.Inferred {
	return contract.Inferred{Field: name, Value: value, Source: source, Confidence: contract.ConfidenceLikely}
}

func inferredChanges(r *Result) []Change {
	var out []Change
	for _, c := range r.Changes {
		if strings.HasPrefix(c.Path, "inferred.") {
			out = append(out, c)
		}
	}
	return out
}

func TestDiffInferredOnlyNonBreaking(t *testing.T) {
	c := bareContract()
	oldFS := recordFS("a", field("workload", "service", "deploy/app.yaml:3"), field("configuration.PORT", "string", "deploy/app.yaml:9"))
	newFS := recordFS("a", field("configuration.PORT", "integer", "deploy/app.yaml:9"), field("dependencies[billing]", "billing", "deploy/app.yaml:12"))
	r := Compare(context.Background(), c, c, oldFS, newFS)
	got := inferredChanges(r)
	if len(got) != 3 || r.Classification != NonBreaking {
		t.Fatalf("changes %+v, overall %s", got, r.Classification)
	}
	want := map[string]ChangeType{"inferred.workload": Removed, "inferred.configuration.PORT": Modified, "inferred.dependencies[billing]": Added}
	for _, ch := range got {
		if want[ch.Path] != ch.Type || ch.Classification != NonBreaking || ch.Reason != inferredReason {
			t.Errorf("change %+v", ch)
		}
	}
}

func TestDiffInferredIgnoresInputs(t *testing.T) {
	c := bareContract()
	f := field("workload", "service", "deploy/app.yaml:3")
	if got := inferredChanges(Compare(context.Background(), c, c, recordFS("a", f), recordFS("b", f))); len(got) != 0 {
		t.Fatalf("inputs-only diff: %+v", got)
	}
	// A value read from another line is a change, shown by its sources.
	moved := field("workload", "service", "deploy/app.yaml:5")
	got := inferredChanges(Compare(context.Background(), c, c, recordFS("a", f), recordFS("a", moved)))
	if len(got) != 1 || got[0].OldValue != "deploy/app.yaml:3" || got[0].NewValue != "deploy/app.yaml:5" {
		t.Fatalf("moved source: %+v", got)
	}
}

func TestDiffInferredOneSideMissing(t *testing.T) {
	c := bareContract()
	newFS := recordFS("a", field("workload", "service", "deploy/app.yaml:3"))
	got := inferredChanges(Compare(context.Background(), c, c, fstest.MapFS{}, newFS))
	if len(got) != 1 || got[0].Type != Added || got[0].NewValue != "service" {
		t.Fatalf("one side missing: %+v", got)
	}
	if got := inferredChanges(Compare(context.Background(), c, c, nil, nil)); len(got) != 0 {
		t.Fatalf("nil FS: %+v", got)
	}
	// An unreadable record compares as empty.
	bad := fstest.MapFS{contract.InferredRecordFile: {Data: []byte("format: other\n")}}
	if got := inferredChanges(Compare(context.Background(), c, c, bad, bad)); len(got) != 0 {
		t.Fatalf("unreadable record: %+v", got)
	}
}

func TestDiffInferredBracketedPathsNonBreaking(t *testing.T) {
	c := bareContract()
	oldFS := recordFS("a", field("capabilities[health]", "GET /healthz", "deploy/app.yaml:31"),
		field("configuration.DB_PASSWORD", "string", "deploy/app.yaml:24"))
	newFS := recordFS("a", field("capabilities[health]", "GET /readyz", "deploy/app.yaml:31"),
		field("configuration.DB_PASSWORD", "string, writeOnly", "deploy/app.yaml:24"))
	got := inferredChanges(Compare(context.Background(), c, c, oldFS, newFS))
	if len(got) != 2 {
		t.Fatalf("changes %+v", got)
	}
	for _, ch := range got {
		if ch.Classification != NonBreaking || ch.Reason != inferredReason {
			t.Errorf("change %+v", ch)
		}
	}
	// A sensitive key's summary is masked like any sensitive configuration change.
	if pw := got[1]; pw.Path != "inferred.configuration.DB_PASSWORD" || pw.OldValue != schemax.Sensitive || pw.NewValue != schemax.Sensitive {
		t.Errorf("sensitive key not masked: %+v", pw)
	}
}
