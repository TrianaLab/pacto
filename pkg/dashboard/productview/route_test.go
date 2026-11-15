package productview

import (
	"net/url"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestChangesHref(t *testing.T) {
	key := fleet.ServiceKey("ghcr.io/acme/payments")
	href := Changes(key, "rev/a b", "rev/c")
	path, query, _ := strings.Cut(href, "?")
	got, err := url.PathUnescape(strings.TrimPrefix(path, Root+"/changes/"))
	if err != nil || got != string(key) {
		t.Fatalf("service key = %q, %v; want %q (href %s)", got, err, key, href)
	}
	v, _ := url.ParseQuery(query)
	if v.Get("old") != "rev/a b" || v.Get("new") != "rev/c" || !strings.HasPrefix(query, "old=") {
		t.Fatalf("query = %q", query)
	}
	if h := Changes(key, "", ""); strings.Contains(h, "?") {
		t.Fatalf("no revisions = %q", h)
	}
	if h := Changes(key, "", "n"); !strings.HasSuffix(h, "?new=n") {
		t.Fatalf("new only = %q", h)
	}
}
