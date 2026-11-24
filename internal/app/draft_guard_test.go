package app

import "testing"

// pacto ci picks its baseline among versions only: draft-<n> tags written by
// `pacto declare --draft` hold other content and are never the base, even when
// they were pushed after the published version.
func TestDraftTagsNeverACIBase(t *testing.T) {
	t.Parallel()
	e := newCIRepo(t)
	e.write(t, map[string]string{"reports/pacto.yaml": reportsV1})
	published := e.publish(t, "reports", "acme/reports", "1.0.0", nil)
	e.write(t, map[string]string{"reports/notes.txt": "a draft waiting for a person\n"})
	for _, tag := range []string{"draft-1", "draft-2"} {
		if d := e.publish(t, "reports", "acme/reports", tag, nil); d == published {
			t.Fatalf("%s must hold other content than 1.0.0", tag)
		}
	}
	tags, err := e.reg.Tags(t.Context(), e.host+"/acme/reports")
	if err != nil || len(tags) != 3 {
		t.Fatalf("tags = %v, %v; want 1.0.0 and two drafts", tags, err)
	}
	if r := row(t, e.plan(t), "reports"); r.Baseline != "1.0.0" || r.BaselineDigest != published {
		t.Fatalf("baseline = %s@%s, want 1.0.0@%s (row %+v)", r.Baseline, r.BaselineDigest, published, r)
	}
}
