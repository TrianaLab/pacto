package fleet

import (
	"github.com/trianalab/pacto/v4/pkg/contract"
	"reflect"
	"strings"
	"testing"
)

// reachedQuery is changesQuery over two owner revisions whose contract
// digests are "c1" and "c2".
func reachedQuery() (*Query, ChangeHistoryPreview) {
	q := changesQuery([]string{"v1.0.0", "v2.0.0"}, []*contract.Contract{owned("a"), owned("b")})
	q.snap.Revisions["svc@v1.0.0"].Digest = "c1"
	q.snap.Revisions["svc@v2.0.0"].Digest = "c2"
	return q, svcChanges(q)
}

func TestAttachReachedByDigest(t *testing.T) {
	q, h := reachedQuery()
	prod := ChangeReach{Namespace: "prod", Stage: "payments", At: "2026-11-01T00:00:00Z", Record: "r1", Verified: true}
	total := h.Total
	q.AttachReached(&h, map[string][]ChangeReach{"c2": {prod}, "c9": {prod}}, TimesPartial, "namespace dev was not read", []string{"dev"})
	if h.Times != TimesPartial || h.TimesReason != "namespace dev was not read" || !reflect.DeepEqual(h.TimesUnreadable, []string{"dev"}) || h.Total != total {
		t.Fatalf("times %q %q %v total %d", h.Times, h.TimesReason, h.TimesUnreadable, h.Total)
	}
	if !reflect.DeepEqual(h.Items[0].Reached, []ChangeReach{prod}) || h.Items[1].Reached != nil {
		t.Errorf("reached %+v / %+v", h.Items[0].Reached, h.Items[1].Reached)
	}
}

func TestChangesLinesTimes(t *testing.T) {
	signed := ChangeReach{Stage: "prod", At: "2026-11-01T00:00:00Z", Record: "sha256:0123456789abcdef0123", Verified: true}
	unverified := ChangeReach{Stage: "dev", At: "2026-10-01T00:00:00Z", Record: "r1"}
	status := ChangeReach{Stage: "qa", At: "2026-10-02T00:00:00Z", FromStatus: true}
	for _, c := range []struct {
		name, times, reason string
		reached             []ChangeReach
		want                string
	}{
		{"not read", TimesNotRead, "ask with reached to read delivery records", nil, "time not read"},
		{"read, none", TimesRead, "", nil, "time not recorded"},
		{"partial, none", TimesPartial, "namespace dev refused", nil, "time not recorded in the Stages read (namespace dev refused)"},
		{"hidden", TimesHidden, "you may not list Stages", nil, "time hidden (you may not list Stages)"},
		{"signed", TimesRead, "", []ChangeReach{signed}, "reached prod 2026-11-01T00:00:00Z (record sha256:0123456789ab)"},
		{"unverified", TimesRead, "", []ChangeReach{unverified}, "reached dev 2026-10-01T00:00:00Z (recorded, signature not checked)"},
		{"from status", TimesPartial, "x", []ChangeReach{status, signed}, "reached qa 2026-10-02T00:00:00Z (from status, not signed); reached prod 2026-11-01T00:00:00Z (record sha256:0123456789ab)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			q, h := reachedQuery()
			q.AttachReached(&h, map[string][]ChangeReach{"c2": c.reached}, c.times, c.reason, nil)
			if l := h.Lines()[0]; !strings.HasSuffix(l, "  "+c.want) {
				t.Errorf("line %q, want suffix %q", l, c.want)
			}
		})
	}
}

func TestChangesHeadingNamesTimes(t *testing.T) {
	q, h := reachedQuery()
	if !strings.Contains(h.Heading(), "times not-read: ask with reached to read delivery records") {
		t.Errorf("heading %q", h.Heading())
	}
	q.AttachReached(&h, nil, TimesRead, "ignored", nil)
	if strings.Contains(h.Heading(), "times") {
		t.Errorf("heading %q", h.Heading())
	}
}
