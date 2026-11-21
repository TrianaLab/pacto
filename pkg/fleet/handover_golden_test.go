package fleet

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/handover.golden.json")

// TestHandoverGolden pins what every surface reads about a hand-over: the
// replacement, the service's fields and replaces, its explanation and the
// fields of its page.
func TestHandoverGolden(t *testing.T) {
	q := NewQuery(handSnap(t, []RawRevision{fieldsRev(t, fieldsOpts{prov: anaSigned(), accepted: ledgerAccepted})}, nil, true))
	v, err := q.GetService("invoices")
	if err != nil {
		t.Fatal(err)
	}
	e, err := q.Explain("invoices")
	if err != nil {
		t.Fatal(err)
	}
	d, err := q.EntityDetail(KindService, string(invoicesKey))
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.MarshalIndent(map[string]any{
		"replacements": q.snap.Replacements,
		"service":      map[string]any{"fields": v.Fields, "replaces": v.Replaces, "replacesTotal": v.ReplacesTotal},
		"explain":      map[string]any{"reasons": e.Reasons, "fields": e.Fields},
		"detailFields": d.Service.Fields,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	const path = "testdata/handover.golden.json"
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch (run with -update to rewrite):\n%s", got)
	}
}
