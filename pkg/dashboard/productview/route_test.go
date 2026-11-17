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

func TestEntityRouteExternal(t *testing.T) {
	if got := Entity(fleet.KindExternal, "aws/rds/123456789012/eu-west-1/orders-db"); got != "/fleet/externals/aws%2Frds%2F123456789012%2Feu-west-1%2Forders-db" {
		t.Errorf("external href = %q", got)
	}
}
